package smartinsureagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/agent/chatflow"
	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
)

func TestAgentGraphPlanActSequenceStreamsCompatibleEvents(t *testing.T) {
	price := "100元/年起"
	search := &agentGraphFakeProductSearch{products: []chatflow.ProductCard{{
		ID:         "p1",
		Name:       "测试医疗险",
		Price:      &price,
		PriceLabel: price,
		URL:        "https://example.com/p",
	}}}
	knowledge := &agentGraphFakeKnowledgeSearch{results: []chatflow.SearchResultItem{{
		Title: "百万医疗险选购指南",
		URL:   "https://example.com/guide",
		Site:  "example.com",
	}}}
	answer := &agentGraphFakeAnswer{chunks: []string{"这是最终建议。"}}
	reasoner := &scriptedReasoner{decisions: []AgentDecision{
		{Thought: "hidden product search thought", Action: ActionProductSearch, ActionInput: map[string]any{"query": "百万医疗险"}},
		{Thought: "hidden knowledge search thought", Action: ActionKnowledgeSearch, ActionInput: map[string]any{"query": "百万医疗险"}},
		{Thought: "hidden final thought", Action: ActionFinalAnswer, ActionInput: map[string]any{"answer_brief": "生成建议"}},
	}}
	graph := &AgentGraph{
		reasoner:      reasoner,
		tools:         AgentTools{search: search, fallback: knowledge},
		answer:        answer,
		maxIterations: 4,
		toolTimeout:   time.Second,
	}

	events := collectGraphEvents(graph.Run(context.Background(), agentruntime.AgentRequest{
		Message:   "百万医疗险怎么选？",
		RequestID: "rid-plan-act",
	}))

	wantOrder := []string{
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventProducts,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventDelta,
		chatflow.EventSources,
		chatflow.EventDisclaimer,
		chatflow.EventDone,
	}
	if got := agentGraphEventNames(events); !sameGraphStrings(got, wantOrder) {
		t.Fatalf("events = %#v, want %#v", got, wantOrder)
	}
	if search.calls != 1 || knowledge.calls != 1 || answer.calls != 1 {
		t.Fatalf("calls search=%d knowledge=%d answer=%d", search.calls, knowledge.calls, answer.calls)
	}
	if !reasoner.sawProductObservationBeforeSecondAction {
		t.Fatal("reasoner did not receive product observation before second action")
	}
	if len(answer.input.Results) != 1 || answer.input.Results[0].Title != "百万医疗险选购指南" {
		t.Fatalf("answer input results = %#v", answer.input.Results)
	}
	for _, want := range []string{"生成建议", "测试医疗险", "产品搜索返回", "知识检索返回"} {
		if !strings.Contains(answer.input.Message, want) {
			t.Fatalf("answer input missing %q: %s", want, answer.input.Message)
		}
	}
	if strings.Contains(answer.input.Message, "hidden") || strings.Contains(answer.input.Message, "thought") {
		t.Fatalf("answer input leaked thought: %s", answer.input.Message)
	}
	if !hasGraphStatus(events, "tool_running", string(ActionProductSearch)) || !hasGraphStatus(events, "observing", string(ActionProductSearch)) {
		t.Fatalf("missing product search status: %#v", events)
	}
	if !hasGraphStatus(events, "tool_running", string(ActionKnowledgeSearch)) || !hasGraphStatus(events, "observing", string(ActionKnowledgeSearch)) {
		t.Fatalf("missing knowledge search status: %#v", events)
	}
	payload, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "hidden") || strings.Contains(string(payload), "thought") {
		t.Fatalf("thought leaked to SSE payload: %s", payload)
	}
}

