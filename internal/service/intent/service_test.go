package intent

import (
	"context"
	"strings"
	"testing"

	"smartinsure-eino-backend/internal/llm"
)

type fakeModel struct {
	data     map[string]any
	messages []llm.Message
}

func (f *fakeModel) CallText(context.Context, []llm.Message, float64) (string, error) {
	return "", nil
}

func (f *fakeModel) CallJSON(_ context.Context, messages []llm.Message, _ float64, out any) error {
	f.messages = messages
	target := out.(*map[string]any)
	*target = f.data
	return nil
}

func (f *fakeModel) StreamText(context.Context, []llm.Message, float64) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

func TestValidateIntentDefaultsInvalidValues(t *testing.T) {
	got := ValidateIntent(map[string]any{
		"intent":         "bad",
		"needs_followup": "yes",
		"missing_slots":  "age",
		"reason":         12,
	})

	if got.Intent != IntentOutOfScope {
		t.Fatalf("intent = %q, want %q", got.Intent, IntentOutOfScope)
	}
	if got.NeedsFollowup {
		t.Fatal("needs_followup should default to false")
	}
	if len(got.MissingSlots) != 0 {
		t.Fatalf("missing_slots = %#v, want empty", got.MissingSlots)
	}
	if got.Reason != "" {
		t.Fatalf("reason = %q, want empty", got.Reason)
	}
}

func TestApplyFollowupRulesProductRecommendationThreshold(t *testing.T) {
	got := ApplyFollowupRules(ValidateIntent(map[string]any{
		"intent":         IntentProductRecommendation,
		"needs_followup": true,
		"missing_slots":  []any{"budget"},
	}))
	if got.NeedsFollowup {
		t.Fatal("single missing recommendation slot should not trigger followup")
	}

	got = ApplyFollowupRules(ValidateIntent(map[string]any{
		"intent":         IntentProductRecommendation,
		"needs_followup": false,
		"missing_slots":  []any{"age", "budget", "noise", "age"},
	}))
	if !got.NeedsFollowup {
		t.Fatal("two missing recommendation slots should trigger followup")
	}
	if len(got.MissingSlots) != 2 || got.MissingSlots[0] != "age" || got.MissingSlots[1] != "budget" {
		t.Fatalf("missing_slots = %#v, want [age budget]", got.MissingSlots)
	}
}

func TestClassifyUsesLLMAndRules(t *testing.T) {
	svc := NewService(&fakeModel{data: map[string]any{
		"intent":         IntentComparison,
		"needs_followup": false,
		"missing_slots":  []any{"product_names"},
		"reason":         "need products",
	}})

	got, err := svc.Classify(context.Background(), "A 和 B 哪个好")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if got.Intent != IntentComparison || !got.NeedsFollowup {
		t.Fatalf("got %#v, want comparison followup", got)
	}
}

func TestClassifyWithHistoryInjectsRecentContext(t *testing.T) {
	model := &fakeModel{data: map[string]any{
		"intent":         IntentProductRecommendation,
		"needs_followup": false,
		"missing_slots":  []any{},
		"reason":         "history has age and budget",
	}}
	_, err := NewService(model).ClassifyWithHistory(context.Background(), "继续推荐", []HistoryMessage{
		{Role: "user", Content: "我 30 岁，预算 1500"},
		{Role: "assistant", Content: "可以考虑百万医疗险"},
	})
	if err != nil {
		t.Fatalf("ClassifyWithHistory returned error: %v", err)
	}
	if len(model.messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(model.messages))
	}
	prompt := model.messages[1].Content
	for _, want := range []string{"【近期对话上下文】", "- user: 我 30 岁，预算 1500", "- assistant: 可以考虑百万医疗险", "用户问题: 继续推荐"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestClassifyWithoutHistoryOmitsRecentContext(t *testing.T) {
	model := &fakeModel{data: map[string]any{"intent": IntentKnowledgeExplain}}
	_, err := NewService(model).Classify(context.Background(), "等待期是什么")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if strings.Contains(model.messages[1].Content, "【近期对话上下文】") {
		t.Fatalf("empty history should not be rendered: %s", model.messages[1].Content)
	}
}
