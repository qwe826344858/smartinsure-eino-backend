package chatflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	einoschema "github.com/cloudwego/eino/schema"
)

func TestGraphFlowNormalPathStreamsSameContract(t *testing.T) {
	price := "100元/年起"
	flow := New()
	flow.Intent = graphFakeIntent{result: IntentResult{Intent: "product_recommendation"}}
	flow.Search = graphFakeProductSearch{products: []ProductCard{{
		ID:         "p1",
		Name:       "测试医疗险",
		Price:      &price,
		PriceLabel: price,
		Platform:   "test",
	}}}
	flow.Fallback = graphFakeFallback{results: []SearchResultItem{{
		Title: "百万医疗险选购指南",
		URL:   "https://example.com/guide",
		Site:  "example.com",
	}}}
	flow.Answer = graphFakeAnswer{chunks: []string{"回答片段"}}

	graphFlow, err := NewGraphFlow(flow)
	if err != nil {
		t.Fatal(err)
	}

	events := collect(graphFlow.Run(context.Background(), Request{
		Message:   "百万医疗险怎么选？",
		RequestID: "rid-graph",
	}))

	wantOrder := []string{
		EventStatus,
		EventStatus,
		EventProducts,
		EventStatus,
		EventDelta,
		EventSources,
		EventDisclaimer,
		EventDone,
	}
	if got := eventNames(events); !sameStrings(got, wantOrder) {
		t.Fatalf("events = %#v, want %#v", got, wantOrder)
	}
}

func TestGraphFlowFollowupPathEndsEarly(t *testing.T) {
	flow := New()
	flow.Intent = graphFakeIntent{result: IntentResult{Intent: "product_recommendation", NeedsFollowup: true, MissingSlots: []string{"age", "budget"}}}
	flow.Followup = graphFakeFollowup{text: "请补充年龄。"}

	graphFlow, err := NewGraphFlow(flow)
	if err != nil {
		t.Fatal(err)
	}

	events := collect(graphFlow.Run(context.Background(), Request{Message: "给我推荐保险", RequestID: "rid-follow"}))
	if got := eventNames(events); !sameStrings(got, []string{EventStatus, EventDelta, EventDone}) {
		t.Fatalf("events = %#v", got)
	}
}

func TestGraphFlowDetailPathReusesDetailRunner(t *testing.T) {
	flow := New()
	flow.Detail = fakeDetailRunner{}

	graphFlow, err := NewGraphFlow(flow)
	if err != nil {
		t.Fatal(err)
	}

	events := collect(graphFlow.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: "https://example.com/product",
		RequestID:  "rid-detail",
	}))
	if got := eventNames(events); !sameStrings(got, []string{EventStatus, EventDetailItems, EventDelta, EventDone}) {
		t.Fatalf("events = %#v", got)
	}
}

