package web

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"smartinsure-eino-backend/internal/schema"
)

func (s *Service) Search(ctx context.Context, queries []string) ([]schema.SearchResultItem, error) {
	if s == nil {
		s = NewService(Options{})
	}
	queries = compactQueries(queries)
	if len(queries) == 0 {
		return []schema.SearchResultItem{}, nil
	}

	results := s.searchConcurrent(ctx, queries)
	cleaned := s.filterLowQuality(deduplicate(results))
	if len(cleaned) == 0 && s.fallback != nil {
		cleaned = deduplicate(s.fallback.SearchAll(queries))
	}
	if len(cleaned) > s.maxResults {
		cleaned = cleaned[:s.maxResults]
	}
	return cleaned, ctx.Err()
}

func (s *Service) searchConcurrent(ctx context.Context, queries []string) []schema.SearchResultItem {
	type result struct {
		items []schema.SearchResultItem
	}

	out := make(chan result, len(queries))
	sem := make(chan struct{}, s.maxConcurrency)
	var wg sync.WaitGroup

	for _, q := range queries {
		query := q
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			items := s.searchSingle(ctx, query)
			out <- result{items: items}
		}()
	}

	wg.Wait()
	close(out)

	items := make([]schema.SearchResultItem, 0)
	for res := range out {
		items = append(items, res.items...)
	}
	return items
}

func (s *Service) searchSingle(ctx context.Context, query string) []schema.SearchResultItem {
	for _, backend := range s.backends {
		if backend == nil {
			continue
		}
		items, err := backend.Search(ctx, query)
		if err == nil && len(items) > 0 {
			return items
		}
	}
	if s.fallback == nil {
		return nil
	}
	return s.fallback.Search(query)
}

func (s *Service) filterLowQuality(results []schema.SearchResultItem) []schema.SearchResultItem {
	out := make([]schema.SearchResultItem, 0, len(results))
	for _, item := range results {
		item.Title = strings.TrimSpace(item.Title)
		item.URL = strings.TrimSpace(item.URL)
		item.Site = strings.TrimSpace(item.Site)
		item.Snippet = strings.TrimSpace(item.Snippet)
		if item.Title == "" || item.URL == "" || item.Snippet == "" {
			continue
		}
		if item.Site == "" {
			item.Site = ExtractSite(item.URL)
		}
		combined := item.Title + item.Snippet
		if containsAny(combined, s.lowQuality) {
			continue
		}
		lowerURL := strings.ToLower(item.URL)
		if strings.Contains(lowerURL, "douyin.com") || strings.Contains(lowerURL, "tiktok.com") {
			continue
		}
		out = append(out, item)
	}
	return out
}

func deduplicate(results []schema.SearchResultItem) []schema.SearchResultItem {
	seen := map[string]struct{}{}
	out := make([]schema.SearchResultItem, 0, len(results))
	for _, item := range results {
		key := NormalizeURL(item.URL)
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

func compactQueries(queries []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(queries))
	for _, q := range queries {
		q = strings.Join(strings.Fields(strings.TrimSpace(q)), " ")
		if q == "" {
			continue
		}
		if _, ok := seen[q]; ok {
			continue
		}
		seen[q] = struct{}{}
		out = append(out, q)
	}
	return out
}

func containsAny(text string, needles []string) bool {
	for _, kw := range needles {
		if kw != "" && strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func NormalizeURL(raw string) string {
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

func ExtractSite(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Host
}
