package chatflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/skill/productdetail"
)

func TestProductDetailActionUsesProductDetailService(t *testing.T) {
	flow := New()
	flow.Detail = detailAdapter{service: productdetail.NewService(
		productdetail.WithFetcher(staticProductPageFetcher{}),
		productdetail.WithCache(productdetail.NewCache(time.Hour)),
	)}

	events := collect(flow.Run(context.Background(), Request{
		Action:      "product_detail",
		ProductURL:  "https://example.com/product/detail",
		ProductName: "测试百万医疗险",
		RequestID:   "rid-product-detail",
	}))

	if len(events) == 0 {
		t.Fatal("expected product detail events")
	}
	if events[len(events)-1].Name != EventDone {
		t.Fatalf("last event = %q, want %q", events[len(events)-1].Name, EventDone)
	}
	if hasEvent(events, EventError) {
		t.Fatalf("unexpected error event: %#v", events)
	}
	if !hasEvent(events, EventDetailItems) {
		t.Fatalf("missing detail_items event: %#v", events)
	}
	if !hasEvent(events, EventDelta) {
		t.Fatalf("missing delta event: %#v", events)
	}
}

type staticProductPageFetcher struct{}

func (staticProductPageFetcher) Fetch(context.Context, string) (string, error) {
	return `<html><body>
<h1>测试百万医疗险</h1>
<p>一般医疗保险金 300万 保障住院医疗费用和特殊门诊费用。</p>
<p>重大疾病医疗保险金 300万 保障重大疾病治疗费用。</p>
<p>可选外购药保险金 100万 可附加报销院外特定药品费用。</p>
</body></html>`, nil
}

func hasEvent(events []Event, name string) bool {
	for _, event := range events {
		if strings.EqualFold(event.Name, name) {
			return true
		}
	}
	return false
}
