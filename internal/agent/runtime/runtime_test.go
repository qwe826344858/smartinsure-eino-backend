package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryAndRuntimeRunRegisteredAgent(t *testing.T) {
	registry := NewRegistry()
	agent := fakeAgent{id: DefaultAgentID}
	if err := registry.Register(agent); err != nil {
		t.Fatal(err)
	}

	events, err := New(registry).Run(context.Background(), AgentRequest{Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	event := <-events
	if event.Name != "done" {
		t.Fatalf("event.Name = %q", event.Name)
	}
	if event.AgentID != DefaultAgentID {
		t.Fatalf("event.AgentID = %q", event.AgentID)
	}
}

func TestRuntimeReturnsAgentNotFound(t *testing.T) {
	_, err := New(NewRegistry()).Run(context.Background(), AgentRequest{AgentID: "missing"})
	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("err = %v, want ErrAgentNotFound", err)
	}
}

func TestAddTraceFieldsPreservesPayloadAndAddsAgentFields(t *testing.T) {
	payload := AddTraceFields(map[string]string{"text": "hi"}, "req-1", "agent-1", "trace-1")
	data, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T", payload)
	}
	for key, want := range map[string]string{
		"text":       "hi",
		"requestId":  "req-1",
		"request_id": "req-1",
		"agent_id":   "agent-1",
		"trace_id":   "trace-1",
	} {
		if data[key] != want {
			t.Fatalf("%s = %#v, want %q", key, data[key], want)
		}
	}
}

type fakeAgent struct {
	id string
}

func (f fakeAgent) ID() string {
	return f.id
}

func (f fakeAgent) Run(_ context.Context, req AgentRequest) <-chan AgentEvent {
	ch := make(chan AgentEvent, 1)
	ch <- AgentEvent{Name: "done", RequestID: req.RequestID, AgentID: req.AgentID}
	close(ch)
	return ch
}