func TestAgentGraphAskFollowupDoesNotCallTools(t *testing.T) {
	search := &agentGraphFakeProductSearch{}
	knowledge := &agentGraphFakeKnowledgeSearch{}
	answer := &agentGraphFakeAnswer{chunks: []string{"should not stream"}}
	graph := &AgentGraph{
		reasoner: &scriptedReasoner{decisions: []AgentDecision{{
			Thought:     "hidden followup thought",
			Action:      ActionAskFollowup,
			ActionInput: map[string]any{"question": "请补充年龄和预算。"},
		}}},
		tools:         AgentTools{search: search, fallback: knowledge},
		answer:        answer,
		maxIterations: 3,
		toolTimeout:   time.Second,
	}

	events := collectGraphEvents(graph.Run(context.Background(), agentruntime.AgentRequest{
		Message:   "给我推荐保险",
		RequestID: "rid-followup",
	}))

	if got := agentGraphEventNames(events); !sameGraphStrings(got, []string{chatflow.EventStatus, chatflow.EventDelta, chatflow.EventDone}) {
		t.Fatalf("events = %#v", got)
	}
	if search.calls != 0 || knowledge.calls != 0 || answer.calls != 0 {
		t.Fatalf("unexpected calls search=%d knowledge=%d answer=%d", search.calls, knowledge.calls, answer.calls)
	}
	if got := graphDeltaText(events); got != "请补充年龄和预算。" {
		t.Fatalf("delta text = %q", got)
	}
}

func TestAgentGraphInvalidActionsFallbackAfterLimit(t *testing.T) {
	answer := &agentGraphFakeAnswer{chunks: []string{"降级回答"}}
	reasoner := &scriptedReasoner{decisions: []AgentDecision{
		{Thought: "bad action one", Action: AgentActionName("bad_action")},
		{Thought: "bad action two", Action: AgentActionName("bad_action")},
		{Thought: "should not run", Action: ActionFinalAnswer, ActionInput: map[string]any{"answer_text": "unexpected"}},
	}}
	graph := &AgentGraph{
		reasoner:      reasoner,
		answer:        answer,
		maxIterations: 4,
		toolTimeout:   time.Second,
	}

	events := collectGraphEvents(graph.Run(context.Background(), agentruntime.AgentRequest{
		Message:   "等待期是什么？",
		RequestID: "rid-invalid",
	}))

	if reasoner.calls != maxInvalidActionDecisions {
		t.Fatalf("reasoner calls = %d", reasoner.calls)
	}
	if answer.calls != 1 {
		t.Fatalf("answer calls = %d", answer.calls)
	}
	if !sameGraphStrings(agentGraphEventNames(events), []string{
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventDelta,
		chatflow.EventDisclaimer,
		chatflow.EventDone,
	}) {
		t.Fatalf("events = %#v", agentGraphEventNames(events))
	}
}

func TestAgentGraphToolErrorRecordsObservationAndFallbacks(t *testing.T) {
	search := &agentGraphFakeProductSearch{err: errors.New("search failed")}
	answer := &agentGraphFakeAnswer{chunks: []string{"工具失败后的回答"}}
	reasoner := &scriptedReasoner{decisions: []AgentDecision{
		{Thought: "hidden product search thought", Action: ActionProductSearch, ActionInput: map[string]any{"query": "百万医疗险"}},
		{Thought: "hidden final thought", Action: ActionFinalAnswer, ActionInput: map[string]any{"answer_brief": "基于已有信息回答"}},
	}}
	graph := &AgentGraph{
		reasoner:      reasoner,
		tools:         AgentTools{search: search},
		answer:        answer,
		maxIterations: 3,
		toolTimeout:   time.Second,
	}

	events := collectGraphEvents(graph.Run(context.Background(), agentruntime.AgentRequest{
		Message:   "百万医疗险怎么选？",
		RequestID: "rid-tool-error",
	}))

	if answer.calls != 1 {
		t.Fatalf("answer calls = %d", answer.calls)
	}
	if !sameGraphStrings(agentGraphEventNames(events), []string{
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventDelta,
		chatflow.EventDisclaimer,
		chatflow.EventDone,
	}) {
		t.Fatalf("events = %#v", agentGraphEventNames(events))
	}
}

