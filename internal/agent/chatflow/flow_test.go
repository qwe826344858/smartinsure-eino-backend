package chatflow

import (
	"context"
	"testing"
)

func TestProductDetailWithoutRunnerReturnsNotImplementedErrorEvent(t *testing.T) {
	flow := New()
	events := collect(flow.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: "https://example.com/product",
		RequestID:  "rid-1",
	}))

	if len(events) != 2 {
		t.Fatalf("expected error and done events, got %d", len(events))
	}
	if events[0].Name != EventError {
		t.Fatalf("first event = %q, want %q", events[0].Name, EventError)
	}
	if events[1].Name != EventDone {
		t.Fatalf("second event = %q, want %q", events[1].Name, EventDone)
	}
}

func TestProductDetailRunnerForwardsEventsAndDone(t *testing.T) {
	flow := New()
	flow.Detail = fakeDetailRunner{}

	events := collect(flow.Run(context.Background(), Request{
		Action:      "product_detail",
		ProductURL:  "https://example.com/product",
		ProductName: "测试产品",
		RequestID:   "rid-detail",
	}))

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d: %#v", len(events), events)
	}
	if events[0].Name != EventStatus {
		t.Fatalf("first event = %q, want %q", events[0].Name, EventStatus)
	}
	if events[1].Name != EventDetailItems {
		t.Fatalf("second event = %q, want %q", events[1].Name, EventDetailItems)
	}
	if events[2].Name != EventDelta {
		t.Fatalf("third event = %q, want %q", events[2].Name, EventDelta)
	}
	if events[3].Name != EventDone {
		t.Fatalf("last event = %q, want %q", events[3].Name, EventDone)
	}
}

func TestProductDetailRequiresURL(t *testing.T) {
	flow := New()
	flow.Detail = fakeDetailRunner{}

	events := collect(flow.Run(context.Background(), Request{
		Action:    "product_detail",
		RequestID: "rid-no-url",
	}))

	if len(events) != 2 {
		t.Fatalf("expected error and done events, got %d", len(events))
	}
	if events[0].Name != EventError {
		t.Fatalf("first event = %q, want %q", events[0].Name, EventError)
	}
	if events[1].Name != EventDone {
		t.Fatalf("second event = %q, want %q", events[1].Name, EventDone)
	}
}

func TestNormalFlowEndsWithDone(t *testing.T) {
	flow := New()
	events := collect(flow.Run(context.Background(), Request{
		Message:   "百万医疗险怎么选？",
		RequestID: "rid-2",
	}))

	if len(events) == 0 {
		t.Fatal("expected events")
	}
	if events[len(events)-1].Name != EventDone {
		t.Fatalf("last event = %q, want %q", events[len(events)-1].Name, EventDone)
	}
}

func TestLiteFlowPassesHistoryToIntentAndAnswer(t *testing.T) {
	history := []ChatMessage{{ID: "m1", Role: "user", Content: "我 30 岁，预算 1500"}}
	intent := &historyCaptureIntent{result: IntentResult{Intent: "knowledge_explain"}}
	answer := &historyCaptureAnswer{chunks: []string{"ok"}}
	flow := New()
	flow.Intent = intent
	flow.Answer = answer

	events := collect(flow.Run(context.Background(), Request{
		Message:   "继续推荐",
		RequestID: "rid-history",
		History:   history,
	}))

	if len(events) == 0 {
		t.Fatal("expected events")
	}
	if len(intent.history) != 1 || intent.history[0].Content != history[0].Content {
		t.Fatalf("intent history = %#v", intent.history)
	}
	if len(answer.input.History) != 1 || answer.input.History[0].Content != history[0].Content {
		t.Fatalf("answer history = %#v", answer.input.History)
	}
}

func collect(ch <-chan Event) []Event {
	var events []Event
	for event := range ch {
		events = append(events, event)
	}
	return events
}

type fakeDetailRunner struct{}

func (fakeDetailRunner) Run(_ context.Context, _ DetailRequest) <-chan Event {
	ch := make(chan Event, 3)
	ch <- Event{Name: EventStatus, Data: map[string]string{"stage": "reading"}}
	ch <- Event{Name: EventDetailItems, Data: map[string]any{"duties": []string{"住院医疗"}}}
	ch <- Event{Name: EventDelta, Data: map[string]string{"text": "解读"}}
	close(ch)
	return ch
}

type historyCaptureIntent struct {
	result  IntentResult
	history []ChatMessage
}

func (f *historyCaptureIntent) Classify(context.Context, string) (IntentResult, error) {
	return f.result, nil
}

func (f *historyCaptureIntent) ClassifyWithHistory(_ context.Context, _ string, history []ChatMessage) (IntentResult, error) {
	f.history = history
	return f.result, nil
}

type historyCaptureAnswer struct {
	chunks []string
	input  AnswerInput
}

func (f *historyCaptureAnswer) Stream(_ context.Context, input AnswerInput) (<-chan string, <-chan error) {
	f.input = input
	chunks := make(chan string, len(f.chunks))
	errs := make(chan error, 1)
	for _, chunk := range f.chunks {
		chunks <- chunk
	}
	close(chunks)
	close(errs)
	return chunks, errs
}
