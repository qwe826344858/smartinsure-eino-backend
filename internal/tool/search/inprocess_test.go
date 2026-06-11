package search

import (
	"context"
	"errors"
	"testing"

	"smartinsure-eino-backend/internal/platform"
	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/service/productsearch"
	toolcontract "smartinsure-eino-backend/internal/tool"
)

func TestInProcessSearchToolProductFailureFallsBackToKnowledge(t *testing.T) {
	tool := NewInProcessSearchTool(
		WithProductSearcher(fakeProductSearcher{err: errors.New("product backend down")}),
		WithKnowledgeSearcher(fakeKnowledgeSearcher{results: []schema.SearchResultItem{{
			Title:   "百万医疗险怎么选",
			URL:     "https://example.test/medical",
			Site:    "example.test",
			Snippet: "关注免赔额、续保和报销范围。",
		}}}),
	)

	output, err := tool.Invoke(context.Background(), SearchToolInput{
		Message: "百万医疗险怎么选",
		Intent:  schema.IntentProductRecommendation,
	})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if len(output.Products) != 0 {
		t.Fatalf("Products len=%d, want 0", len(output.Products))
	}
	if len(output.Results) != 1 || len(output.Sources) != 1 {
		t.Fatalf("knowledge fallback missing: results=%#v sources=%#v", output.Results, output.Sources)
	}
	if !hasFailure(output.Failures, ProductSearchToolName) || !output.Degraded {
		t.Fatalf("product failure not recorded: degraded=%v failures=%#v", output.Degraded, output.Failures)
	}
}

func TestInProcessSearchToolKnowledgeFailureKeepsProducts(t *testing.T) {
	price := "588元/年起"
	tool := NewInProcessSearchTool(
		WithProductSearcher(fakeProductSearcher{products: []platform.ProductCard{{
			ID:         "p1",
			Name:       "成人百万医疗",
			Company:    "测试保险",
			Price:      &price,
			PriceLabel: price,
			Tags:       []string{"医疗险"},
			URL:        "https://example.test/p1",
			Platform:   "test",
			Brief:      "覆盖住院医疗费用。",
		}}}),
		WithKnowledgeSearcher(fakeKnowledgeSearcher{err: errors.New("knowledge backend down")}),
	)

	output, err := tool.Invoke(context.Background(), SearchToolInput{
		Message: "32岁想买医疗险",
		Intent:  schema.IntentProductQuery,
	})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if len(output.Products) != 1 {
		t.Fatalf("Products len=%d, want 1", len(output.Products))
	}
	if output.Products[0].Price != price {
		t.Fatalf("Price=%q, want %q", output.Products[0].Price, price)
	}
	if len(output.Results) != 0 || len(output.Sources) != 0 {
		t.Fatalf("knowledge output should be empty on failure: results=%#v sources=%#v", output.Results, output.Sources)
	}
	if !hasFailure(output.Failures, KnowledgeSearchName) || !output.Degraded {
		t.Fatalf("knowledge failure not recorded: degraded=%v failures=%#v", output.Degraded, output.Failures)
	}
}

func TestInProcessSearchToolDoubleFailureDegradesToEmptyOutput(t *testing.T) {
	tool := NewInProcessSearchTool(
		WithProductSearcher(fakeProductSearcher{err: errors.New("product backend down")}),
		WithKnowledgeSearcher(fakeKnowledgeSearcher{err: errors.New("knowledge backend down")}),
	)

	output, err := tool.Invoke(context.Background(), SearchToolInput{
		Message: "帮我推荐医疗险",
		Intent:  schema.IntentProductRecommendation,
	})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if len(output.Products) != 0 || len(output.Results) != 0 || len(output.Sources) != 0 {
		t.Fatalf("output should be empty on double failure: %#v", output)
	}
	if !output.Degraded || len(output.Failures) != 2 {
		t.Fatalf("double failure should be recorded: degraded=%v failures=%#v", output.Degraded, output.Failures)
	}
	if !hasFailure(output.Failures, ProductSearchToolName) || !hasFailure(output.Failures, KnowledgeSearchName) {
		t.Fatalf("expected both backend failures: %#v", output.Failures)
	}
}

type fakeProductSearcher struct {
	products []platform.ProductCard
	err      error
}

func (f fakeProductSearcher) Search(context.Context, string, productsearch.Options) ([]platform.ProductCard, error) {
	return append([]platform.ProductCard(nil), f.products...), f.err
}

type fakeKnowledgeSearcher struct {
	results []schema.SearchResultItem
	err     error
}

func (f fakeKnowledgeSearcher) Search(context.Context, string) ([]schema.SearchResultItem, error) {
	return append([]schema.SearchResultItem(nil), f.results...), f.err
}

func hasFailure(failures []toolcontract.ToolFailure, toolName string) bool {
	for _, failure := range failures {
		if failure.ToolName == toolName {
			return true
		}
	}
	return false
}
