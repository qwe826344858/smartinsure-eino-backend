package search

import (
	"context"
	"errors"
	"strings"

	"smartinsure-eino-backend/internal/platform"
	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/search/fallback"
	"smartinsure-eino-backend/internal/service/productsearch"
	toolcontract "smartinsure-eino-backend/internal/tool"
)

var productSearchIntents = map[string]struct{}{
	schema.IntentProductRecommendation: {},
	schema.IntentProductQuery:          {},
	schema.IntentComparison:            {},
}

type InProcessSearchTool struct {
	product        ProductSearcher
	knowledge      KnowledgeSearcher
	runtime        toolcontract.ToolRuntimeConfig
	productOptions productsearch.Options
}

type Option func(*InProcessSearchTool)

func NewInProcessSearchTool(opts ...Option) *InProcessSearchTool {
	t := &InProcessSearchTool{
		product: productsearch.New(),
		knowledge: fallbackSearcher{
			service: fallback.NewService(nil),
		},
		runtime: toolcontract.ToolRuntimeConfig{
			Backend: toolcontract.BackendInProcess,
		}.WithDefaults(),
		productOptions: productsearch.Options{MaxPerPlatform: 10, MaxTotal: 10},
	}
	for _, opt := range opts {
		opt(t)
	}
	t.runtime = t.runtime.WithDefaults()
	return t
}

func WithProductSearcher(searcher ProductSearcher) Option {
	return func(t *InProcessSearchTool) {
		t.product = searcher
	}
}

func WithKnowledgeSearcher(searcher KnowledgeSearcher) Option {
	return func(t *InProcessSearchTool) {
		t.knowledge = searcher
	}
}

func WithRuntimeConfig(config toolcontract.ToolRuntimeConfig) Option {
	return func(t *InProcessSearchTool) {
		t.runtime = config
	}
}

func WithProductOptions(opts productsearch.Options) Option {
	return func(t *InProcessSearchTool) {
		t.productOptions = opts
	}
}

func (t *InProcessSearchTool) Name() string {
	return ToolName
}

func (t *InProcessSearchTool) Invoke(ctx context.Context, input SearchToolInput) (SearchToolOutput, error) {
	var output SearchToolOutput
	message := strings.TrimSpace(input.Message)
	if message == "" {
		return output, &toolcontract.ToolError{
			ToolName: ToolName,
			Code:     toolcontract.ErrorCodeInvalidInput,
			Message:  "message is required",
		}
	}
	if t == nil {
		t = NewInProcessSearchTool()
	}
	ctx, cancel := withRuntimeTimeout(ctx, t.runtime)
	defer cancel()

	productsCh := make(chan productResult, 1)
	knowledgeCh := make(chan knowledgeResult, 1)

	if shouldSearchProducts(input.Intent) && t.product != nil {
		go func() {
			products, err := invokeWithRetries(ctx, t.runtime.MaxRetries, func() ([]platform.ProductCard, error) {
				return t.product.Search(ctx, message, t.productOptions)
			})
			productsCh <- productResult{products: products, err: err}
		}()
	} else {
		productsCh <- productResult{skipped: true}
	}

	if t.knowledge != nil {
		go func() {
			results, err := invokeWithRetries(ctx, t.runtime.MaxRetries, func() ([]schema.SearchResultItem, error) {
				return t.knowledge.Search(ctx, message)
			})
			knowledgeCh <- knowledgeResult{results: results, err: err}
		}()
	} else {
		knowledgeCh <- knowledgeResult{err: errors.New("knowledge searcher is nil")}
	}

	products := <-productsCh
	knowledge := <-knowledgeCh

	if products.err != nil {
		output.Degraded = true
		output.Failures = append(output.Failures, toolcontract.FailureFromError(ProductSearchToolName, products.err, true))
	} else if !products.skipped {
		output.Products = toSchemaProducts(products.products)
	}

	if knowledge.err != nil {
		output.Degraded = true
		output.Failures = append(output.Failures, toolcontract.FailureFromError(KnowledgeSearchName, knowledge.err, false))
	} else {
		output.Results = append([]schema.SearchResultItem(nil), knowledge.results...)
		output.Sources = uniqueSources(output.Results)
	}

	return output, nil
}

type productResult struct {
	products []platform.ProductCard
	err      error
	skipped  bool
}

type knowledgeResult struct {
	results []schema.SearchResultItem
	err     error
}

type fallbackSearcher struct {
	service *fallback.Service
}

func (s fallbackSearcher) Search(_ context.Context, query string) ([]schema.SearchResultItem, error) {
	service := s.service
	if service == nil {
		service = fallback.NewService(nil)
	}
	return service.Search(query), nil
}

func shouldSearchProducts(intent string) bool {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return true
	}
	_, ok := productSearchIntents[intent]
	return ok
}

func withRuntimeTimeout(ctx context.Context, config toolcontract.ToolRuntimeConfig) (context.Context, context.CancelFunc) {
	if config.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, config.Timeout)
}

func invokeWithRetries[T any](ctx context.Context, maxRetries int, fn func() (T, error)) (T, error) {
	var zero T
	if maxRetries < 0 {
		maxRetries = 0
	}
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		value, err := fn()
		if err == nil {
			return value, nil
		}
		if attempt == maxRetries {
			return zero, err
		}
	}
	return zero, ctx.Err()
}

func toSchemaProducts(products []platform.ProductCard) []schema.ProductCard {
	out := make([]schema.ProductCard, 0, len(products))
	for _, product := range products {
		price := ""
		if product.Price != nil {
			price = *product.Price
		}
		out = append(out, schema.ProductCard{
			ID:         product.ID,
			Name:       product.Name,
			Company:    product.Company,
			Price:      price,
			PriceLabel: product.PriceLabel,
			Tags:       append([]string(nil), product.Tags...),
			URL:        product.URL,
			Platform:   product.Platform,
			Brief:      product.Brief,
		})
	}
	return out
}

func uniqueSources(results []schema.SearchResultItem) []schema.SourceItem {
	seen := make(map[string]struct{}, len(results))
	sources := make([]schema.SourceItem, 0, len(results))
	for _, result := range results {
		productURL := firstNonEmpty(result.ProductURL, result.URL)
		normalized := fallback.NormalizeURL(productURL)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		sources = append(sources, schema.SourceItem{
			Title:      result.Title,
			URL:        result.URL,
			Site:       result.Site,
			ProductURL: productURL,
		})
	}
	return sources
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