func TestAgentGraphMaxIterationsFallbacks(t *testing.T) {
	search := &agentGraphFakeProductSearch{}
	answer := &agentGraphFakeAnswer{chunks: []string{"达到上限后的回答"}}
	reasoner := &scriptedReasoner{decisions: []AgentDecision{
		{Thought: "first repeated action", Action: ActionProductSearch, ActionInput: map[string]any{"query": "百万医疗险"}},
		{Thought: "second repeated action", Action: ActionProductSearch, ActionInput: map[string]any{"query": "百万医疗险"}},
	}}
	graph := &AgentGraph{
		reasoner:      reasoner,
		tools:         AgentTools{search: search},
		answer:        answer,
		maxIterations: 2,
		toolTimeout:   time.Second,
	}

	events := collectGraphEvents(graph.Run(context.Background(), agentruntime.AgentRequest{
		Message:   "百万医疗险怎么选？",
		RequestID: "rid-max-iterations",
	}))

	if reasoner.calls != 2 || search.calls != 1 || answer.calls != 1 {
		t.Fatalf("calls reasoner=%d search=%d answer=%d", reasoner.calls, search.calls, answer.calls)
	}
	if !sameGraphStrings(agentGraphEventNames(events), []string{
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventDelta,
		chatflow.EventDisclaimer,
		chatflow.EventDone,
	}) {
		t.Fatalf("events = %#v", agentGraphEventNames(events))
	}
	if !strings.Contains(answer.input.Message, "工具调用次数已达上限") {
		t.Fatalf("fallback brief missing: %s", answer.input.Message)
	}
	if !strings.Contains(answer.input.Message, "重复工具调用已拦截") {
		t.Fatalf("duplicate observation missing: %s", answer.input.Message)
	}
}

func TestHeuristicReasonerDoesNotRetryFailedProductSearch(t *testing.T) {
	search := &agentGraphFakeProductSearch{err: errors.New("search failed")}
	knowledge := &agentGraphFakeKnowledgeSearch{results: []chatflow.SearchResultItem{{
		Title: "投保建议",
		URL:   "https://example.com/advice",
		Site:  "example.com",
	}}}
	answer := &agentGraphFakeAnswer{chunks: []string{"基于知识检索回答"}}
	flow := chatflow.New()
	flow.Intent = agentGraphFakeIntent{result: chatflow.IntentResult{Intent: "product_recommendation"}}
	flow.Search = search
	flow.Fallback = knowledge
	flow.Answer = answer

	graph := NewGraphFromFlow(flow)
	events := collectGraphEvents(graph.Run(context.Background(), agentruntime.AgentRequest{
		Message:   "百万医疗险怎么选？",
		RequestID: "rid-tool-route",
	}))

	if search.calls != 1 || knowledge.calls != 1 || answer.calls != 1 {
		t.Fatalf("calls search=%d knowledge=%d answer=%d", search.calls, knowledge.calls, answer.calls)
	}
	if !hasGraphStatus(events, "tool_running", string(ActionProductSearch)) || !hasGraphStatus(events, "tool_running", string(ActionKnowledgeSearch)) {
		t.Fatalf("missing tool statuses: %#v", events)
	}
	if !strings.Contains(answer.input.Message, "search failed") {
		t.Fatalf("tool error observation missing: %s", answer.input.Message)
	}
}

func TestHeuristicReasonerAsksFollowupForGenericRecommendation(t *testing.T) {
	search := &agentGraphFakeProductSearch{}
	knowledge := &agentGraphFakeKnowledgeSearch{}
	answer := &agentGraphFakeAnswer{chunks: []string{"should not stream"}}
	flow := chatflow.New()
	flow.Intent = agentGraphFakeIntent{result: chatflow.IntentResult{Intent: "product_recommendation"}}
	flow.Followup = agentGraphFakeFollowup{text: "请补充被保人年龄、预算和关注的保障。"}
	flow.Search = search
	flow.Fallback = knowledge
	flow.Answer = answer

	graph := NewGraphFromFlow(flow)
	events := collectGraphEvents(graph.Run(context.Background(), agentruntime.AgentRequest{
		Message:   "给我推荐保险",
		RequestID: "rid-generic-followup",
	}))

	if got := agentGraphEventNames(events); !sameGraphStrings(got, []string{chatflow.EventStatus, chatflow.EventDelta, chatflow.EventDone}) {
		t.Fatalf("events = %#v", got)
	}
	if search.calls != 0 || knowledge.calls != 0 || answer.calls != 0 {
		t.Fatalf("unexpected calls search=%d knowledge=%d answer=%d", search.calls, knowledge.calls, answer.calls)
	}
	if got := graphDeltaText(events); got != "请补充被保人年龄、预算和关注的保障。" {
		t.Fatalf("delta text = %q", got)
	}
}

