package smartinsureagent

import (
	"context"
	"testing"

	"smartinsure-eino-backend/internal/agent/chatflow"
	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
)

func TestAgentRunsIndependentGraphAndAddsTraceFields(t *testing.T) {
	flow := chatflow.New()
	agent := New(NewGraphFromFlow(flow))

	events := collect(agent.Run(context.Background(), agentruntime.AgentRequest{
		RequestID:     "req-1",
		AgentID:       DefaultID,
		Message:       "百万医疗险怎么选",
		AnonymousID:   "anon-1",
		ChatSessionID: "chat-1",
		Metadata:      map[string]any{"source": "web"},
	}))

	if len(events) == 0 {
		t.Fatal("expected events")
	}
	if events[0].Name != chatflow.EventStatus {
		t.Fatalf("first event = %#v, want status", events[0])
	}
	last := events[len(events)-1]
	if last.Name != chatflow.EventDone {
		t.Fatalf("last event = %#v, want done", last)
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", events[0].Data)
	}
	if data["agent_id"] != DefaultID || data["trace_id"] == "" || data["requestId"] != "req-1" {
		t.Fatalf("trace fields missing: %#v", data)
	}
}

func TestAgentCanDisableTraceFields(t *testing.T) {
	agent := New(NewGraphFromFlow(chatflow.New()))
	events := collect(agent.Run(context.Background(), agentruntime.AgentRequest{
		RequestID:     "req-no-trace",
		AgentID:       DefaultID,
		Message:       "百万医疗险怎么选",
		TraceDisabled: true,
	}))

	if len(events) == 0 {
		t.Fatal("expected events")
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", events[0].Data)
	}
	if _, ok := data["trace_id"]; ok || events[0].TraceID != "" {
		t.Fatalf("trace should be disabled: event=%#v data=%#v", events[0], data)
	}
	if data["agent_id"] != DefaultID || data["requestId"] != "req-no-trace" {
		t.Fatalf("agent/request fields missing: %#v", data)
	}
}

func TestAgentGraphRoutesProductDetailAction(t *testing.T) {
	flow := chatflow.New()
	flow.Detail = fakeDetailRunner{}
	agent := New(NewGraphFromFlow(flow))

	events := collect(agent.Run(context.Background(), agentruntime.AgentRequest{
		RequestID:   "req-detail",
		AgentID:     DefaultID,
		Action:      "product_detail",
		ProductURL:  "https://example.com/p",
		ProductName: "测试产品",
	}))

	if len(events) != 3 {
		t.Fatalf("len(events) = %d", len(events))
	}
	if events[0].Name != chatflow.EventStatus || events[1].Name != chatflow.EventDetailItems || events[2].Name != chatflow.EventDone {
		t.Fatalf("events = %#v", events)
	}
}

func TestToChatflowRequestForwardsMetadataAndHistory(t *testing.T) {
	req := toGraphRequest(agentruntime.AgentRequest{
		RequestID: "req-meta",
		Message:   "hello",
		Metadata:  map[string]any{"source": "web"},
		History: []agentruntime.ChatMessage{{
			ID:       "m1",
			Role:     "user",
			Content:  "history",
			Metadata: map[string]any{"k": "v"},
		}},
	})

	if req.Metadata["source"] != "web" {
		t.Fatalf("metadata not forwarded: %#v", req.Metadata)
	}
	if len(req.History) != 1 || req.History[0].Metadata["k"] != "v" {
		t.Fatalf("history not forwarded: %#v", req.History)
	}
}

type fakeDetailRunner struct{}

func (fakeDetailRunner) Run(_ context.Context, req chatflow.DetailRequest) <-chan chatflow.Event {
	ch := make(chan chatflow.Event, 2)
	ch <- chatflow.Event{Name: chatflow.EventDetailItems, Data: map[string]any{
		"product_name": req.ProductName,
		"duties":       []map[string]any{{"name": "一般医疗", "coverage": "300万"}},
	}}
	ch <- chatflow.Event{Name: chatflow.EventDone, Data: map[string]string{"requestId": req.RequestID}}
	close(ch)
	return ch
}

func collect(ch <-chan agentruntime.AgentEvent) []agentruntime.AgentEvent {
	var events []agentruntime.AgentEvent
	for event := range ch {
		events = append(events, event)
	}
	return events
}
