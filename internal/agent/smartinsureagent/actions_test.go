package smartinsureagent

import (
	"strings"
	"testing"

	"smartinsure-eino-backend/internal/agent/chatflow"
)

func TestValidateDecisionNormalizesSearchQuery(t *testing.T) {
	action, err := validateDecision(AgentDecision{
		Action: ActionKnowledgeSearch,
	}, chatflow.Request{Message: "等待期是什么？"})
	if err != nil {
		t.Fatal(err)
	}
	if got := stringInput(action.Input, "query"); got != "等待期是什么？" {
		t.Fatalf("query = %q", got)
	}
}

func TestValidateDecisionRequiresWhitelistedAction(t *testing.T) {
	_, err := validateDecision(AgentDecision{
		Action: AgentActionName("workflow_search"),
	}, chatflow.Request{Message: "百万医疗险"})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateDecisionRejectsUnexpectedOrNonStringInput(t *testing.T) {
	_, err := validateDecision(AgentDecision{
		Action: ActionProductSearch,
		ActionInput: map[string]any{
			"query": 123,
		},
	}, chatflow.Request{})
	if err == nil || !strings.Contains(err.Error(), "must be string") {
		t.Fatalf("err = %v", err)
	}

	_, err = validateDecision(AgentDecision{
		Action: ActionProductSearch,
		ActionInput: map[string]any{
			"query": nil,
		},
	}, chatflow.Request{})
	if err == nil || !strings.Contains(err.Error(), "must be string") {
		t.Fatalf("err = %v", err)
	}

	_, err = validateDecision(AgentDecision{
		Action: ActionKnowledgeSearch,
		ActionInput: map[string]any{
			"query": "等待期",
			"shell": "whoami",
		},
	}, chatflow.Request{})
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateDecisionProductDetailUsesRequestFallback(t *testing.T) {
	action, err := validateDecision(AgentDecision{
		Action: ActionProductDetail,
		ActionInput: map[string]any{
			"product_name": "",
		},
	}, chatflow.Request{ProductURL: "https://example.com/p", ProductName: "测试产品"})
	if err != nil {
		t.Fatal(err)
	}
	if stringInput(action.Input, "product_url") != "https://example.com/p" {
		t.Fatalf("product_url = %#v", action.Input)
	}
	if stringInput(action.Input, "product_name") != "测试产品" {
		t.Fatalf("product_name = %#v", action.Input)
	}
}

func TestValidateDecisionRejectsMissingRequiredFields(t *testing.T) {
	if _, err := validateDecision(AgentDecision{Action: ActionProductDetail}, chatflow.Request{}); err == nil {
		t.Fatal("expected product_detail error")
	}
	if _, err := validateDecision(AgentDecision{Action: ActionAskFollowup}, chatflow.Request{}); err == nil {
		t.Fatal("expected ask_followup error")
	}
}

func TestValidateDecisionFinalAnswerGetsDefaultBrief(t *testing.T) {
	action, err := validateDecision(AgentDecision{Action: ActionFinalAnswer}, chatflow.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if got := stringInput(action.Input, "answer_brief"); got == "" {
		t.Fatalf("answer_brief should be populated: %#v", action.Input)
	}
}

func TestValidateDecisionFinalAnswerRejectsUntrustedAnswerText(t *testing.T) {
	_, err := validateDecision(AgentDecision{
		Action:      ActionFinalAnswer,
		ActionInput: map[string]any{"answer_text": "直接输出"},
	}, chatflow.Request{})
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("err = %v", err)
	}

	action, err := validateDecision(AgentDecision{
		Action:      ActionFinalAnswer,
		ActionInput: map[string]any{"answer_text": "内部安全边界说明"},
		Trusted:     true,
	}, chatflow.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if got := stringInput(action.Input, "answer_text"); got != "内部安全边界说明" {
		t.Fatalf("answer_text = %q", got)
	}
}
