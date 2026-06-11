package productlegacy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/search/parsers"
	"smartinsure-eino-backend/internal/search/web"
)

type Searcher interface {
	Search(ctx context.Context, query string) ([]schema.SearchResultItem, error)
}

type Service struct {
	searcher    Searcher
	httpClient  *http.Client
	platforms   []Platform
	maxProducts int
	now         func() time.Time
}

type Options struct {
	Searcher    Searcher
	HTTPClient  *http.Client
	Platforms   []Platform
	MaxProducts int
	Now         func() time.Time
}

func NewService(opts Options) *Service {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	platforms := opts.Platforms
	if len(platforms) == 0 {
		platforms = DefaultPlatforms()
	}
	maxProducts := opts.MaxProducts
	if maxProducts <= 0 {
		maxProducts = 5
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		searcher:    opts.Searcher,
		httpClient:  client,
		platforms:   append([]Platform(nil), platforms...),
		maxProducts: maxProducts,
		now:         now,
	}
}

func NewMiniMaxService(apiKey string, client *http.Client) *Service {
	return NewService(Options{
		Searcher: web.NewMiniMaxBackend(web.MiniMaxOptions{
			APIKey:     apiKey,
			HTTPClient: client,
		}),
		HTTPClient: client,
	})
}

func (s *Service) SearchProducts(ctx context.Context, keyword string) ([]schema.ProductCard, error) {
	if s == nil {
		s = NewService(Options{})
	}
	if s.searcher == nil {
		return []schema.ProductCard{}, nil
	}

	searchKeyword := normalizeKeyword(keyword)
	queries := make([]string, 0, len(s.platforms))
	for _, platform := range s.platforms {
		queries = append(queries, fmt.Sprintf("site:%s %s %d", platform.Domain, searchKeyword, s.now().Year()))
	}

	results := s.searchPlatforms(ctx, queries)
	cards := CardsFromSearchResults(results, s.platforms)
	if len(cards) > s.maxProducts {
		cards = cards[:s.maxProducts]
	}
	return cards, ctx.Err()
}

func (s *Service) EnrichProducts(ctx context.Context, products []schema.ProductCard) []schema.ProductCard {
	if len(products) == 0 {
		return products
	}
	out := make([]schema.ProductCard, len(products))
	var wg sync.WaitGroup
	for i := range products {
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[idx] = s.EnrichProduct(ctx, products[idx])
		}()
	}
	wg.Wait()
	return out
}

func (s *Service) EnrichProduct(ctx context.Context, product schema.ProductCard) schema.ProductCard {
	if s == nil {
		s = NewService(Options{})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, product.URL, nil)
	if err != nil {
		product.PriceLabel = "查看详情"
		return product
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		product.PriceLabel = "查看详情"
		return product
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		product.PriceLabel = "查看详情"
		return product
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		product.PriceLabel = "查看详情"
		return product
	}

	detail := parsers.GetParser(product.URL)(string(body))
	if detail.Price != "" {
		product.Price = detail.Price
		product.PriceLabel = detail.Price
	}
	if product.Company == "" && detail.Company != "" {
		product.Company = detail.Company
	}
	if detail.Brief != "" && len([]rune(detail.Brief)) > len([]rune(product.Brief)) {
		product.Brief = detail.Brief
	}
	product.Tags = mergeTags(product.Tags, detail.Tags, 4)
	if product.PriceLabel == "" || product.PriceLabel == "加载中" {
		product.PriceLabel = "查看详情"
	}
	return product
}

func (s *Service) searchPlatforms(ctx context.Context, queries []string) []schema.SearchResultItem {
	type result struct {
		items []schema.SearchResultItem
	}
	ch := make(chan result, len(queries))
	var wg sync.WaitGroup
	for _, q := range queries {
		query := q
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := s.searcher.Search(ctx, query)
			if err == nil {
				ch <- result{items: items}
			}
		}()
	}
	wg.Wait()
	close(ch)

	out := make([]schema.SearchResultItem, 0)
	for res := range ch {
		out = append(out, res.items...)
	}
	return out
}

func CardsFromSearchResults(results []schema.SearchResultItem, platforms []Platform) []schema.ProductCard {
	if len(platforms) == 0 {
		platforms = DefaultPlatforms()
	}
	seen := map[string]struct{}{}
	cards := make([]schema.ProductCard, 0)
	for _, result := range results {
		platform, ok := MatchPlatform(result.URL, platforms)
		if !ok {
			continue
		}
		key := normalizeProductURL(result.URL)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		cards = append(cards, CardFromSearchResult(result, platform))
	}
	return cards
}

func CardFromSearchResult(result schema.SearchResultItem, platform Platform) schema.ProductCard {
	name := cleanProductName(result.Title)
	company := extractCompany(name)
	brief := truncate(result.Snippet, 80)
	return schema.ProductCard{
		ID:         productID(result.URL),
		Name:       name,
		Company:    company,
		PriceLabel: "加载中",
		Tags:       parsers.ExtractTags(name, brief),
		URL:        strings.TrimSpace(result.URL),
		Platform:   platform.Name,
		Brief:      brief,
	}
}

func MatchPlatform(rawURL string, platforms []Platform) (Platform, bool) {
	for _, platform := range platforms {
		if platform.IsProductURL(rawURL) {
			return platform, true
		}
	}
	return Platform{}, false
}

func normalizeKeyword(keyword string) string {
	text := strings.TrimSpace(keyword)
	re := regexp.MustCompile(`(百万医疗|医疗保险|医疗险|重疾险|意外险|寿险|年金险|防癌险|健康险)`)
	match := re.FindString(text)
	switch match {
	case "医疗保险", "医疗险", "健康险":
		return "百万医疗险"
	case "":
		return "保险"
	default:
		return match
	}
}

func cleanProductName(title string) string {
	name := strings.TrimSpace(title)
	name = regexp.MustCompile(`^【(.+?)】`).ReplaceAllString(name, "$1")
	suffixes := []*regexp.Regexp{
		regexp.MustCompile(`[_\-|]+.*?小雨伞.*$`),
		regexp.MustCompile(`[_\-|]+.*?慧择.*$`),
		regexp.MustCompile(`[_\-|]+.*?深蓝保.*$`),
		regexp.MustCompile(`[_\-|]+.*?众安.*$`),
		regexp.MustCompile(`[_\-|]+.*?沃保.*$`),
		regexp.MustCompile(`[_\-|]+.*?保险网.*$`),
		regexp.MustCompile(`[_\-|]+.*?保险经纪.*$`),
	}
	for _, suffix := range suffixes {
		name = suffix.ReplaceAllString(name, "")
	}
	return strings.TrimSpace(name)
}

func extractCompany(text string) string {
	if m := regexp.MustCompile(`[\p{Han}]{2,6}(?:人寿|财险|保险|财产|健康)`).FindString(text); m != "" {
		return m
	}
	return ""
}

func productID(rawURL string) string {
	sum := md5.Sum([]byte(rawURL))
	return "prod_" + hex.EncodeToString(sum[:])[:8]
}

func normalizeProductURL(rawURL string) string {
	return strings.TrimRight(web.NormalizeURL(rawURL), "/")
}

func mergeTags(primary, secondary []string, max int) []string {
	out := append([]string(nil), primary...)
	for _, tag := range secondary {
		tag = strings.TrimSpace(tag)
		if tag == "" || contains(out, tag) {
			continue
		}
		out = append(out, tag)
		if len(out) >= max {
			break
		}
	}
	return out
}

func truncate(text string, max int) string {
	runes := []rune(strings.TrimSpace(text))
	if max <= 0 || len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max])
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
