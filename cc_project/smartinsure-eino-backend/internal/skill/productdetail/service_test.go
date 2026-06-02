package productdetail

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/schema"
)

type fakeFetcher struct {
	html  string
	err   error
	calls int
}

func (f *fakeFetcher) Fetch(context.Context, string) (string, error) {
	f.calls++
	return f.html, f.err
}

type fakeModel struct {
	text          string
	textErr       error
	stream        []string
	streamErr     error
	callTextCalls int
}

func (f *fakeModel) CallText(context.Context, []llm.Message, float64) (string, error) {
	f.callTextCalls++
	return f.text, f.textErr
}

func (f *fakeModel) CallJSON(context.Context, []llm.Message, float64, any) error {
	return nil
}

func (f *fakeModel) StreamText(context.Context, []llm.Message, float64) (<-chan llm.StreamChunk, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	ch := make(chan llm.StreamChunk, len(f.stream))
	for _, text := range f.stream {
		ch <- llm.StreamChunk{Text: text}
	}
	close(ch)
	return ch, nil
}

func TestValidateExtraction(t *testing.T) {
	duties := []schema.DutyItem{
		{Name: "一般医疗保险金", Coverage: "300万"},
		{Name: "重大疾病保险金", Coverage: "300万"},
		{Name: "不存在的保障", Coverage: "100万"},
	}
	text := "本产品包含一般医疗保险金和重大疾病保险金。"

	passed, rate, reason := ValidateExtraction(duties, text)
	if passed {
		t.Fatalf("passed = true, want false below threshold; %s", reason)
	}
	if rate >= 0.7 {
		t.Fatalf("rate = %f, want below 0.7", rate)
	}

	passed, rate, _ = ValidateExtraction(duties[:2], text)
	if !passed || rate != 1 {
		t.Fatalf("ValidateExtraction = (%v, %f), want true and 1", passed, rate)
	}
}

func TestParseExtractionJSONSupportsMarkdownWrapper(t *testing.T) {
	raw := "```json\n{\"product_name\":\"测试产品\",\"duties\":[{\"name\":\"一般医疗\",\"coverage\":\"300万\",\"description\":\"说明\",\"is_optional\":false},{\"name\":\"\",\"coverage\":\"100万\"}]}\n```"

	payload, ok := ParseExtractionJSON(raw)
	if !ok {
		t.Fatal("expected parse success")
	}
	if payload.ProductName != "测试产品" {
		t.Fatalf("ProductName = %q", payload.ProductName)
	}
	if len(payload.Duties) != 1 || payload.Duties[0].Name != "一般医疗" {
		t.Fatalf("duties = %#v", payload.Duties)
	}
}

func TestServiceFetchesCleansFallbackExtractsAndCaches(t *testing.T) {
	html := `<html><body>
<h1>测试百万医疗险</h1>
<p>一般医疗保险金 300万 保障住院医疗费用和特殊门诊费用。</p>
<p>重大疾病医疗保险金 300万 保障重大疾病治疗费用。</p>
<p>可选外购药保险金 100万 可附加报销院外特定药品费用。</p>
</body></html>`
	fetcher := &fakeFetcher{html: html}
	cache := NewCache(time.Hour)
	svc := NewService(WithFetcher(fetcher), WithCache(cache))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:      "product_detail",
		ProductURL:  "https://example.com/product/1",
		ProductName: "测试百万医疗险",
	}))

	if fetcher.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetcher.calls)
	}
	payload, ok := findDetailItems(events)
	if !ok {
		t.Fatalf("missing detail_items event: %#v", events)
	}
	if payload.ProductName != "测试百万医疗险" {
		t.Fatalf("ProductName = %q", payload.ProductName)
	}
	if len(payload.Duties) < 3 {
		t.Fatalf("duties = %#v, want at least 3", payload.Duties)
	}
	if _, ok := cache.Get("https://example.com/product/1"); !ok {
		t.Fatal("expected cached product detail")
	}
	if !strings.Contains(deltaText(events), "主要保障") {
		t.Fatalf("fallback answer missing summary: %s", deltaText(events))
	}
}

