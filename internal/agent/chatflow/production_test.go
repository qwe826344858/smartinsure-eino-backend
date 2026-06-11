package chatflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/search/fallback"
	skillproduct "smartinsure-eino-backend/internal/skill/productdetail"
)

type fakeSchemaSearcher struct {
	results []schema.SearchResultItem
	err     error
	calls   int
}

func (s *fakeSchemaSearcher) Search(context.Context, string) ([]schema.SearchResultItem, error) {
	s.calls++
	return s.results, s.err
}

func TestRAGFallbackAdapterUsesPrimaryResults(t *testing.T) {
	primary := &fakeSchemaSearcher{results: []schema.SearchResultItem{{
		Title:   "RAG 产品责任",
		URL:     "https://example.com/product",
		Site:    "huize",
		Snippet: "外购药责任",
	}}}
	adapter := ragFallbackAdapter{
		primary:  primary,
		fallback: fallback.NewService([]fallback.KnowledgeItem{fallbackItem()}),
	}

	results, err := adapter.Search(context.Background(), "外购药")
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(results) != 1 || results[0].Title != "RAG 产品责任" {
		t.Fatalf("unexpected results: %#v", results)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}
}

func TestRAGFallbackAdapterFallsBackOnEmptyOrError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		primary *fakeSchemaSearcher
	}{
		{name: "empty", primary: &fakeSchemaSearcher{}},
		{name: "error", primary: &fakeSchemaSearcher{err: errors.New("rag down")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := ragFallbackAdapter{
				primary:  tc.primary,
				fallback: fallback.NewService([]fallback.KnowledgeItem{fallbackItem()}),
			}
			results, err := adapter.Search(context.Background(), "fallback-keyword")
			if err != nil {
				t.Fatalf("Search error = %v", err)
			}
			if len(results) != 1 || results[0].Title != "fallback title" {
				t.Fatalf("unexpected fallback results: %#v", results)
			}
		})
	}
}

func TestRAGFallbackAdapterRespectsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	adapter := ragFallbackAdapter{
		primary:  &fakeSchemaSearcher{results: []schema.SearchResultItem{{Title: "RAG"}}},
		fallback: fallback.NewService([]fallback.KnowledgeItem{fallbackItem()}),
	}
	if _, err := adapter.Search(ctx, "fallback-keyword"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestProductDetailRAGInputFromStoredMapsSource(t *testing.T) {
	expiresAt := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	fetchedAt := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	record := skillproduct.StoredProductDetailWithSource{
		Detail: skillproduct.StoredProductDetail{
			ProductKey:    "huize:104006:108504",
			Platform:      "huize",
			ProductName:   "测试医疗险",
			CanonicalURL:  "https://example.com/product",
			URLHash:       "url-hash",
			SourceHash:    "source-hash",
			PromptVersion: "detail-v1",
			ModelName:     "extract-model",
			Status:        skillproduct.DetailStatusActive,
			ExpiresAt:     &expiresAt,
			Detail: schema.ProductDetail{
				ProductName: "测试医疗险",
				ProductURL:  "https://example.com/product",
				Platform:    "huize",
				Duties: []schema.DutyItem{{
					Name:        "一般住院医疗保险金",
					Coverage:    "300万",
					Description: "住院医疗费用",
				}},
				CNCharCount: 1000,
				MatchRate:   0.9,
			},
		},
		Source: &skillproduct.ProductDetailSource{
			SourceURL:         "https://example.com/product",
			SourceType:        "web_page",
			SourceFormat:      "html",
			CleanedText:       "产品原文",
			ContentHash:       "source-hash",
			CNCharCount:       1000,
			FetchedAt:         fetchedAt,
			NormalizedURLHash: "source-url-hash",
		},
	}

	input := productDetailRAGInputFromStored(record, "product_details")
	if input.ProductKey != "huize:104006:108504" || input.NormalizedURLHash != "url-hash" {
		t.Fatalf("unexpected input identity: %#v", input)
	}
	if len(input.Sources) != 1 {
		t.Fatalf("len(Sources) = %d, want 1", len(input.Sources))
	}
	if input.Sources[0].OriginSourceType != "web_page" || input.Sources[0].ContentHash != "source-hash" {
		t.Fatalf("unexpected source: %#v", input.Sources[0])
	}
}

func TestProductionRAGDepsKeyIncludesRuntimeControls(t *testing.T) {
	base := config.Settings{
		DatabaseURL:                  "postgres://example",
		EmbeddingProvider:            "ark",
		EmbeddingModel:               "ep-test",
		EmbeddingAPIBase:             "https://ark.cn-beijing.volces.com/api/v3",
		RAGSearchEnabled:             true,
		RAGSearchNamespace:           "product_details",
		RAGSearchTopK:                5,
		ProductDetailRAGEnabled:      true,
		ProductDetailRAGNamespace:    "product_details",
		ProductDetailRAGMinMatchRate: 0.6,
		ProductDetailRAGAsyncTimeout: 30,
		ProductDetailRAGAsyncWorkers: 2,
		ProductDetailRAGQueueSize:    100,
		EmbeddingTimeout:             30,
		EmbeddingBatchSize:           16,
		EmbeddingDimensions:          1024,
	}
	changed := base
	changed.ProductDetailRAGAsyncWorkers = 4
	if productionRAGDepsKey(base, true) == productionRAGDepsKey(changed, true) {
		t.Fatal("productionRAGDepsKey should change when async worker config changes")
	}
	changed = base
	changed.ProductDetailRAGMinMatchRate = 0.75
	if productionRAGDepsKey(base, true) == productionRAGDepsKey(changed, true) {
		t.Fatal("productionRAGDepsKey should change when min match rate changes")
	}
	if productionRAGDepsKey(base, false) == productionRAGDepsKey(base, true) {
		t.Fatal("productionRAGDepsKey should change when product detail rag status tracking changes")
	}
}

func fallbackItem() fallback.KnowledgeItem {
	return fallback.KnowledgeItem{
		Title:    "fallback title",
		URL:      "https://example.com/fallback",
		Site:     "fallback",
		Snippet:  "fallback snippet",
		Keywords: []string{"fallback-keyword"},
	}
}