func TestAgentGraphPlannerProductDetailReturnsObservationBeforeFinalAnswer(t *testing.T) {
	detail := &agentGraphFakeDetailRunner{events: []chatflow.Event{
		{Name: chatflow.EventDetailItems, Data: map[string]any{
			"product_name": "测试产品",
			"duties":       []any{map[string]any{"name": "一般医疗"}},
		}},
		{Name: chatflow.EventDelta, Data: map[string]string{"text": "详情工具生成的中间解读"}},
	}}
	answer := &agentGraphFakeAnswer{chunks: []string{"最终详情回答"}}
	graph := &AgentGraph{
		reasoner: &scriptedReasoner{decisions: []AgentDecision{
			{Thought: "need detail", Action: ActionProductDetail, ActionInput: map[string]any{"product_url": "https://example.com/p", "product_name": "测试产品"}},
			{Thought: "final after detail", Action: ActionFinalAnswer, ActionInput: map[string]any{"answer_brief": "结合详情回答"}},
		}},
		tools:         AgentTools{detail: detail},
		answer:        answer,
		maxIterations: 3,
		toolTimeout:   time.Second,
	}

	events := collectGraphEvents(graph.Run(context.Background(), agentruntime.AgentRequest{
		Message:   "解析这个产品",
		RequestID: "rid-planner-detail",
	}))

	if detail.calls != 1 || answer.calls != 1 {
		t.Fatalf("calls detail=%d answer=%d", detail.calls, answer.calls)
	}
	if !sameGraphStrings(agentGraphEventNames(events), []string{
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventDetailItems,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventStatus,
		chatflow.EventDelta,
		chatflow.EventDisclaimer,
		chatflow.EventDone,
	}) {
		t.Fatalf("events = %#v", agentGraphEventNames(events))
	}
	if strings.Contains(graphDeltaText(events), "中间解读") {
		t.Fatalf("planner product_detail should not stream intermediate delta: %s", graphDeltaText(events))
	}
	for _, want := range []string{"结合详情回答", "测试产品", "duty_count"} {
		if !strings.Contains(answer.input.Message, want) {
			t.Fatalf("answer input missing %q: %s", want, answer.input.Message)
		}
	}
}

func TestAgentGraphProductDetailUsesToolTimeoutAndRecordsObservation(t *testing.T) {
	graph := &AgentGraph{
		tools:       AgentTools{detail: blockingDetailRunner{}},
		toolTimeout: 10 * time.Millisecond,
	}
	events := make(chan chatflow.Event, 4)
	state := &AgentState{
		Request: chatflow.Request{RequestID: "rid-detail-timeout", Action: "product_detail"},
		Events:  events,
	}
	step := AgentStep{Action: AgentAction{
		Name: ActionProductDetail,
		Input: map[string]any{
			"product_url": "https://example.com/p",
		},
	}}

	done := graph.executeProductDetail(context.Background(), state, &step)
	close(events)
	collected := collectGraphEvents(events)

	if !done {
		t.Fatal("product detail should end request on timeout")
	}
	if step.Err == "" || !strings.Contains(step.Err, "deadline") {
		t.Fatalf("step err = %q", step.Err)
	}
	if step.Observation.Data["error"] == "" {
		t.Fatalf("observation = %#v", step.Observation)
	}
	if !sameGraphStrings(agentGraphEventNames(collected), []string{chatflow.EventStatus, chatflow.EventError, chatflow.EventDone}) {
		t.Fatalf("events = %#v", agentGraphEventNames(collected))
	}
}

type scriptedReasoner struct {
	decisions                               []AgentDecision
	calls                                   int
	sawProductObservationBeforeSecondAction bool
}

