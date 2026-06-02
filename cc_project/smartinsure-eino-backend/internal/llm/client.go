package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

type Client struct {
	Provider   ProviderConfig
	HTTPClient *http.Client
	Timeout    time.Duration
	MaxRetries int
}

type StreamChunk struct {
	Text string
	Err  error
}

// ChatModel is the narrow interface expected by downstream Eino graph nodes.
// It can be wrapped by an Eino adapter once the root module dependencies exist.
type ChatModel interface {
	CallText(ctx context.Context, messages []Message, temperature float64) (string, error)
	CallJSON(ctx context.Context, messages []Message, temperature float64, out any) error
	StreamText(ctx context.Context, messages []Message, temperature float64) (<-chan StreamChunk, error)
}

func NewClient(provider ProviderConfig, timeout time.Duration, maxRetries int) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		Provider:   provider,
		HTTPClient: &http.Client{Timeout: timeout},
		Timeout:    timeout,
		MaxRetries: maxRetries,
	}
}

func (c *Client) CallText(ctx context.Context, messages []Message, temperature float64) (string, error) {
	content, err := c.completion(ctx, messages, temperature, false)
	if err != nil {
		return "", err
	}
	return StripThink(content), nil
}

func (c *Client) CallJSON(ctx context.Context, messages []Message, temperature float64, out any) error {
	var lastErr error
	for i := 0; i < 2; i++ {
		content, err := c.CallText(ctx, messages, temperature)
		if err != nil {
			return err
		}
		content = StripMarkdownFence(content)
		if err := json.Unmarshal([]byte(content), out); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("llm returned non-json content: %w", lastErr)
}

func (c *Client) StreamText(ctx context.Context, messages []Message, temperature float64) (<-chan StreamChunk, error) {
	req, err := c.newRequest(ctx, messages, temperature, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("llm stream failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	out := make(chan StreamChunk)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		filter := newThinkStreamFilter()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				return
			}
			text, err := parseStreamDelta(payload)
			if err != nil {
				out <- StreamChunk{Err: err}
				return
			}
			if text = filter.feed(text); text != "" {
				out <- StreamChunk{Text: text}
			}
		}
		if err := scanner.Err(); err != nil {
			out <- StreamChunk{Err: err}
		}
	}()
	return out, nil
}

func (c *Client) completion(ctx context.Context, messages []Message, temperature float64, stream bool) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		req, err := c.newRequest(ctx, messages, temperature, stream)
		if err != nil {
			return "", err
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			content, err := readCompletion(resp)
			if err == nil {
				return content, nil
			}
			lastErr = err
		}
		if attempt < c.MaxRetries {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}
	}
	return "", lastErr
}

func (c *Client) newRequest(ctx context.Context, messages []Message, temperature float64, stream bool) (*http.Request, error) {
	if c.Provider.Key == "" {
		return nil, errors.New("llm api key is empty")
	}
	if c.Provider.Base == "" {
		return nil, errors.New("llm api base is empty")
	}
	model := c.Provider.Model
	if strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		model = parts[1]
	}
	payload := map[string]any{
		"model":       model,
		"messages":    messages,
		"temperature": temperature,
		"stream":      stream,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(c.Provider.Base, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Provider.Key)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func readCompletion(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("llm completion failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("llm response has no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

func parseStreamDelta(payload string) (string, error) {
	var parsed struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", nil
	}
	return parsed.Choices[0].Delta.Content, nil
}

var thinkBlockRE = regexp.MustCompile(`(?s)<think>.*?</think>`)

func StripThink(text string) string {
	return strings.TrimSpace(thinkBlockRE.ReplaceAllString(text, ""))
}

func StripMarkdownFence(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

type thinkStreamFilter struct {
	inThink bool
}

func newThinkStreamFilter() *thinkStreamFilter {
	return &thinkStreamFilter{}
}

func (f *thinkStreamFilter) feed(text string) string {
	if text == "" {
		return ""
	}
	if f.inThink {
		end := strings.Index(text, "</think>")
		if end < 0 {
			return ""
		}
		f.inThink = false
		text = text[end+len("</think>"):]
	}
	start := strings.Index(text, "<think>")
	if start < 0 {
		return text
	}
	before := text[:start]
	after := text[start+len("<think>"):]
	end := strings.Index(after, "</think>")
	if end < 0 {
		f.inThink = true
		return before
	}
	return before + after[end+len("</think>"):]
}
