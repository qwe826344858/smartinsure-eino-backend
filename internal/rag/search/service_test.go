package search

import (
	"context"
	"errors"
	"testing"

	ragstore "smartinsure-eino-backend/internal/rag/store"
)

type fakeEmbedder struct {
	texts []string
	err   error
}

func (e *fakeEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float64, error) {
	e.texts = append(e.texts, texts...)
	if e.err != nil {
		return nil, e.err
	}
	return [][]float64{{0.1, 0.2}}, nil
}

type fakeSearchStore struct {
	query   ragstore.SearchQuery
	results []ragstore.SearchResult
	err     error
}

func (s *fakeSearchStore) SearchSimilarChunks(_ context.Context, query ragstore.SearchQuery) ([]ragstore.SearchResult, error) {
	s.query = query
	if s.err != nil {
		return nil, s.err
	}
	return s.results, nil
}

func TestSearchWithOptionsEmbedsAndMapsResults(t *testing.T) {
	embedder := &fakeEmbedder{}
	store := &fakeSearchStore{results: []ragstore.SearchResult{{
		Content:    "产品名称：测试医疗险\n保障责任：外购药品费用医疗保险金",
		Score:      0.91,
		SourceURL:  "productdetail://huize:1",
		Title:      "测试医疗险",
		SourceType: "product_detail",
		ChunkIndex: 2,
		Metadata: map[string]any{
			"product_name":  "测试医疗险",
			"platform":      "huize",
			"canonical_url": "https://example.com/product",
			"chunk_type":    "product_duty",
			"duty_name":     "外购药品费用医疗保险金",
			"tags":          []any{"医疗险", "外购药"},
		},
	}}}
	service, err := NewService(embedder, store, Config{Namespace: "product_details", TopK: 3, MinScore: 0.5})
	if err != nil {
		t.Fatalf("NewService error = %v", err)
	}

	items, err := service.SearchWithOptions(context.Background(), "外购药能报吗", Options{
		TopK: 2,
		Filters: map[string]any{
			"chunk_type": "product_duty",
		},
	})
	if err != nil {
		t.Fatalf("SearchWithOptions error = %v", err)
	}
	if len(embedder.texts) != 1 || embedder.texts[0] != "外购药能报吗" {
		t.Fatalf("unexpected embedder texts: %#v", embedder.texts)
	}
	if store.query.Namespace != "product_details" || store.query.TopK != 2 {
		t.Fatalf("unexpected search query: %#v", store.query)
	}
	if store.query.Filters["chunk_type"] != "product_duty" {
		t.Fatalf("filters not propagated: %#v", store.query.Filters)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Title != "测试医疗险 - 外购药品费用医疗保险金" ||
		items[0].URL != "https://example.com/product" ||
		items[0].ProductURL != "https://example.com/product" ||
		items[0].ProductName != "测试医疗险" ||
		items[0].Site != "huize" ||
		len(items[0].Tags) != 2 {
		t.Fatalf("unexpected mapped item: %#v", items[0])
	}
}

func TestSearchFiltersMinScore(t *testing.T) {
	store := &fakeSearchStore{results: []ragstore.SearchResult{
		{Content: "低分", Score: 0.2, Title: "低分"},
		{Content: "高分", Score: 0.8, Title: "高分"},
	}}
	service, _ := NewService(&fakeEmbedder{}, store, Config{Namespace: "product_details", TopK: 5, MinScore: 0.6})
	items, err := service.Search(context.Background(), "医疗险")
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(items) != 1 || items[0].Title != "高分" {
		t.Fatalf("unexpected min score filtered items: %#v", items)
	}
}

func TestSearchReturnsEmptyForBlankQuery(t *testing.T) {
	service, _ := NewService(&fakeEmbedder{}, &fakeSearchStore{}, Config{Namespace: "product_details"})
	items, err := service.Search(context.Background(), "  ")
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len(items) = %d, want 0", len(items))
	}
}

func TestSearchPropagatesStoreError(t *testing.T) {
	service, _ := NewService(&fakeEmbedder{}, &fakeSearchStore{err: errors.New("pg down")}, Config{Namespace: "product_details"})
	_, err := service.Search(context.Background(), "医疗险")
	if err == nil || err.Error() != "pg down" {
		t.Fatalf("err = %v, want pg down", err)
	}
}
