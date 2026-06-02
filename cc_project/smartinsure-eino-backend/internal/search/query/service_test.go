package query

import (
	"context"
	"strings"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/schema"
)

type fakeModel struct {
	data map[string]any
}

func (m fakeModel) CallText(context.Context, []llm.Message, float64) (string, error) {
	return "", nil
}

func (m fakeModel) CallJSON(_ context.Context, _ []llm.Message, _ float64, out any) error {
	ptr := out.(*map[string]any)
	*ptr = m.data
	return nil
}

func (m fakeModel) StreamText(context.Context, []llm.Message, float64) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

func TestGenerateFallbackInjectsCurrentYearForProductIntent(t *testing.T) {
	svc := NewService(nil, WithClock(func() time.Time {
		return time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	}))

	got, err := svc.Generate(context.Background(), "30岁想买医疗险，有没有推荐", schema.IntentProductRecommendation)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 || len(got) > 5 {
		t.Fatalf("len(got)=%d, want 3-5: %#v", len(got), got)
	}
	for _, q := range got {
		if !strings.Contains(q, "2026") {
			t.Fatalf("query missing current year: %#v", got)
		}
	}
	if !strings.Contains(got[0], "百万医疗险") {
		t.Fatalf("fallback did not normalize insurance keyword: %#v", got)
	}
}

func TestGenerateUsesLLMResultAndValidateQuery(t *testing.T) {
	svc := NewService(fakeModel{data: map[string]any{
		"queries": []any{"好医保 价格", " ", 123, "好医保 测评"},
	}}, WithClock(func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}))

	got, err := svc.Generate(context.Background(), "好医保怎么样", schema.IntentProductQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 {
		t.Fatalf("len(got)=%d, want fallback to top up: %#v", len(got), got)
	}
	if got[0] != "好医保 价格 2026" {
		t.Fatalf("first query=%q", got[0])
	}
	for _, q := range got {
		if !strings.Contains(q, "2026") {
			t.Fatalf("product query missing current year: %#v", got)
		}
	}
}
