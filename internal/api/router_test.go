package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smartinsure-eino-backend/internal/agent/chatflow"
	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
	"smartinsure-eino-backend/internal/agent/smartinsuredeep"
)

func TestHealthz(t *testing.T) {
	handler := NewHandler(nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
}

func TestChatRejectsEmptyMessageWithoutAction(t *testing.T) {
	handler := NewHandler(nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"   "}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"INVALID_ARGUMENT"`) {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
}

func TestChatProductDetailActionStreamsEvents(t *testing.T) {
	flow := chatflow.New()
	flow.Detail = apiFakeDetailRunner{}
	handler := NewHandler(flow)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"action":"product_detail","productUrl":"https://example.com/p","requestId":"rid-api"}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{"event: detail_items", "event: delta", "event: done"} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "NOT_IMPLEMENTED") {
		t.Fatalf("SSE body should not contain NOT_IMPLEMENTED: %s", body)
	}
}

func TestChatRouteDoesNotEmitAgentTraceFields(t *testing.T) {
	handler := NewHandler(chatflow.New())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"等待期是什么？","requestId":"rid-chat-no-agent"}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, `"agent_id"`) || strings.Contains(body, `"trace_id"`) {
		t.Fatalf("/api/chat should not emit agent trace fields: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("SSE body missing done: %s", body)
	}
}

func TestAgentChatStreamsTraceFields(t *testing.T) {
	setAgentRouteTestEnv(t)
	t.Setenv("AGENT_TRACE_ENABLED", "true")
	handler := NewHandler(chatflow.New())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(`{"message":"百万医疗险怎么选？","requestId":"rid-agent","metadata":{"source":"web"}}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"event: status",
		"event: delta",
		"event: done",
		`"agent_id":"smartinsure-advisor"`,
		`"trace_id":"trace_`,
		`"requestId":"rid-agent"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q: %s", want, body)
		}
	}
}

func TestAgentChatCanDisableTraceFields(t *testing.T) {
	setAgentRouteTestEnv(t)
	t.Setenv("AGENT_TRACE_ENABLED", "false")
	handler := NewHandler(chatflow.New())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(`{"message":"百万医疗险怎么选？","requestId":"rid-agent-no-trace"}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{`"agent_id":"smartinsure-advisor"`, `"requestId":"rid-agent-no-trace"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"trace_id"`) {
		t.Fatalf("trace_id should be disabled: %s", body)
	}
}

func TestAgentChatAcceptsSnakeCaseProductDetailFields(t *testing.T) {
	setAgentRouteTestEnv(t)
	flow := chatflow.New()
	flow.Detail = apiFakeDetailRunner{}
	handler := NewHandler(flow)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(`{"action":"product_detail","product_url":"https://example.com/p","product_name":"测试产品","requestId":"rid-agent-detail"}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{"event: detail_items", "event: delta", `"agent_id":"smartinsure-advisor"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "NOT_IMPLEMENTED") {
		t.Fatalf("SSE body should not contain NOT_IMPLEMENTED: %s", body)
	}
}

func TestAgentChatDoesNotUseLegacyWorkflowRunner(t *testing.T) {
	setAgentRouteTestEnv(t)
	handler := NewHandler(apiPanicRunner{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(`{"message":"写代码","requestId":"rid-agent-no-workflow"}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"agent_id":"smartinsure-advisor"`) {
		t.Fatalf("SSE body missing agent id: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("SSE body missing done: %s", body)
	}
}

func TestAgentDeepChatForcesDeepAgentID(t *testing.T) {
	setAgentRouteTestEnv(t)
	server := newServerWithFakeAgent(t, apiFakeAgent{id: smartinsuredeep.DefaultID})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/deep-chat", strings.NewReader(`{"message":"百万医疗险怎么选？","agent_id":"smartinsure-advisor","requestId":"rid-deep"}`))
	request.Header.Set("Content-Type", "application/json")

	server.agentDeepChat(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{`"agent_id":"smartinsure-deep-advisor"`, `"requestId":"rid-deep"`, "event: delta", "event: done"} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"agent_id":"smartinsure-advisor"`) {
		t.Fatalf("deep-chat should ignore request agent_id override: %s", body)
	}
}

func TestAgentChatPassesIncludeThinkFlag(t *testing.T) {
	setAgentRouteTestEnv(t)
	server := newServerWithFakeAgent(t, apiFakeAgent{id: smartinsuredeep.DefaultID})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/deep-chat", strings.NewReader(`{"message":"百万医疗险怎么选？","include_think":true,"requestId":"rid-think"}`))
	request.Header.Set("Content-Type", "application/json")

	server.agentDeepChat(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"include_think":true`) {
		t.Fatalf("include_think flag was not passed to agent: %s", body)
	}
}

func TestRAGAgentChatForcesRAGAgentID(t *testing.T) {
	setAgentRouteTestEnv(t)
	server := newServerWithFakeAgent(t, apiFakeAgent{id: smartinsuredeep.RAGAgentID})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat/rag-agent", strings.NewReader(`{"message":"帮我匹配适合家庭投保的高端医疗险","agent_id":"smartinsure-deep-advisor","requestId":"rid-rag-agent"}`))
	request.Header.Set("Content-Type", "application/json")

	server.agentRAGChat(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{`"agent_id":"smartinsure-rag-advisor"`, `"requestId":"rid-rag-agent"`, "event: delta", "event: done"} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"agent_id":"smartinsure-deep-advisor"`) {
		t.Fatalf("rag-agent should ignore request agent_id override: %s", body)
	}
}

func TestRAGAgentChatRouteWithoutAPIPrefix(t *testing.T) {
	setAgentRouteTestEnv(t)
	server := newServerWithFakeAgent(t, apiFakeAgent{id: smartinsuredeep.RAGAgentID})
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/rag-agent", server.agentRAGChat)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/chat/rag-agent", strings.NewReader(`{"message":"外购药保障强的百万医疗险","requestId":"rid-rag-short"}`))
	request.Header.Set("Content-Type", "application/json")

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"agent_id":"smartinsure-rag-advisor"`) || !strings.Contains(body, "event: done") {
		t.Fatalf("unexpected SSE body: %s", body)
	}
}

func TestLimitAgentHistoryKeepsNewestMessages(t *testing.T) {
	history := []agentruntime.ChatMessage{
		{ID: "1", Content: "old"},
		{ID: "2", Content: "mid"},
		{ID: "3", Content: "new"},
	}
	got := limitAgentHistory(history, 2)
	if len(got) != 2 || got[0].ID != "2" || got[1].ID != "3" {
		t.Fatalf("history = %#v", got)
	}
	if len(limitAgentHistory(history, 0)) != 3 {
		t.Fatalf("non-positive limit should keep existing history")
	}
}

func setAgentRouteTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AGENT_CHAT_ENABLED", "true")
	t.Setenv("AGENT_DEFAULT_ID", "smartinsure-advisor")
}

func newServerWithFakeAgent(t *testing.T, agent agentruntime.Agent) *Server {
	t.Helper()
	server := NewServer(chatflow.New())
	registry := agentruntime.NewRegistry()
	if err := registry.Register(agent); err != nil {
		t.Fatal(err)
	}
	server.agentRuntime = agentruntime.New(registry)
	return server
}

type apiFakeDetailRunner struct{}

func (apiFakeDetailRunner) Run(_ context.Context, _ chatflow.DetailRequest) <-chan chatflow.Event {
	ch := make(chan chatflow.Event, 2)
	ch <- chatflow.Event{Name: chatflow.EventDetailItems, Data: map[string]any{
		"product_name": "测试产品",
		"duties":       []map[string]any{{"name": "一般医疗", "coverage": "300万"}},
	}}
	ch <- chatflow.Event{Name: chatflow.EventDelta, Data: map[string]string{"text": "解读"}}
	close(ch)
	return ch
}

type apiPanicRunner struct{}

func (apiPanicRunner) Run(context.Context, chatflow.Request) <-chan chatflow.Event {
	panic("legacy workflow runner must not be used by /api/agent/chat")
}

type apiFakeAgent struct {
	id string
}

func (a apiFakeAgent) ID() string {
	return a.id
}

func (a apiFakeAgent) Run(_ context.Context, req agentruntime.AgentRequest) <-chan agentruntime.AgentEvent {
	traceID := ""
	if !req.TraceDisabled {
		traceID = agentruntime.NewTraceID()
	}
	ch := make(chan agentruntime.AgentEvent, 4)
	ch <- agentruntime.AgentEvent{
		Name:      chatflow.EventStatus,
		Data:      agentruntime.AddTraceFields(map[string]any{"stage": "reasoning", "message": "fake deep", "include_think": req.IncludeThink}, req.RequestID, req.AgentID, traceID),
		RequestID: req.RequestID,
		AgentID:   req.AgentID,
		TraceID:   traceID,
	}
	if req.Action == "product_detail" {
		ch <- agentruntime.AgentEvent{
			Name: chatflow.EventDetailItems,
			Data: agentruntime.AddTraceFields(map[string]any{
				"product_name": req.ProductName,
				"duties":       []map[string]any{{"name": "一般医疗", "coverage": "300万"}},
			}, req.RequestID, req.AgentID, traceID),
			RequestID: req.RequestID,
			AgentID:   req.AgentID,
			TraceID:   traceID,
		}
	}
	ch <- agentruntime.AgentEvent{
		Name:      chatflow.EventDelta,
		Data:      agentruntime.AddTraceFields(map[string]string{"text": "deep response"}, req.RequestID, req.AgentID, traceID),
		RequestID: req.RequestID,
		AgentID:   req.AgentID,
		TraceID:   traceID,
	}
	ch <- agentruntime.AgentEvent{
		Name:      chatflow.EventDone,
		Data:      agentruntime.AddTraceFields(map[string]string{"requestId": req.RequestID}, req.RequestID, req.AgentID, traceID),
		RequestID: req.RequestID,
		AgentID:   req.AgentID,
		TraceID:   traceID,
	}
	close(ch)
	return ch
}