func TestGraphFlowPassesHistoryToIntentAndAnswer(t *testing.T) {
	history := []ChatMessage{{ID: "m1", Role: "user", Content: "上一轮说预算 1500"}}
	intent := &historyCaptureIntent{result: IntentResult{Intent: "knowledge_explain"}}
	answer := &historyCaptureAnswer{chunks: []string{"ok"}}
	flow := New()
	flow.Intent = intent
	flow.Answer = answer

	graphFlow, err := NewGraphFlow(flow)
	if err != nil {
		t.Fatal(err)
	}

	events := collect(graphFlow.Run(context.Background(), Request{
		Message:   "继续",
		RequestID: "rid-graph-history",
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

func TestGraphFlowMainPathUsesChatModelNodes(t *testing.T) {
	flow := New()
	flow.Intent = graphFakeIntent{err: errors.New("legacy intent should not be called")}
	flow.Answer = graphFakeAnswer{err: errors.New("legacy answer should not be called")}
	flow.Fallback = graphFakeFallback{results: []SearchResultItem{{
		Title: "保险知识",
		URL:   "https://example.com/source",
		Site:  "example.com",
	}}}

	graphFlow, err := NewGraphFlow(flow, WithGraphChatModels(GraphChatModels{
		Intent: graphFakeChatModel{content: `{"intent":"knowledge_explain","needs_followup":false,"missing_slots":[],"reason":"test"}`},
		Followup: graphFakeChatModel{
			content: "请补充年龄。",
		},
		Answer: graphFakeChatModel{stream: []string{"第一段", "第二段"}},
	}))
	if err != nil {
		t.Fatal(err)
	}

	events := collect(graphFlow.Run(context.Background(), Request{
		Message:   "等待期是什么？",
		RequestID: "rid-chat-model-node",
	}))

	wantOrder := []string{
		EventStatus,
		EventStatus,
		EventStatus,
		EventDelta,
		EventDelta,
		EventSources,
		EventDisclaimer,
		EventDone,
	}
	if got := eventNames(events); !sameStrings(got, wantOrder) {
		t.Fatalf("events = %#v, want %#v", got, wantOrder)
	}
	if got := strings.Join(deltaTexts(events), ""); got != "第一段第二段" {
		t.Fatalf("delta text = %q", got)
	}
}

func TestNewProductionRunnerSwitchesByOrchestrator(t *testing.T) {
	t.Setenv("ORCHESTRATOR", OrchestratorLite)
	if _, ok := NewProductionRunner().(*Flow); !ok {
		t.Fatalf("lite orchestrator should return *Flow")
	}

	t.Setenv("ORCHESTRATOR", OrchestratorEinoGraph)
	if _, ok := NewProductionRunner().(*GraphFlow); !ok {
		t.Fatalf("eino_graph orchestrator should return *GraphFlow")
	}
}

type graphFakeIntent struct {
	result IntentResult
	err    error
}

func (f graphFakeIntent) Classify(context.Context, string) (IntentResult, error) {
	return f.result, f.err
}

type graphFakeProductSearch struct {
	products []ProductCard
	err      error
}

func (f graphFakeProductSearch) Search(context.Context, string) ([]ProductCard, error) {
	return f.products, f.err
}

type graphFakeFallback struct {
	results []SearchResultItem
	err     error
}

func (f graphFakeFallback) Search(context.Context, string) ([]SearchResultItem, error) {
	return f.results, f.err
}

type graphFakeAnswer struct {
	chunks []string
	err    error
}

func (f graphFakeAnswer) Stream(context.Context, AnswerInput) (<-chan string, <-chan error) {
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

type graphFakeFollowup struct {
	text string
	err  error
}

func (f graphFakeFollowup) Generate(context.Context, []string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.text, nil
}

type graphFakeChatModel struct {
	content string
	stream  []string
	err     error
}

func (m graphFakeChatModel) Generate(context.Context, []*einoschema.Message, ...einomodel.Option) (*einoschema.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	content := m.content
	if content == "" {
		content = strings.Join(m.stream, "")
	}
	return einoschema.AssistantMessage(content, nil), nil
}

func (m graphFakeChatModel) Stream(context.Context, []*einoschema.Message, ...einomodel.Option) (*einoschema.StreamReader[*einoschema.Message], error) {
	if m.err != nil {
		return nil, m.err
	}
	chunks := m.stream
	if len(chunks) == 0 {
		chunks = []string{m.content}
	}
	stream, writer := einoschema.Pipe[*einoschema.Message](len(chunks))
	go func() {
		defer writer.Close()
		for _, chunk := range chunks {
			writer.Send(einoschema.AssistantMessage(chunk, nil), nil)
		}
	}()
	return stream, nil
}

func TestGraphFlowIntentErrorReturnsErrorEvent(t *testing.T) {
	flow := New()
	flow.Intent = graphFakeIntent{err: errors.New("intent failed")}

	graphFlow, err := NewGraphFlow(flow)
	if err != nil {
		t.Fatal(err)
	}

	events := collect(graphFlow.Run(context.Background(), Request{Message: "百万医疗险", RequestID: "rid-error"}))
	if got := eventNames(events); !sameStrings(got, []string{EventStatus, EventError}) {
		t.Fatalf("events = %#v", got)
	}
}

func eventNames(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Name)
	}
	return out
}

func sameStrings(a, b []string) bool {
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

func deltaTexts(events []Event) []string {
	out := make([]string, 0)
	for _, event := range events {
		if event.Name != EventDelta {
			continue
		}
		if payload, ok := event.Data.(map[string]string); ok {
			out = append(out, payload["text"])
		}
	}
	return out
}
