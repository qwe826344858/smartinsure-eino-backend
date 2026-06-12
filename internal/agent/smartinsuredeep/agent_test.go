package smartinsuredeep

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"smartinsure-eino-backend/internal/agent/chatflow"
	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
)

func TestAgentMapsADKEventsToRuntimeEvents(t *testing.T) {
	productPayload := `{"summary":"ok","products":[{"id":"p1","name":"测试医疗险","company":"测试保险","url":"https://example.com/p"}]}`
	stream := schema.StreamReaderFromArray([]*schema.Message{
		schema.AssistantMessage("第一段", nil),
		schema.AssistantMessage("第二段", nil),
	})
	runner := &fakeADKRunner{events: []*adk.AgentEvent{
		adk.EventFromMessage(schema.ToolMessage(productPayload, "call-1", schema.WithToolName(toolProductSearch)), nil, schema.Tool, toolProductSearch),
		adk.EventFromMessage(nil, stream, schema.Assistant, ""),
	}}
	agent := &Agent{id: DefaultID, runner: runner}

	events := collect(agent.Run(context.Background(), agentruntime.AgentRequest{
		RequestID: "req-deep",
		AgentID:   DefaultID,
		Message:   "百万医疗险怎么选",
	}))

	if len(runner.messages) != 1 || runner.messages[0].Content != "百万医疗险怎么选" {
		t.Fatalf("messages = %#v", runner.messages)
	}
	if !hasEvent(events, "products") {
		t.Fatalf("products event missing: %#v", events)
	}
	if got := joinedDelta(events); got != "第一段第二段" {
		t.Fatalf("delta text = %q", got)
	}
	if last := events[len(events)-1]; last.Name != "done" {
		t.Fatalf("last event = %#v, want done", last)
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok || data["agent_id"] != DefaultID || data["requestId"] != "req-deep" || data["trace_id"] == "" {
		t.Fatalf("trace fields missing: %#v", events[0].Data)
	}
}

func TestAgentMapsKnowledgeSearchProductsToRuntimeEvents(t *testing.T) {
	knowledgePayload := `{"summary":"ok","results":[{"title":"测试医疗险 - 外购药","url":"https://example.com/p","site":"huize","product_url":"https://example.com/p","product_name":"测试医疗险"}],"sources":[{"title":"测试医疗险 - 外购药","url":"https://example.com/p","site":"huize","product_url":"https://example.com/p"}],"products":[{"id":"rag_p1","name":"测试医疗险","price_label":"详见产品页","url":"https://example.com/p","platform":"huize","brief":"外购药责任"}]}`
	runner := &fakeADKRunner{events: []*adk.AgentEvent{
		adk.EventFromMessage(schema.ToolMessage(knowledgePayload, "call-knowledge", schema.WithToolName(toolKnowledgeSearch)), nil, schema.Tool, toolKnowledgeSearch),
	}}
	agent := &Agent{id: RAGAgentID, runner: runner}

	events := collect(agent.Run(context.Background(), agentruntime.AgentRequest{
		RequestID: "req-rag-products",
		AgentID:   RAGAgentID,
		Message:   "帮我匹配外购药保障强的产品",
	}))

	products := eventItems(events, chatflow.EventProducts)
	if len(products) != 1 {
		t.Fatalf("products event items = %#v, want 1 item; events=%#v", products, events)
	}
	product, ok := products[0].(map[string]any)
	if !ok || product["url"] != "https://example.com/p" {
		t.Fatalf("product card url missing: %#v", products[0])
	}
	sources := eventItems(events, chatflow.EventSources)
	if len(sources) != 1 {
		t.Fatalf("sources event items = %#v, want 1 item; events=%#v", sources, events)
	}
	source, ok := sources[0].(map[string]any)
	if !ok || source["product_url"] != "https://example.com/p" {
		t.Fatalf("source product_url missing: %#v", sources[0])
	}
}

func TestAgentRunsDirectDetailActionWithoutADK(t *testing.T) {
	runner := &fakeADKRunner{}
	detail := &fakeDetailRunner{events: []chatflow.Event{
		{Name: chatflow.EventDetailItems, Data: map[string]any{"product_name": "测试产品"}},
		{Name: chatflow.EventDone, Data: map[string]string{"requestId": "req-detail"}},
	}}
	agent := &Agent{id: DefaultID, runner: runner, flow: &chatflow.Flow{Detail: detail}, directDetailEnabled: true}

	events := collect(agent.Run(context.Background(), agentruntime.AgentRequest{
		RequestID:   "req-detail",
		AgentID:     DefaultID,
		Action:      "product_detail",
		ProductURL:  "https://example.com/p",
		ProductName: "测试产品",
		Message:     "保障责任有哪些？",
	}))

	if len(runner.messages) != 0 {
		t.Fatalf("runner should not be called for direct detail action: %#v", runner.messages)
	}
	if detail.request.ProductURL != "https://example.com/p" || detail.request.ProductName != "测试产品" || detail.request.UserQuestion != "保障责任有哪些？" {
		t.Fatalf("detail request = %#v", detail.request)
	}
	if !hasEvent(events, chatflow.EventDetailItems) {
		t.Fatalf("detail_items event missing: %#v", events)
	}
	if last := events[len(events)-1]; last.Name != chatflow.EventDone {
		t.Fatalf("last event = %#v, want done", last)
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok || data["agent_id"] != DefaultID || data["requestId"] != "req-detail" || data["trace_id"] == "" {
		t.Fatalf("trace fields missing: %#v", events[0].Data)
	}
}

func TestRAGAgentDoesNotRunDirectDetailAction(t *testing.T) {
	runner := &fakeADKRunner{events: []*adk.AgentEvent{
		adk.EventFromMessage(schema.AssistantMessage("RAG 匹配回答", nil), nil, schema.Assistant, ""),
	}}
	detail := &fakeDetailRunner{events: []chatflow.Event{
		{Name: chatflow.EventDetailItems, Data: map[string]any{"product_name": "不应触发"}},
	}}
	agent := &Agent{id: RAGAgentID, runner: runner, flow: &chatflow.Flow{Detail: detail}, directDetailEnabled: false}

	events := collect(agent.Run(context.Background(), agentruntime.AgentRequest{
		RequestID:   "req-rag-detail",
		AgentID:     RAGAgentID,
		Action:      "product_detail",
		ProductURL:  "https://example.com/p",
		ProductName: "测试产品",
		Message:     "保障责任有哪些？",
	}))

	if len(runner.messages) != 1 {
		t.Fatalf("runner should be called for rag agent, messages=%#v", runner.messages)
	}
	if detail.request.ProductURL != "" {
		t.Fatalf("detail runner should not be called, request=%#v", detail.request)
	}
	if hasEvent(events, chatflow.EventDetailItems) {
		t.Fatalf("detail_items should not be emitted by rag agent direct path: %#v", events)
	}
	if got := joinedDelta(events); !strings.Contains(got, "RAG 匹配回答") {
		t.Fatalf("delta text = %q", got)
	}
}

func TestRAGAgentRunsGuardSearchWhenModelSkipsKnowledgeTool(t *testing.T) {
	runner := &fakeADKRunner{events: []*adk.AgentEvent{
		adk.EventFromMessage(schema.AssistantMessage("我根据上下文先给出推荐。", nil), nil, schema.Assistant, ""),
	}}
	fallback := &fakeFallbackSearcher{results: []chatflow.SearchResultItem{
		{
			Title:       "慧择测试医疗险 - 产品摘要",
			URL:         "https://www.huize.com/product/1",
			Site:        "huize",
			Snippet:     "适合作为慧择平台医疗险候选。",
			ProductURL:  "https://www.huize.com/product/1",
			ProductName: "慧择测试医疗险",
			Tags:        []string{"医疗险", "慧择"},
		},
	}}
	agent := &Agent{
		id:          RAGAgentID,
		runner:      runner,
		flow:        &chatflow.Flow{Fallback: fallback},
		toolTimeout: time.Second,
	}

	events := collect(agent.Run(context.Background(), agentruntime.AgentRequest{
		RequestID: "req-rag-guard",
		AgentID:   RAGAgentID,
		Message:   "推荐慧泽平台的保险商品",
	}))

	if fallback.calls != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallback.calls)
	}
	if !strings.Contains(fallback.query, "慧择") || !strings.Contains(fallback.query, "huize") {
		t.Fatalf("fallback query should include platform aliases, got %q", fallback.query)
	}
	products := eventItems(events, chatflow.EventProducts)
	if len(products) != 1 {
		t.Fatalf("products event items = %#v, want 1 item; events=%#v", products, events)
	}
	product, ok := products[0].(map[string]any)
	if !ok || product["url"] != "https://www.huize.com/product/1" {
		t.Fatalf("product card url missing: %#v", products[0])
	}
	sources := eventItems(events, chatflow.EventSources)
	if len(sources) != 1 {
		t.Fatalf("sources event items = %#v, want 1 item; events=%#v", sources, events)
	}
	if last := events[len(events)-1]; last.Name != chatflow.EventDone {
		t.Fatalf("last event = %#v, want done", last)
	}
}

