package mcp

import (
	"encoding/json"
	"net/url"
	"strings"

	"smartinsure-eino-backend/internal/schema"
)

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
}

type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func ParseSearchResponse(resp jsonRPCResponse) []schema.SearchResultItem {
	if len(resp.Result) == 0 {
		return []schema.SearchResultItem{}
	}
	var result toolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return []schema.SearchResultItem{}
	}

	items := make([]schema.SearchResultItem, 0)
	for _, content := range result.Content {
		if content.Type != "text" || strings.TrimSpace(content.Text) == "" {
			continue
		}
		items = append(items, ParseSearchText(content.Text)...)
	}
	return dedupe(items)
}

func ParseSearchText(text string) []schema.SearchResultItem {
	text = strings.TrimSpace(text)
	if text == "" {
		return []schema.SearchResultItem{}
	}

	var data any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return []schema.SearchResultItem{}
	}

	switch typed := data.(type) {
	case []any:
		return parseRawList(typed)
	case map[string]any:
		for _, key := range []string{"results", "organic", "items"} {
			if list, ok := typed[key].([]any); ok {
				return parseRawList(list)
			}
		}
		if nested, ok := typed["data"].(map[string]any); ok {
			for _, key := range []string{"results", "organic", "items"} {
				if list, ok := nested[key].([]any); ok {
					return parseRawList(list)
				}
			}
		}
	}
	return []schema.SearchResultItem{}
}

func parseRawList(raw []any) []schema.SearchResultItem {
	items := make([]schema.SearchResultItem, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		title := firstString(m, "title", "name")
		link := firstString(m, "url", "link")
		snippet := firstString(m, "description", "snippet", "desc")
		if title == "" || link == "" {
			continue
		}
		lowerURL := strings.ToLower(link)
		if strings.Contains(lowerURL, "douyin.com") || strings.Contains(lowerURL, "tiktok.com") {
			continue
		}
		if snippet == "" {
			snippet = title
		}
		items = append(items, schema.SearchResultItem{
			Title:   title,
			URL:     link,
			Site:    extractSite(link),
			Snippet: truncate(snippet, 300),
		})
	}
	return items
}

func firstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := data[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func dedupe(items []schema.SearchResultItem) []schema.SearchResultItem {
	seen := map[string]struct{}{}
	out := make([]schema.SearchResultItem, 0, len(items))
	for _, item := range items {
		key := normalizeURL(item.URL)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func extractSite(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func normalizeURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.Fragment = ""
	return parsed.String()
}

func truncate(text string, max int) string {
	runes := []rune(strings.TrimSpace(text))
	if max <= 0 || len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max])
}
