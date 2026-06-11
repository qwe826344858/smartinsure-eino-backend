package smartinsureagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/agent/chatflow"
	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/prompt"
)

const (
	defaultAgentProviderConfigPath = "configs/llm_providers.yaml"
	agentPlannerStage              = "agent_planner"
)

type Reasoner interface {
	Next(ctx context.Context, state *AgentState) (AgentDecision, error)
}

// heuristicReasoner 是无模型或模型失败时的保底规划器。
// 它不追求复杂推理，只保证 Agent 链路在本地和异常场景下仍能稳定完成：
// 意图识别 -> 必要追问 -> 产品/知识工具 -> 最终回答。
type heuristicReasoner struct {
	intent   chatflow.IntentClassifier
	followup chatflow.FollowupGenerator
}

func newHeuristicReasoner(intent chatflow.IntentClassifier, followup chatflow.FollowupGenerator) Reasoner {
	return heuristicReasoner{intent: intent, followup: followup}
}

func newProductionReasoner(flow *chatflow.Flow, settings config.Settings) Reasoner {
	fallback := newHeuristicReasoner(flow.Intent, flow.Followup)
	mode := strings.ToLower(strings.TrimSpace(settings.AgentMode))
	if mode == "deterministic_graph" || mode == "deterministic" || mode == "heuristic" {
		return fallback
	}

	registry, err := llm.LoadRegistry(defaultAgentProviderConfigPath, settings)
	if err != nil {
		return fallback
	}
	provider := registry.ForStage(agentPlannerStage)
	if provider.Key == "" || provider.Base == "" || provider.Model == "" {
		return fallback
	}
	return modelReasoner{
		model:               llm.NewClient(provider, time.Duration(settings.LLMTimeout)*time.Second, settings.LLMMaxRetries),
		fallback:            fallback,
		repairEnabled:       settings.AgentActionRepairEnabled,
		scratchpadMaxChars:  settings.AgentScratchpadMaxChars,
		observationMaxChars: settings.AgentObservationMaxChars,
	}
}

type modelReasoner struct {
	model               llm.ChatModel
	fallback            Reasoner
	repairEnabled       bool
	scratchpadMaxChars  int
	observationMaxChars int
}

// Next 调用 LLM planner 生成下一步 action JSON。
// 如果模型不可用、输出无法解析或 repair 失败，会回退 heuristicReasoner，
// 这样 Agent 模式不会因为 planner 模型异常而整体不可用。
func (r modelReasoner) Next(ctx context.Context, state *AgentState) (AgentDecision, error) {
	if r.model == nil {
		return r.fallback.Next(ctx, state)
	}

	messages := r.plannerMessages(state)
	raw, err := r.model.CallText(ctx, messages, 0.2)
	if err != nil {
		return r.fallback.Next(ctx, state)
	}
	decision, parseErr := parseAgentDecision(raw)
	if parseErr == nil {
		return decision, nil
	}

	if r.repairEnabled {
		messages = append(messages, llm.Message{
			Role:    llm.RoleUser,
			Content: fmt.Sprintf("上次输出不是合法 JSON 或不符合协议：%v。请只重新输出一个 JSON 对象，不要解释。", parseErr),
		})
		raw, err = r.model.CallText(ctx, messages, 0.1)
		if err == nil {
			if repaired, err := parseAgentDecision(raw); err == nil {
				return repaired, nil
			}
		}
	}

	return r.fallback.Next(ctx, state)
}

func (r modelReasoner) plannerMessages(state *AgentState) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleSystem, Content: plannerSystemPrompt()},
		{Role: llm.RoleUser, Content: plannerUserPrompt(state, r.scratchpadMaxChars, r.observationMaxChars)},
	}
}

