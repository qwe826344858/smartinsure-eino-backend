package chatflow

import (
	"context"
	"strings"
)

type defaultIntentClassifier struct{}

func (defaultIntentClassifier) Classify(_ context.Context, message string) (IntentResult, error) {
	text := strings.TrimSpace(message)
	lower := strings.ToLower(text)
	if text == "" {
		return IntentResult{Intent: "knowledge_explain"}, nil
	}
	if strings.Contains(lower, "天气") || strings.Contains(lower, "股票") || strings.Contains(lower, "写代码") {
		return IntentResult{Intent: "out_of_scope"}, nil
	}
	if strings.Contains(text, "推荐") || strings.Contains(text, "怎么买") || strings.Contains(text, "怎么选") {
		return IntentResult{Intent: "product_recommendation"}, nil
	}
	if strings.Contains(text, "对比") || strings.Contains(text, "区别") {
		return IntentResult{Intent: "comparison"}, nil
	}
	return IntentResult{Intent: "knowledge_explain"}, nil
}

func (c defaultIntentClassifier) ClassifyWithHistory(ctx context.Context, message string, _ []ChatMessage) (IntentResult, error) {
	return c.Classify(ctx, message)
}

type defaultFallbackSearcher struct{}

func (defaultFallbackSearcher) Search(_ context.Context, query string) ([]SearchResultItem, error) {
	return []SearchResultItem{
		{
			Title:   "保险基础知识",
			URL:     "smartinsure://fallback/insurance-basics",
			Site:    "SmartInsure",
			Snippet: "保险配置通常需要先明确保障对象、预算、既有保障和健康告知情况。",
		},
		{
			Title:   "产品选择提示",
			URL:     "smartinsure://fallback/product-selection",
			Site:    "SmartInsure",
			Snippet: "产品选择应重点关注保障责任、免责条款、等待期、续保条件和理赔限制。",
		},
	}, nil
}

type defaultAnswerStreamer struct{}

func (defaultAnswerStreamer) Stream(ctx context.Context, input AnswerInput) (<-chan string, <-chan error) {
	chunks := make(chan string)
	errs := make(chan error, 1)
	go func() {
		defer close(chunks)
		defer close(errs)

		text := defaultAnswer(input)
		for _, chunk := range splitChunks(text, 48) {
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case chunks <- chunk:
			}
		}
	}()
	return chunks, errs
}

type defaultFollowupGenerator struct{}

func (defaultFollowupGenerator) Generate(_ context.Context, missingSlots []string) (string, error) {
	return followupText(missingSlots), nil
}

func defaultAnswer(input AnswerInput) string {
	switch input.Intent {
	case "product_recommendation", "product_query":
		return "我会先按保障需求、预算和健康告知约束筛选产品。当前 Go P0 已接通 SSE 编排骨架，平台产品搜索服务接入后会同步返回产品卡片。"
	case "comparison":
		return "可以从保障范围、赔付条件、等待期、续保条件、免赔额和价格稳定性几个维度对比。具体产品接入后，我会结合产品卡片和条款来源给出差异点。"
	default:
		return "保险问题建议先明确保障对象、核心风险和预算，再看条款责任、免责项、等待期和续保条件。当前回答来自 Go P0 fallback 骨架，后续会接入 LLM 流式回答。"
	}
}

func splitChunks(text string, size int) []string {
	runes := []rune(text)
	if len(runes) <= size {
		return []string{text}
	}
	var chunks []string
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}
