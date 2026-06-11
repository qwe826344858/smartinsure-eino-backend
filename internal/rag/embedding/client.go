package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"
)

const (
	DefaultTimeout   = 30 * time.Second
	DefaultBatchSize = 16
)

type Config struct {
	Provider   Provider
	APIBase    string
	APIKey     string
	Model      string
	Region     string
	APIType    string
	Timeout    time.Duration
	BatchSize  int
	Dimensions int
	RetryTimes int
	HTTPClient *http.Client
}

type Client struct {
	apiBase    string
	apiKey     string
	model      string
	timeout    time.Duration
	batchSize  int
	dimensions int
	httpClient *http.Client
}

type Embedder interface {
	EmbedTexts(ctx context.Context, texts []string) ([][]float64, error)
}

func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	logx.Printf("运行日志", "runtime log", "embedding openai_compatible init_success model=%s dimensions=%d batch_size=%d timeout_seconds=%d", cfg.Model, cfg.Dimensions, batchSize, int(timeout.Seconds()))
	return &Client{
		apiBase:    strings.TrimRight(cfg.APIBase, "/"),
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		timeout:    timeout,
		batchSize:  batchSize,
		dimensions: cfg.Dimensions,
		httpClient: httpClient,
	}
}

func (c *Client) EmbedTexts(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if err := c.validate(); err != nil {
		return nil, err
	}

	vectors := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += c.batchSize {
		batchStartedAt := time.Now()
		end := start + c.batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batchVectors, err := c.embedBatch(ctx, texts[start:end])
		if err != nil {
			logx.Printf("运行日志", "runtime log", "embedding openai_compatible batch_failed start=%d end=%d size=%d duration_ms=%d err=%v", start, end, end-start, time.Since(batchStartedAt).Milliseconds(), err)
			return nil, err
		}
		dims := 0
		if len(batchVectors) > 0 {
			dims = len(batchVectors[0])
		}
		logx.Printf("运行日志", "runtime log", "embedding openai_compatible batch_success start=%d end=%d size=%d dims=%d duration_ms=%d", start, end, len(batchVectors), dims, time.Since(batchStartedAt).Milliseconds())
		vectors = append(vectors, batchVectors...)
	}
	return vectors, nil
}

func (c *Client) validate() error {
	if c == nil {
		return errors.New("embedding client is nil")
	}
	if strings.TrimSpace(c.apiBase) == "" {
		return errors.New("embedding api base is empty")
	}
	if strings.TrimSpace(c.apiKey) == "" {
		return errors.New("embedding api key is empty")
	}
	if strings.TrimSpace(c.model) == "" {
		return errors.New("embedding model is empty")
	}
	if c.batchSize <= 0 {
		return errors.New("embedding batch size must be greater than 0")
	}
	if c.httpClient == nil {
		return errors.New("embedding http client is nil")
	}
	return nil
}

func (c *Client) embedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	payload := map[string]any{
		"model": c.model,
		"input": texts,
	}
	if c.dimensions > 0 {
		payload["dimensions"] = c.dimensions
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.apiBase+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding request failed: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var parsed embeddingResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embedding response count mismatch: got=%d want=%d", len(parsed.Data), len(texts))
	}
	return orderedEmbeddings(parsed.Data)
}

type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}

type embeddingData struct {
	Index     *int      `json:"index,omitempty"`
	Embedding []float64 `json:"embedding"`
}

func orderedEmbeddings(data []embeddingData) ([][]float64, error) {
	hasIndex := true
	for _, item := range data {
		if item.Index == nil {
			hasIndex = false
			break
		}
	}
	if !hasIndex {
		out := make([][]float64, len(data))
		for i, item := range data {
			if len(item.Embedding) == 0 {
				return nil, fmt.Errorf("embedding at position %d is empty", i)
			}
			out[i] = item.Embedding
		}
		return out, nil
	}

	out := make([][]float64, len(data))
	for _, item := range data {
		if item.Index == nil || *item.Index < 0 || *item.Index >= len(data) {
			return nil, fmt.Errorf("embedding response index out of range")
		}
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("embedding at index %d is empty", *item.Index)
		}
		if out[*item.Index] != nil {
			return nil, fmt.Errorf("duplicate embedding response index %d", *item.Index)
		}
		out[*item.Index] = item.Embedding
	}
	return out, nil
}