func (r heuristicReasoner) Next(ctx context.Context, state *AgentState) (AgentDecision, error) {
	if state == nil {
		return AgentDecision{Action: ActionFinalAnswer, ActionInput: map[string]any{"answer_text": "当前请求状态异常，请稍后重试。"}, Trusted: true}, nil
	}
	if state.Intent.Intent == "" {
		intent, err := classifyWithHistory(ctx, r.intent, state)
		if err != nil {
			return AgentDecision{}, err
		}
		state.Intent = intent
	}

	if state.Intent.Intent == "out_of_scope" {
		return AgentDecision{
			Thought: "用户问题超出保险咨询范围，直接输出边界说明。",
			Action:  ActionFinalAnswer,
			ActionInput: map[string]any{
				"answer_text": "我目前专注于保险咨询。您如果想了解重疾险、医疗险、条款解读或产品对比，我可以继续帮您。",
			},
			Trusted: true,
		}, nil
	}

	if state.Intent.NeedsFollowup {
		return r.followupDecision(ctx, state, "用户关键信息不足，先追问再推荐。"), nil
	}

	if state.Intent.Intent == "product_recommendation" && shouldAskGenericRecommendationFollowup(state.Request.Message) {
		if len(state.Intent.MissingSlots) == 0 {
			state.Intent.MissingSlots = []string{"age", "budget", "preference"}
		}
		return r.followupDecision(ctx, state, "用户推荐需求过泛，先收集基础投保信息。"), nil
	}

	if productSearchIntents[state.Intent.Intent] && !state.hasAttemptedAction(ActionProductSearch) {
		return AgentDecision{
			Thought: "用户请求涉及产品选择，先搜索候选保险产品。",
			Action:  ActionProductSearch,
			ActionInput: map[string]any{
				"query": state.Request.Message,
			},
		}, nil
	}

	if !state.hasAttemptedAction(ActionKnowledgeSearch) {
		return AgentDecision{
			Thought: "需要补充保险知识或条款依据，执行知识检索。",
			Action:  ActionKnowledgeSearch,
			ActionInput: map[string]any{
				"query": state.Request.Message,
			},
		}, nil
	}

	return AgentDecision{
		Thought: "已有足够 observation，可以输出最终回答。",
		Action:  ActionFinalAnswer,
		ActionInput: map[string]any{
			"answer_brief": "基于当前对话、产品和知识来源生成最终建议。",
		},
	}, nil
}

func (r heuristicReasoner) followupDecision(ctx context.Context, state *AgentState, thought string) AgentDecision {
	question := ""
	if r.followup != nil {
		if text, err := r.followup.Generate(ctx, state.Intent.MissingSlots); err == nil {
			question = strings.TrimSpace(text)
		}
	}
	if question == "" {
		question = "为了给出更准确的建议，请补充保障对象、年龄、预算和已有保障情况。"
	}
	return AgentDecision{
		Thought:     thought,
		Action:      ActionAskFollowup,
		ActionInput: map[string]any{"question": question},
	}
}