func (r *scriptedReasoner) Next(_ context.Context, state *AgentState) (AgentDecision, error) {
	if r.calls == 1 && len(state.Products) == 1 && state.hasAction(ActionProductSearch) {
		r.sawProductObservationBeforeSecondAction = true
	}
	if len(r.decisions) == 0 {
		return AgentDecision{Action: ActionFinalAnswer, ActionInput: map[string]any{"answer_brief": "ok"}}, nil
	}
	index := r.calls
	if index >= len(r.decisions) {
		index = len(r.decisions) - 1
	}
	r.calls++
	return r.decisions[index], nil
}

type agentGraphFakeProductSearch struct {
	products []chatflow.ProductCard
	err      error
	calls    int
	queries  []string
}

type agentGraphFakeIntent struct {
	result chatflow.IntentResult
	err    error
}

func (f agentGraphFakeIntent) Classify(context.Context, string) (chatflow.IntentResult, error) {
	return f.result, f.err
}

func (f agentGraphFakeIntent) ClassifyWithHistory(context.Context, string, []chatflow.ChatMessage) (chatflow.IntentResult, error) {
	return f.result, f.err
}

type agentGraphFakeFollowup struct {
	text string
	err  error
}

func (f agentGraphFakeFollowup) Generate(context.Context, []string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.text, nil
}

func (f *agentGraphFakeProductSearch) Search(_ context.Context, query string) ([]chatflow.ProductCard, error) {
	f.calls++
	f.queries = append(f.queries, query)
	return f.products, f.err
}

type agentGraphFakeKnowledgeSearch struct {
	results []chatflow.SearchResultItem
	err     error
	calls   int
	queries []string
}

func (f *agentGraphFakeKnowledgeSearch) Search(_ context.Context, query string) ([]chatflow.SearchResultItem, error) {
	f.calls++
	f.queries = append(f.queries, query)
	return f.results, f.err
}

type agentGraphFakeAnswer struct {
	chunks []string
	err    error
	calls  int
	input  chatflow.AnswerInput
}

func (f *agentGraphFakeAnswer) Stream(_ context.Context, input chatflow.AnswerInput) (<-chan string, <-chan error) {
	f.calls++
	f.input = input
	chunks := make(chan string, len(f.chunks))
	errs := make(chan error, 1)
	for _, chunk := range f.chunks {
		chunks <- chunk
	}
	close(chunks)
	if f.err != nil {
		errs <- f.err
	}
	close(errs)
	return chunks, errs
}

type blockingDetailRunner struct{}

func (blockingDetailRunner) Run(context.Context, chatflow.DetailRequest) <-chan chatflow.Event {
	return make(chan chatflow.Event)
}

type agentGraphFakeDetailRunner struct {
	events []chatflow.Event
	calls  int
}

func (r *agentGraphFakeDetailRunner) Run(context.Context, chatflow.DetailRequest) <-chan chatflow.Event {
	r.calls++
	ch := make(chan chatflow.Event, len(r.events))
	for _, event := range r.events {
		ch <- event
	}
	close(ch)
	return ch
}

func collectGraphEvents(ch <-chan chatflow.Event) []chatflow.Event {
	var events []chatflow.Event
	for event := range ch {
		events = append(events, event)
	}
	return events
}

func agentGraphEventNames(events []chatflow.Event) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Name)
	}
	return out
}

func sameGraphStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasGraphStatus(events []chatflow.Event, stage, tool string) bool {
	for _, event := range events {
		if event.Name != chatflow.EventStatus {
			continue
		}
		payload, ok := event.Data.(map[string]string)
		if ok && payload["stage"] == stage && payload["tool"] == tool {
			return true
		}
	}
	return false
}

func graphDeltaText(events []chatflow.Event) string {
	var builder strings.Builder
	for _, event := range events {
		if event.Name != chatflow.EventDelta {
			continue
		}
		if payload, ok := event.Data.(map[string]string); ok {
			builder.WriteString(payload["text"])
		}
	}
	return builder.String()
}
