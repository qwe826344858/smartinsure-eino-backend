package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"smartinsure-eino-backend/internal/schema"
)

const protocolVersion = "2024-11-05"

type Client struct {
	BaseURL       string
	HTTPClient    *http.Client
	Engines       []string
	ReadyTimeout  time.Duration
	ResultTimeout time.Duration
	nextRequestID atomic.Int64
	clientName    string
	clientVersion string
}

type Option func(*Client)

func NewClient(baseURL string, opts ...Option) *Client {
	c := &Client{
		BaseURL:       strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		HTTPClient:    &http.Client{Timeout: 60 * time.Second},
		Engines:       []string{"baidu", "bing"},
		ReadyTimeout:  8 * time.Second,
		ResultTimeout: 20 * time.Second,
		clientName:    "smartinsure",
		clientVersion: "1.0",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.HTTPClient = client
		}
	}
}

func WithEngines(engines []string) Option {
	return func(c *Client) {
		c.Engines = compactStrings(engines)
	}
}

func WithTimeouts(ready, result time.Duration) Option {
	return func(c *Client) {
		if ready > 0 {
			c.ReadyTimeout = ready
		}
		if result > 0 {
			c.ResultTimeout = result
		}
	}
}

func (c *Client) Search(ctx context.Context, query string, limit int) ([]schema.SearchResultItem, error) {
	if c == nil || c.BaseURL == "" {
		return nil, fmt.Errorf("mcp base url is empty")
	}
	if limit <= 0 {
		limit = 5
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sessionCh := make(chan string, 1)
	resultCh := make(chan []schema.SearchResultItem, 1)
	errCh := make(chan error, 1)
	callID := c.nextID()

	go c.listenSSE(ctx, sessionCh, resultCh, errCh, callID)

	sessionURL, err := waitSession(ctx, sessionCh, errCh, c.ReadyTimeout)
	if err != nil {
		return nil, err
	}
	fullURL, err := c.resolveSessionURL(sessionURL)
	if err != nil {
		return nil, err
	}

	if err := c.postJSONRPC(ctx, fullURL, initializeRequest(c.clientName, c.clientVersion)); err != nil {
		return nil, err
	}
	if err := c.postJSONRPC(ctx, fullURL, initializedNotification()); err != nil {
		return nil, err
	}
	if err := c.postJSONRPC(ctx, fullURL, toolsCallRequest(callID, query, limit, c.Engines)); err != nil {
		return nil, err
	}

	timer := time.NewTimer(c.ResultTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		return nil, err
	case results := <-resultCh:
		return results, nil
	case <-timer.C:
		return nil, fmt.Errorf("mcp search timeout")
	}
}

func (c *Client) listenSSE(ctx context.Context, sessionCh chan<- string, resultCh chan<- []schema.SearchResultItem, errCh chan<- error, callID int64) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/sse", nil)
	if err != nil {
		sendErr(errCh, err)
		return
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		sendErr(errCh, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		sendErr(errCh, fmt.Errorf("mcp sse failed: status=%d body=%s", resp.StatusCode, string(body)))
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if strings.HasPrefix(payload, "/messages") || strings.HasPrefix(payload, "http://") || strings.HasPrefix(payload, "https://") {
			select {
			case sessionCh <- payload:
			default:
			}
			continue
		}
		if !strings.HasPrefix(payload, "{") {
			continue
		}
		var rpc jsonRPCResponse
		if err := json.Unmarshal([]byte(payload), &rpc); err != nil {
			continue
		}
		if rpc.ID == callID && len(rpc.Result) > 0 {
			results := ParseSearchResponse(rpc)
			select {
			case resultCh <- results:
			default:
			}
			return
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		sendErr(errCh, err)
	}
}

func (c *Client) postJSONRPC(ctx context.Context, endpoint string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("mcp post failed: status=%d body=%s", resp.StatusCode, string(data))
	}
	return nil
}

func (c *Client) resolveSessionURL(session string) (string, error) {
	if strings.HasPrefix(session, "http://") || strings.HasPrefix(session, "https://") {
		return session, nil
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(session)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

func (c *Client) nextID() int64 {
	id := c.nextRequestID.Add(1)
	if id <= 0 {
		return 1
	}
	return id
}

func waitSession(ctx context.Context, sessionCh <-chan string, errCh <-chan error, timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-errCh:
		return "", err
	case session := <-sessionCh:
		if strings.TrimSpace(session) == "" {
			return "", fmt.Errorf("mcp session url is empty")
		}
		return session, nil
	case <-timer.C:
		return "", fmt.Errorf("mcp sse session timeout")
	}
}

func sendErr(errCh chan<- error, err error) {
	select {
	case errCh <- err:
	default:
	}
}

func initializeRequest(name, version string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]string{
				"name":    name,
				"version": version,
			},
		},
	}
}

func initializedNotification() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
}

func toolsCallRequest(id int64, query string, limit int, engines []string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "search",
			"arguments": map[string]any{
				"query":   query,
				"limit":   limit,
				"engines": engines,
			},
		},
	}
}

func compactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