func shouldAskGenericRecommendationFollowup(message string) bool {
	text := strings.TrimSpace(message)
	if text == "" {
		return false
	}
	if containsAny(text, []string{"重疾", "医疗", "百万", "意外", "寿险", "年金", "养老", "少儿", "车险", "家财", "惠民保", "防癌"}) {
		return false
	}
	if containsDigit(text) {
		return false
	}
	runes := []rune(text)
	if len(runes) > 18 {
		return false
	}
	return containsAny(text, []string{"推荐保险", "买保险", "保险推荐", "给我推荐", "推荐一下"})
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func containsDigit(text string) bool {
	for _, r := range text {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func classifyWithHistory(ctx context.Context, classifier chatflow.IntentClassifier, state *AgentState) (chatflow.IntentResult, error) {
	if classifierWithHistory, ok := classifier.(chatflow.HistoryIntentClassifier); ok {
		return classifierWithHistory.ClassifyWithHistory(ctx, state.Request.Message, state.History)
	}
	return classifier.Classify(ctx, state.Request.Message)
}

func parseAgentDecision(raw string) (AgentDecision, error) {
	content := llm.StripMarkdownFence(strings.TrimSpace(raw))
	if start := strings.Index(content, "{"); start >= 0 {
		if end := strings.LastIndex(content, "}"); end > start {
			content = content[start : end+1]
		}
	}
	var decision AgentDecision
	if err := json.Unmarshal([]byte(content), &decision); err != nil {
		return AgentDecision{}, err
	}
	if decision.ActionInput == nil {
		decision.ActionInput = map[string]any{}
	}
	return decision, nil
}

func plannerSystemPrompt() string {
	return prompt.System + `

【Plan-Act Agent 规划规则】
你是 SmartInsureAdvisorAgent 的 planner。你需要根据用户目标、历史上下文和工具 observation 决定下一步 action。
你只能输出一个 JSON 对象，不允许输出 Markdown、解释或自由文本。
action 只能是：product_search、knowledge_search、product_detail、ask_followup、final_answer。
不要调用或臆造任何未列出的工具。
不要重复调用 scratchpad 中已有的同一工具和同一输入；如果 observation 已足够或工具失败，应换工具或 final_answer。
thought 只用于后端 scratchpad，必须简短，不要包含敏感信息。
如果信息不足且会影响推荐，输出 ask_followup。
如果已有足够 observation，输出 final_answer，优先使用 answer_brief，不要在 planner 中生成长答案。`
}

func plannerUserPrompt(state *AgentState, scratchpadMaxChars, observationMaxChars int) string {
	if state == nil {
		return "请求状态为空。请输出 final_answer JSON。"
	}
	var builder strings.Builder
	builder.WriteString("用户问题:\n")
	builder.WriteString(strings.TrimSpace(state.Request.Message))
	builder.WriteString("\n\n近期对话:\n")
	builder.WriteString(formatPlannerHistory(state.History))
	builder.WriteString("\n\n当前已识别意图:\n")
	if state.Intent.Intent == "" {
		builder.WriteString("（未识别）")
	} else {
		builder.WriteString(state.Intent.Intent)
		if state.Intent.NeedsFollowup {
			builder.WriteString("，需要追问: ")
			builder.WriteString(strings.Join(state.Intent.MissingSlots, ","))
		}
	}
	builder.WriteString("\n\n可展示产品:\n")
	builder.WriteString(formatPlannerProducts(state.Products))
	builder.WriteString("\n\n知识来源:\n")
	builder.WriteString(formatPlannerSources(state.Sources))
	builder.WriteString("\n\nScratchpad observations:\n")
	builder.WriteString(formatPlannerScratchpad(state.Steps, observationMaxChars))
	builder.WriteString("\n\n请只输出如下 JSON 之一:\n")
	builder.WriteString(`{"thought":"简短规划理由","action":"product_search","action_input":{"query":"搜索词"}}`)
	builder.WriteString("\n")
	builder.WriteString(`{"thought":"简短规划理由","action":"knowledge_search","action_input":{"query":"搜索词"}}`)
	builder.WriteString("\n")
	builder.WriteString(`{"thought":"简短规划理由","action":"product_detail","action_input":{"product_url":"https://...","product_name":"产品名"}}`)
	builder.WriteString("\n")
	builder.WriteString(`{"thought":"简短规划理由","action":"ask_followup","action_input":{"question":"追问问题"}}`)
	builder.WriteString("\n")
	builder.WriteString(`{"thought":"简短规划理由","action":"final_answer","action_input":{"answer_brief":"回答重点"}}`)
	return limitText(builder.String(), scratchpadMaxChars)
}

func formatPlannerHistory(history []chatflow.ChatMessage) string {
	if len(history) == 0 {
		return "（无）"
	}
	lines := make([]string, 0, len(history))
	for _, item := range history {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		role := strings.TrimSpace(item.Role)
		if role == "" {
			role = "unknown"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", role, limitText(content, 300)))
	}
	if len(lines) == 0 {
		return "（无）"
	}
	return strings.Join(lines, "\n")
}

func formatPlannerProducts(products []chatflow.ProductCard) string {
	if len(products) == 0 {
		return "（无）"
	}
	lines := make([]string, 0, len(products))
	for i, product := range products {
		if i >= 5 {
			break
		}
		parts := []string{strings.TrimSpace(product.Name)}
		if product.Company != "" {
			parts = append(parts, "公司: "+product.Company)
		}
		if product.PriceLabel != "" {
			parts = append(parts, "价格: "+product.PriceLabel)
		}
		if product.Brief != "" {
			parts = append(parts, "简介: "+limitText(product.Brief, 160))
		}
		lines = append(lines, "- "+strings.Join(compactNonEmpty(parts), "；"))
	}
	if len(lines) == 0 {
		return "（无）"
	}
	return strings.Join(lines, "\n")
}

func formatPlannerSources(sources []chatflow.SourceItem) string {
	if len(sources) == 0 {
		return "（无）"
	}
	lines := make([]string, 0, len(sources))
	for i, source := range sources {
		if i >= 5 {
			break
		}
		title := strings.TrimSpace(source.Title)
		if title == "" {
			title = source.URL
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)", title, source.Site))
	}
	return strings.Join(lines, "\n")
}

func formatPlannerScratchpad(steps []AgentStep, observationMaxChars int) string {
	if len(steps) == 0 {
		return "（无）"
	}
	lines := make([]string, 0, len(steps))
	for i, step := range steps {
		actionName := string(step.Action.Name)
		if actionName == "" {
			actionName = "invalid_action"
		}
		observation := step.Observation.Summary
		if len(step.Observation.Data) > 0 {
			if payload, err := json.Marshal(step.Observation.Data); err == nil {
				observation = strings.TrimSpace(observation + " " + string(payload))
			}
		}
		if step.Err != "" {
			observation = strings.TrimSpace(observation + " error=" + step.Err)
		}
		lines = append(lines, fmt.Sprintf("%d. action=%s observation=%s", i+1, actionName, limitText(observation, observationMaxChars)))
	}
	return strings.Join(lines, "\n")
}

func compactNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func limitText(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if maxChars <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars]) + "..."
}
