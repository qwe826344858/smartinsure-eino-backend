package chatflow

import (
	"context"

	"smartinsure-eino-backend/internal/platform"
	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/service/productsearch"
	toolsearch "smartinsure-eino-backend/internal/tool/search"
)

type SearchTool interface {
	Invoke(ctx context.Context, input toolsearch.SearchToolInput) (toolsearch.SearchToolOutput, error)
}

func newInProcessSearchTool(flow *Flow) SearchTool {
	opts := []toolsearch.Option{}
	if flow != nil && flow.Search != nil {
		opts = append(opts, toolsearch.WithProductSearcher(flowProductSearcher{searcher: flow.Search}))
	}
	if flow != nil && flow.Fallback != nil {
		opts = append(opts, toolsearch.WithKnowledgeSearcher(flowKnowledgeSearcher{searcher: flow.Fallback}))
	}
	return toolsearch.NewInProcessSearchTool(opts...)
}

type flowProductSearcher struct {
	searcher ProductSearcher
}

func (s flowProductSearcher) Search(ctx context.Context, message string, _ productsearch.Options) ([]platform.ProductCard, error) {
	if s.searcher == nil {
		return nil, nil
	}
	products, err := s.searcher.Search(ctx, message)
	if err != nil {
		return nil, err
	}
	out := make([]platform.ProductCard, 0, len(products))
	for _, product := range products {
		out = append(out, platform.ProductCard{
			ID:         product.ID,
			Name:       product.Name,
			Company:    product.Company,
			Price:      product.Price,
			PriceLabel: product.PriceLabel,
			Tags:       append([]string(nil), product.Tags...),
			URL:        product.URL,
			Platform:   product.Platform,
			Brief:      product.Brief,
		})
	}
	return out, nil
}

type flowKnowledgeSearcher struct {
	searcher FallbackSearcher
}

func (s flowKnowledgeSearcher) Search(ctx context.Context, query string) ([]schema.SearchResultItem, error) {
	if s.searcher == nil {
		return nil, nil
	}
	results, err := s.searcher.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]schema.SearchResultItem, 0, len(results))
	for _, result := range results {
		out = append(out, schema.SearchResultItem{
			Title:   result.Title,
			URL:     result.URL,
			Site:    result.Site,
			Snippet: result.Snippet,
		})
	}
	return out, nil
}

func fromSchemaProductCards(products []schema.ProductCard) []ProductCard {
	out := make([]ProductCard, 0, len(products))
	for _, product := range products {
		var price *string
		if product.Price != "" {
			value := product.Price
			price = &value
		}
		out = append(out, ProductCard{
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

func fromSchemaSources(sources []schema.SourceItem) []SourceItem {
	out := make([]SourceItem, 0, len(sources))
	for _, source := range sources {
		out = append(out, SourceItem{Title: source.Title, URL: source.URL, Site: source.Site})
	}
	return out
}
