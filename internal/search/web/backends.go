package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/schema"
)

const defaultMiniMaxEndpoint = "https://api.minimaxi.com/v1/coding_plan/search"

type MiniMaxBackend struct {
	endpoint string
	apiKey   string
	client   *http.Client
	timeout  time.Duration
}

func NewMiniMaxBackend(opts MiniMaxOptions) *MiniMaxBackend {
	endpoint := strings.TrimSpace(opts.Endpoint)
	if endpoint == "" {
		endpoint = defaultMiniMaxEndpoint
	}
	client := opts.HTTPClient
	if client == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	return &MiniMaxBackend{
		endpoint: endpoint,
		apiKey:   strings.TrimSpace(opts.APIKey),
		client:   client,
		timeout:  opts.Timeout,
	}
}

func (b *MiniMaxBackend) Search(ctx context.Context, query string) ([]schema.SearchResultItem, error) {
	if b == nil || strings.TrimSpace(b.apiKey) == "" {
		return nil, fmt.Errorf("minimax search api key is empty")
	}
	payload, err := json.Marshal(map[string]string{"q": query})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("minimax search failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return ParseMiniMaxResponse(body)
}

type ExternalBackend struct {
	endpoint string
	apiKey   string
	client   *http.Client
	topN     int
}

func NewExternalBackend(opts ExternalOptions) *ExternalBackend {
	client := opts.HTTPClient
	if client == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	topN := opts.TopN
	if topN <= 0 {
		topN = 10
	}
	return &ExternalBackend{
		endpoint: strings.TrimSpace(opts.Endpoint),
		apiKey:   strings.TrimSpace(opts.APIKey),
		client:   client,
		topN:     topN,
	}
}

func (b *ExternalBackend) Search(ctx context.Context, query string) ([]schema.SearchResultItem, error) {
	if b == nil || b.endpoint == "" || b.apiKey == "" {
		return nil, fmt.Errorf("external search is not configured")
	}
	parsed, err := url.Parse(b.endpoint)
	if err != nil {
		return nil, err
	}
	params := parsed.Query()
	params.Set("q", query)
	params.Set("count", fmt.Sprintf("%d", b.topN))
	parsed.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.apiKey)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("external search failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return ParseExternalResponse(body)
}

func ParseMiniMaxResponse(body []byte) ([]schema.SearchResultItem, error) {
	var data struct {
		BaseResp struct {
			StatusCode int `json:"status_code"`
		} `json:"base_resp"`
		Organic []map[string]any `json:"organic"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	if data.BaseResp.StatusCode != 0 {
		return []schema.SearchResultItem{}, nil
	}
	return parseRawResults(data.Organic), nil
}

func ParseExternalResponse(body []byte) ([]schema.SearchResultItem, error) {
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	var raw any
	switch {
	case data["results"] != nil:
		raw = data["results"]
	case data["items"] != nil:
		raw = data["items"]
	default:
		if nested, ok := data["data"].(map[string]any); ok {
			raw = nested["results"]
			if raw == nil {
				raw = nested["items"]
			}
		}
	}

	rawList, ok := raw.([]any)
	if !ok {
		return []schema.SearchResultItem{}, nil
	}
	items := make([]map[string]any, 0, len(rawList))
	for _, item := range rawList {
		if m, ok := item.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return parseRawResults(items), nil
}

func parseRawResults(raw []map[string]any) []schema.SearchResultItem {
	items := make([]schema.SearchResultItem, 0, len(raw))
	for _, r := range raw {
		title := firstString(r, "title", "name")
		link := firstString(r, "url", "link")
		snippet := firstString(r, "snippet", "description", "desc")
		if strings.TrimSpace(title) == "" || strings.TrimSpace(link) == "" {
			continue
		}
		if snippet == "" {
			snippet = title
		}
		items = append(items, schema.SearchResultItem{
			Title:   strings.TrimSpace(title),
			URL:     strings.TrimSpace(link),
			Site:    ExtractSite(link),
			Snippet: truncateRunes(strings.TrimSpace(snippet), 300),
		})
	}
	return items
}

func firstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := data[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncateRunes(text string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n])
}
