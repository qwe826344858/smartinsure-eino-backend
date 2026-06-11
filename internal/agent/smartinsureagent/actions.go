package smartinsureagent

import (
	"fmt"
	"sort"
	"strings"

	"smartinsure-eino-backend/internal/agent/chatflow"
)

type AgentActionName string

const (
	ActionProductSearch   AgentActionName = "product_search"
	ActionKnowledgeSearch AgentActionName = "knowledge_search"
	ActionProductDetail   AgentActionName = "product_detail"
	ActionAskFollowup     AgentActionName = "ask_followup"
	ActionFinalAnswer     AgentActionName = "final_answer"
)

// allowedActions 是 Agent 的工具边界。模型 planner 只能在这些 action 中选择，
// 后端会在 validateDecision 中再次校验，避免开放任意工具调用。
var allowedActions = map[AgentActionName]struct{}{
	ActionProductSearch:   {},
	ActionKnowledgeSearch: {},
	ActionProductDetail:   {},
	ActionAskFollowup:     {},
	ActionFinalAnswer:     {},
}

type AgentDecision struct {
	Thought     string          `json:"thought"`
	Action      AgentActionName `json:"action"`
	ActionInput map[string]any  `json:"action_input"`
	Trusted     bool            `json:"-"`
}

type AgentAction struct {
	Name  AgentActionName
	Input map[string]any
}

func validateDecision(decision AgentDecision, req chatflow.Request) (AgentAction, error) {
	name := AgentActionName(strings.TrimSpace(string(decision.Action)))
	if _, ok := allowedActions[name]; !ok {
		return AgentAction{}, fmt.Errorf("unknown action: %s", decision.Action)
	}

	input := map[string]any{}
	for key, value := range decision.ActionInput {
		input[key] = value
	}

	switch name {
	case ActionProductSearch, ActionKnowledgeSearch:
		if err := validateActionInput(input, map[string]bool{"query": true}); err != nil {
			return AgentAction{}, err
		}
		if strings.TrimSpace(stringInput(input, "query")) == "" {
			input["query"] = req.Message
		}
	case ActionProductDetail:
		if err := validateActionInput(input, map[string]bool{"product_url": true, "product_name": true}); err != nil {
			return AgentAction{}, err
		}
		if strings.TrimSpace(stringInput(input, "product_url")) == "" {
			input["product_url"] = req.ProductURL
		}
		if strings.TrimSpace(stringInput(input, "product_name")) == "" {
			input["product_name"] = req.ProductName
		}
		if strings.TrimSpace(stringInput(input, "product_url")) == "" {
			return AgentAction{}, fmt.Errorf("product_detail requires product_url")
		}
	case ActionAskFollowup:
		if err := validateActionInput(input, map[string]bool{"question": true}); err != nil {
			return AgentAction{}, err
		}
		if strings.TrimSpace(stringInput(input, "question")) == "" {
			return AgentAction{}, fmt.Errorf("ask_followup requires question")
		}
	case ActionFinalAnswer:
		allowed := map[string]bool{"answer_brief": true}
		if decision.Trusted {
			allowed["answer_text"] = true
		}
		if err := validateActionInput(input, allowed); err != nil {
			return AgentAction{}, err
		}
		if strings.TrimSpace(stringInput(input, "answer_text")) == "" && strings.TrimSpace(stringInput(input, "answer_brief")) == "" {
			input["answer_brief"] = "基于当前对话和工具结果生成最终建议。"
		}
	}

	return AgentAction{Name: name, Input: input}, nil
}

// validateActionInput 按 action schema 收紧输入：
// 1. 不接受 schema 外字段；
// 2. 不接受数字、对象、数组和显式 null；
// 3. 只接受字符串，便于后续构建安全的 tool input。
func validateActionInput(input map[string]any, allowed map[string]bool) error {
	for key, value := range input {
		if !allowed[key] {
			return fmt.Errorf("unexpected action_input field: %s", key)
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("action_input.%s must be string", key)
		}
	}
	return nil
}

// actionFingerprint 用于识别“同一工具 + 同一核心输入”的重复调用。
// 例如同一个 product_search query 重复出现时，Graph 会跳过工具执行并让 reasoner 重新规划。
func actionFingerprint(action AgentAction) string {
	if action.Name == "" {
		return ""
	}
	switch action.Name {
	case ActionProductSearch, ActionKnowledgeSearch:
		return string(action.Name) + ":" + normalizeActionValue(stringInput(action.Input, "query"))
	case ActionProductDetail:
		return string(action.Name) + ":" + normalizeActionValue(stringInput(action.Input, "product_url"))
	case ActionAskFollowup, ActionFinalAnswer:
		return ""
	default:
		keys := make([]string, 0, len(action.Input))
		for key := range action.Input {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys)+1)
		parts = append(parts, string(action.Name))
		for _, key := range keys {
			parts = append(parts, key+"="+normalizeActionValue(stringInput(action.Input, key)))
		}
		return strings.Join(parts, ":")
	}
}

func normalizeActionValue(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func stringInput(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	value, ok := input[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}