func TestServiceUsesLLMExtractionWhenValidated(t *testing.T) {
	html := `<p>LLM百万医疗险</p><p>一般医疗保险金 300万 保障住院医疗费用、特殊门诊费用和住院前后门急诊费用。</p><p>重大疾病保险金 300万 保障重疾治疗费用、重大疾病住院医疗费用和相关检查费用。</p>`
	model := &fakeModel{
		text:   `{"product_name":"LLM百万医疗险","duties":[{"name":"一般医疗保险金","coverage":"300万","description":"住院医疗费用","is_optional":false},{"name":"重大疾病保险金","coverage":"300万","description":"重疾治疗费用","is_optional":false}]}`,
		stream: []string{"这是LLM解读"},
	}
	svc := NewService(WithFetcher(&fakeFetcher{html: html}), WithCache(NewCache(time.Hour)), WithModel(model))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: "https://example.com/product/llm",
	}))

	if model.callTextCalls == 0 {
		t.Fatal("expected LLM extraction call")
	}
	payload, ok := findDetailItems(events)
	if !ok {
		t.Fatal("missing detail_items event")
	}
	if payload.ProductName != "LLM百万医疗险" {
		t.Fatalf("ProductName = %q", payload.ProductName)
	}
	if !strings.Contains(deltaText(events), "这是LLM解读") {
		t.Fatalf("delta text = %s", deltaText(events))
	}
}

func TestServiceCacheHitSkipsFetch(t *testing.T) {
	cache := NewCache(time.Hour)
	url := "https://example.com/product/cached"
	cache.Set(url, schema.ProductDetail{
		ProductName: "缓存医疗险",
		ProductURL:  url,
		Duties: []schema.DutyItem{{
			Name:        "一般医疗保险金",
			Coverage:    "300万",
			Description: "保障住院医疗费用",
		}},
	})
	fetcher := &fakeFetcher{err: errors.New("should not fetch")}
	svc := NewService(WithFetcher(fetcher), WithCache(cache))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: url,
	}))

	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", fetcher.calls)
	}
	if _, ok := findDetailItems(events); ok {
		t.Fatal("cache hit should not emit detail_items")
	}
	if !strings.Contains(deltaText(events), "一般医疗保险金") {
		t.Fatalf("delta text = %s", deltaText(events))
	}
}

func TestServiceFollowupUsesCacheAndMissingCacheIsClear(t *testing.T) {
	cache := NewCache(time.Hour)
	url := "https://example.com/product/followup"
	cache.Set(url, schema.ProductDetail{
		ProductName: "追问医疗险",
		ProductURL:  url,
		Duties: []schema.DutyItem{{
			Name:        "外购药保险金",
			Coverage:    "100万",
			Description: "报销院外特定药品费用",
		}},
	})
	fetcher := &fakeFetcher{err: errors.New("should not fetch")}
	svc := NewService(WithFetcher(fetcher), WithCache(cache))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_followup",
		ProductURL: url,
		Message:    "外购药能报吗？",
	}))

	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", fetcher.calls)
	}
	if !strings.Contains(deltaText(events), "外购药") {
		t.Fatalf("delta text = %s", deltaText(events))
	}

	miss := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_followup",
		ProductURL: "https://example.com/product/miss",
		Message:    "能报吗？",
	}))
	if !strings.Contains(deltaText(miss), "暂未找到该产品的详情缓存") {
		t.Fatalf("missing-cache delta = %s", deltaText(miss))
	}
}

func collectEvents(ch <-chan Event) []Event {
	var events []Event
	for event := range ch {
		events = append(events, event)
	}
	return events
}

func findDetailItems(events []Event) (schema.SSEDetailItemsPayload, bool) {
	for _, event := range events {
		if event.Name != schema.SSEEventDetailItems {
			continue
		}
		payload, ok := event.Data.(schema.SSEDetailItemsPayload)
		return payload, ok
	}
	return schema.SSEDetailItemsPayload{}, false
}

func deltaText(events []Event) string {
	var b strings.Builder
	for _, event := range events {
		if event.Name != schema.SSEEventDelta {
			continue
		}
		switch data := event.Data.(type) {
		case schema.SSEDeltaPayload:
			b.WriteString(data.Text)
		case map[string]string:
			b.WriteString(data["text"])
		}
	}
	return b.String()
}
