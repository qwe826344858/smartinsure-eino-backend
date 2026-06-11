package search

import (
	"context"

	"smartinsure-eino-backend/internal/platform"
	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/service/productsearch"
	toolcontract "smartinsure-eino-backend/internal/tool"
)

const (
	ToolName              = "search_tool"
	ProductSearchToolName = "product_search"
	KnowledgeSearchName   = "knowledge_search"
)

type SearchToolInput struct {
	Message string                     `json:"message"`
	Intent  string                     `json:"intent,omitempty"`
	History []toolcontract.ChatMessage `json:"history,omitempty"`
}

type SearchToolOutput struct {
	Products []schema.ProductCard       `json:"products"`
	Results  []schema.SearchResultItem  `json:"results"`
	Sources  []schema.SourceItem        `json:"sources"`
	Degraded bool                       `json:"degraded,omitempty"`
	Failures []toolcontract.ToolFailure `json:"failures,omitempty"`
}

type ProductSearcher interface {
	Search(ctx context.Context, message string, opts productsearch.Options) ([]platform.ProductCard, error)
}

type KnowledgeSearcher interface {
	Search(ctx context.Context, query string) ([]schema.SearchResultItem, error)
}

var _ toolcontract.ToolBackend[SearchToolInput, SearchToolOutput] = (*InProcessSearchTool)(nil)
