package smartinsureagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"smartinsure-eino-backend/internal/agent/chatflow"
	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/llm"
)

func TestParseAgentDecisionExtractsJSON(t *testing.T) {
	decision, err := parseAgentDecision("```json\n{\"thought\":\"ok\",\"action\":\"knowledge_search\",\"action_input\":{\"query\":\"等待期\"}}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionKnowledgeSearch || stringInput(decision.ActionInput, "query") != "等待期" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestModelReasonerUsesPlannerJSON(t *testing.T) {
	model := &fakePlannerModel{responses: []string{`{"thought":"need products","action":"product_search","action_input":{"query":"百万医疗险"}}`}}
	reasoner := modelReasoner{
		model:               model,
		fallback:            &scriptedReasoner{},
		repairEnabled:       true,
		scratchpadMaxChars:  2000,
		observationMaxChars: 300,
	}

	decision, err := reasoner.Next(context.Background(), &AgentState{Request: chatflow.Request{Message: "百万医疗险怎么选？"}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionProductSearch || stringInput(decision.ActionInput, "query") != "百万医疗险" {
		t.Fatalf("decision = %#v", decision)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d", model.calls)
	}
}

func TestModelReasonerRepairsInvalidJSONOnce(t *testing.T) {
	model := &fakePlannerModel{responses: []string{
		"not-json",
		`{"thought":"fixed","action":"final_answer","action_input":{"answer_brief":"可以回答"}}`,
	}}
	reasoner := modelReasoner{
		model:               model,
		fallback:            &scriptedReasoner{},
		repairEnabled:       true,
		scratchpadMaxChars:  2000,
		observationMaxChars: 300,
	}

	decision, err := reasoner.Next(context.Background(), &AgentState{Request: chatflow.Request{Message: "等待期是什么？"}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionFinalAnswer {
		t.Fatalf("decision = %#v", decision)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d", model.calls)
	}
	if !strings.Contains(model.lastMessages[len(model.lastMessages)-1].Content, "不是合法 JSON") {
		t.Fatalf("repair prompt missing: %#v", model.lastMessages)
	}
}

func TestModelReasonerFallsBackWhenModelFails(t *testing.T) {
	fallback := &scriptedReasoner{decisions: []AgentDecision{{Action: ActionKnowledgeSearch, ActionInput: map[string]any{"query": "fallback"}}}}
	reasoner := modelReasoner{
		model:    &fakePlannerModel{err: errors.New("model unavailable")},
		fallback: fallback,
	}

	decision, err := reasoner.Next(context.Background(), &AgentState{Request: chatflow.Request{Message: "等待期是什么？"}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionKnowledgeSearch || stringInput(decision.ActionInput, "query") != "fallback" {
		t.Fatalf("decision = %#v", decision)
	}
	if fallback.calls != 1 {
		t.Fatalf("fallback calls = %d", fallback.calls)
	}
}

func TestNewProductionReasonerRespectsDeterministicMode(t *testing.T) {
	reasoner := newProductionReasoner(chatflow.New(), config.Settings{AgentMode: "deterministic_graph"})
	if _, ok := reasoner.(heuristicReasoner); !ok {
		t.Fatalf("reasoner type = %T", reasoner)
	}
}

type fakePlannerModel struct {
	responses    []string
	err          error
	calls        int
	lastMessages []llm.Message
}

func (m *fakePlannerModel) CallText(_ context.Context, messages []llm.Message, _ float64) (string, error) {
	m.calls++
	m.lastMessages = messages
	if m.err != nil {
		return "", m.err
	}
	if len(m.responses) == 0 {
		return "", nil
	}
	index := m.calls - 1
	if index >= len(m.responses) {
		index = len(m.responses) - 1
	}
	return m.responses[index], nil
}

func (m *fakePlannerModel) CallJSON(ctx context.Context, messages []llm.Message, temperature float64, out any) error {
	text, err := m.CallText(ctx, messages, temperature)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(text), out)
}

func (m *fakePlannerModel) StreamText(context.Context, []llm.Message, float64) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}