func TestRAGAgentSkipsGuardSearchWhenKnowledgeToolAlreadyUsed(t *testing.T) {
	knowledgePayload := `{"summary":"ok","products":[{"id":"rag_p1","name":"测试医疗险","url":"https://example.com/p"}]}`
	runner := &fakeADKRunner{events: []*adk.AgentEvent{
		adk.EventFromMessage(schema.ToolMessage(knowledgePayload, "call-knowledge", schema.WithToolName(toolKnowledgeSearch)), nil, schema.Tool, toolKnowledgeSearch),
	}}
	fallback := &fakeFallbackSearcher{results: []chatflow.SearchResultItem{
		{Title: "不应触发", URL: "https://example.com/unused", ProductURL: "https://example.com/unused"},
	}}
	agent := &Agent{
		id:          RAGAgentID,
		runner:      runner,
		flow:        &chatflow.Flow{Fallback: fallback},
		toolTimeout: time.Second,
	}

	events := collect(agent.Run(context.Background(), agentruntime.AgentRequest{
		RequestID: "req-rag-no-guard",
		AgentID:   RAGAgentID,
		Message:   "推荐慧择平台的保险商品",
	}))

	if fallback.calls != 0 {
		t.Fatalf("fallback calls = %d, want 0", fallback.calls)
	}
	products := eventItems(events, chatflow.EventProducts)
	if len(products) != 1 {
		t.Fatalf("products event items = %#v, want 1 item; events=%#v", products, events)
	}
}

