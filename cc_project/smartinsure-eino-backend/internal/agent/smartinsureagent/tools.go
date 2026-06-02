package smartinsureagent

import (
	"context"
	"fmt"
	"strings"

	"smartinsure-eino-backend/internal/agent/chatflow"
)

type AgentTools struct {
	search   chatflow.ProductSearcher
	fallback chatflow.FallbackSearcher
	detail   chatflow.DetailRunner
}

// ProductSearchObservation 是产品搜索工具的裁剪结果。
// Products 用于前端展示，Summary/Data 用于写入 AgentState.Steps，
// 避免把完整产品 JSON 全量塞回 planner prompt。
type ProductSearchObservation struct {
	Products []chatflow.ProductCard
	Summary  string
}

// KnowledgeSearchObservation 是知识检索工具的裁剪结果。
// Results 交给最终回答模型使用，Sources 交给 SSE 展示，
// Summary/Data 交给下一轮 reasoner 判断是否需要继续检索。
type KnowledgeSearchObservation struct {
	Results []chatflow.SearchResultItem
	Sources []chatflow.SourceItem
	Summary string
}

func (t AgentTools) ProductSearch(ctx context.Context, query string) (ProductSearchObservation, error) {
	if t.search == nil {
		return ProductSearchObservation{Summary: "产品搜索工具未配置。"}, nil
	}
	products, err := t.search.Search(ctx, query)
	if err != nil {
		return ProductSearchObservation{}, err
	}
	return ProductSearchObservation{
		Products: products,
		Summary:  fmt.Sprintf("产品搜索返回 %d 个候选产品。", len(products)),
	}, nil
}

func (t AgentTools) KnowledgeSearch(ctx context.Context, query string) (KnowledgeSearchObservation, error) {
	if t.fallback == nil {
		return KnowledgeSearchObservation{Summary: "知识检索工具未配置。"}, nil
	}
	results, err := t.fallback.Search(ctx, query)
	if err != nil {
		return KnowledgeSearchObservation{}, err
	}
	return KnowledgeSearchObservation{
		Results: results,
		Sources: uniqueSources(results),
		Summary: fmt.Sprintf("知识检索返回 %d 条来源。", len(results)),
	}, nil
}

func observationFromProductSearch(obs ProductSearchObservation) AgentObservation {
	return AgentObservation{
		Summary: obs.Summary,
		Data: map[string]any{
			"product_count": len(obs.Products),
			"products":      compactProductNames(obs.Products),
		},
	}
}

func observationFromKnowledgeSearch(obs KnowledgeSearchObservation) AgentObservation {
	return AgentObservation{
		Summary: obs.Summary,
		Data: map[string]any{
			"source_count": len(obs.Sources),
			"sources":      compactSourceTitles(obs.Sources),
		},
	}
}

func compactProductNames(products []chatflow.ProductCard) []string {
	out := make([]string, 0, len(products))
	for _, product := range products {
		name := strings.TrimSpace(product.Name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func compactSourceTitles(sources []chatflow.SourceItem) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		title := strings.TrimSpace(source.Title)
		if title != "" {
			out = append(out, title)
		}
	}
	return out
}