func TestAgentFinalizesWhenMaxIterationsExceeded(t *testing.T) {
	runner := &fakeADKRunner{events: []*adk.AgentEvent{
		adk.EventFromMessage(schema.ToolMessage(`{"summary":"已检索到百万医疗险资料"}`, "call-1", schema.WithToolName(toolKnowledgeSearch)), nil, schema.Tool, toolKnowledgeSearch),
		{Err: fmt.Errorf("run node[ChatModel] pre processor fail: %w", adk.ErrExceedMaxIterations)},
	}}
	agent := &Agent{
		id:           DefaultID,
		runner:       runner,
		finalModel:   fakeFinalModel{content: "基于已检索到的信息，百万医疗险需要重点确认免赔额、外购药责任和续保条件。"},
		finalTimeout: time.Second,
	}

	events := collect(agent.Run(context.Background(), agentruntime.AgentRequest{
		RequestID: "req-max",
		AgentID:   DefaultID,
		Message:   "百万医疗险怎么选？",
	}))

	if hasEvent(events, chatflow.EventError) {
		t.Fatalf("error event should not be emitted on max iteration fallback: %#v", events)
	}
	if got := joinedDelta(events); !strings.Contains(got, "百万医疗险需要重点确认") {
		t.Fatalf("fallback final delta missing, got %q", got)
	}
	if last := events[len(events)-1]; last.Name != chatflow.EventDone {
		t.Fatalf("last event = %#v, want done", last)
	}
}

type fakeADKRunner struct {
	messages []adk.Message
	events   []*adk.AgentEvent
}

func (r *fakeADKRunner) Run(_ context.Context, messages []adk.Message, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	r.messages = messages
	iter, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer gen.Close()
		for _, event := range r.events {
			gen.Send(event)
		}
	}()
	return iter
}

type fakeDetailRunner struct {
	request chatflow.DetailRequest
	events  []chatflow.Event
}

func (r *fakeDetailRunner) Run(_ context.Context, req chatflow.DetailRequest) <-chan chatflow.Event {
	r.request = req
	ch := make(chan chatflow.Event, len(r.events))
	for _, event := range r.events {
		ch <- event
	}
	close(ch)
	return ch
}

type fakeFallbackSearcher struct {
	query   string
	calls   int
	results []chatflow.SearchResultItem
	err     error
}

func (s *fakeFallbackSearcher) Search(_ context.Context, query string) ([]chatflow.SearchResultItem, error) {
	s.calls++
	s.query = query
	if s.err != nil {
		return nil, s.err
	}
	return s.results, nil
}

type fakeFinalModel struct {
	content string
	err     error
}

func (m fakeFinalModel) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	return schema.AssistantMessage(m.content, nil), nil
}

func (m fakeFinalModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(m.content, nil)}), m.err
}

func collect(ch <-chan agentruntime.AgentEvent) []agentruntime.AgentEvent {
	var events []agentruntime.AgentEvent
	for event := range ch {
		events = append(events, event)
	}
	return events
}

func hasEvent(events []agentruntime.AgentEvent, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}

func eventItems(events []agentruntime.AgentEvent, name string) []any {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		data, ok := event.Data.(map[string]any)
		if !ok {
			continue
		}
		items, ok := data["items"].([]any)
		if ok {
			return items
		}
	}
	return nil
}

func joinedDelta(events []agentruntime.AgentEvent) string {
	var b strings.Builder
	for _, event := range events {
		if event.Name != "delta" {
			continue
		}
		data, ok := event.Data.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := data["text"].(string); ok {
			b.WriteString(text)
		}
	}
	return b.String()
}
