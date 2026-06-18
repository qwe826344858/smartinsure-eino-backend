package smartinsuredeep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"smartinsure-eino-backend/internal/agent/chatflow"
	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/llm"
)

const (
	// DefaultID 是 DeepAgent 在 AgentRuntime 注册表中的固定 ID。
	DefaultID                 = "smartinsure-deep-advisor"
	RAGAgentID                = "smartinsure-rag-advisor"
	defaultProviderConfigPath = "configs/llm_providers.yaml"
	defaultMaxIterations      = 10
	defaultFinalAnswerTimeout = 30 * time.Second
	// deepAgentStage 对应 configs/llm_providers.yaml 中的 deep_agent 阶段配置。
	deepAgentStage = "deep_agent"
)

// adkRunner 抽象 Eino ADK Runner，便于单测用 fake runner 驱动 DeepAgent 事件。
type adkRunner interface {
	Run(ctx context.Context, messages []adk.Message, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent]
}

// Agent 是 DeepAgent 的 AgentRuntime 适配器，负责把 Eino ADK 事件转换为项目统一 SSE 事件。
type Agent struct {
	id          string
	runner      adkRunner
	finalModel  einomodel.BaseChatModel
	instruction string
	// flow 保存确定性的 chatflow/detail runner。
	// 前端按钮触发的产品详情请求不能依赖 LLM 重新复述 URL，否则可能出现链接被模型抄错或工具超时。
	flow                *chatflow.Flow
	finalTimeout        time.Duration
	toolTimeout         time.Duration
	directDetailEnabled bool
}

type productionConfig struct {
	id                  string
	instruction         string
	toolFactory         func(*chatflow.Flow, time.Duration) (adk.ToolsConfig, error)
	directDetailEnabled bool
}

// NewProduction 组装生产 DeepAgent：读取配置、加载 deep_agent 模型、注册工具、创建 ADK runner。
func NewProduction() (*Agent, error) {
	return newProduction(productionConfig{
		id:                  DefaultID,
		instruction:         deepInstruction,
		toolFactory:         newTools,
		directDetailEnabled: true,
	})
}

// NewRAGProduction 组装专用于 /chat/rag-agent 的 RAG DeepAgent。
// 该 Agent 只暴露 knowledge_search，强制模型基于已入库 RAG 数据做产品匹配。
func NewRAGProduction() (*Agent, error) {
	return newProduction(productionConfig{
		id:                  RAGAgentID,
		instruction:         ragAgentInstruction,
		toolFactory:         newRAGTools,
		directDetailEnabled: false,
	})
}

func newProduction(cfg productionConfig) (*Agent, error) {
	if cfg.id == "" {
		cfg.id = DefaultID
	}
	if strings.TrimSpace(cfg.instruction) == "" {
		cfg.instruction = deepInstruction
	}
	if cfg.toolFactory == nil {
		cfg.toolFactory = newTools
	}
	// 1. 读取全局配置和 LLM provider 路由。
	settings := config.Load()
	registry, err := llm.LoadRegistry(defaultProviderConfigPath, settings)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	// 2. 按 deep_agent 阶段创建 Eino ChatModel。
	model, provider, err := registry.EinoChatModelForStage(ctx, deepAgentStage)
	if err != nil {
		return nil, err
	}
	if provider.Key == "" || provider.Base == "" || provider.Model == "" {
		return nil, errors.New("deep agent llm provider is not configured")
	}

	// 3. 复用生产 chatflow 的搜索、知识检索、产品详情等底层能力生成 DeepAgent tools。
	flow := chatflow.NewProduction()
	toolTimeout := time.Duration(settings.AgentToolTimeout) * time.Second
	if toolTimeout <= 0 {
		toolTimeout = 15 * time.Second
	}
	tools, err := cfg.toolFactory(flow, toolTimeout)
	if err != nil {
		return nil, err
	}
	maxIterations := settings.AgentMaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations
	}
	finalTimeout := time.Duration(settings.LLMTimeout) * time.Second
	if finalTimeout <= 0 {
		finalTimeout = defaultFinalAnswerTimeout
	}
	// 4. 创建 Eino prebuilt DeepAgent，并关闭通用子 Agent，保持工具集合可控。
	deepAgent, err := deep.New(ctx, &deep.Config{
		Name:                   cfg.id,
		Description:            "SmartInsure insurance advisor powered by Eino DeepAgent.",
		ChatModel:              model,
		Instruction:            cfg.instruction,
		ToolsConfig:            tools,
		MaxIteration:           maxIterations,
		WithoutGeneralSubAgent: true,
	})
	if err != nil {
		return nil, err
	}
	logx.Printf("运行日志", "runtime log", "deep_agent init_success id=%s model=%s tool_timeout_seconds=%d max_iterations=%d", cfg.id, provider.Model, int(toolTimeout.Seconds()), maxIterations)
	// 5. 返回 AgentRuntime 可调用的 Agent 实例。
	return &Agent{
		id: cfg.id,
		runner: adk.NewRunner(ctx, adk.RunnerConfig{
			Agent:           deepAgent,
			EnableStreaming: true,
		}),
		finalModel:          model,
		instruction:         cfg.instruction,
		flow:                flow,
		finalTimeout:        finalTimeout,
		toolTimeout:         toolTimeout,
		directDetailEnabled: cfg.directDetailEnabled,
	}, nil
}

// ID 返回 AgentRuntime 用于路由的 Agent ID。
func (a *Agent) ID() string {
	if a == nil || a.id == "" {
		return DefaultID
	}
	return a.id
}

// Run 是 DeepAgent 的请求入口：接收 Runtime 请求，输出统一 AgentEvent 流。
func (a *Agent) Run(ctx context.Context, req agentruntime.AgentRequest) <-chan agentruntime.AgentEvent {
	out := make(chan agentruntime.AgentEvent)
	go func() {
		defer close(out)
		// AgentID 为空时补齐默认 ID，确保后续 SSE 事件能带上 agent_id。
		if req.AgentID == "" {
			req.AgentID = a.ID()
		}
		// 开启 trace 时为本次请求生成 trace_id，前端和日志可以用它串联完整链路。
		traceID := ""
		if !req.TraceDisabled {
			traceID = agentruntime.NewTraceID()
		}
		startedAt := time.Now()
		logx.Printf("运行日志", "runtime log", "deep_agent request_start request_id=%s action=%s history=%d trace_id=%s", req.RequestID, req.Action, len(req.History), traceID)
		defer func() {
			logx.Printf("运行日志", "runtime log", "deep_agent request_end request_id=%s action=%s duration_ms=%d", req.RequestID, req.Action, time.Since(startedAt).Milliseconds())
		}()
		if a.directDetailEnabled && isDirectDetailAction(req.Action) {
			logx.Printf("运行日志", "runtime log", "deep_agent direct_detail_route request_id=%s action=%s product_url_present=%t product_name_present=%t", req.RequestID, req.Action, strings.TrimSpace(req.ProductURL) != "", strings.TrimSpace(req.ProductName) != "")
			// 直达详情 action 来自明确的 UI/API 参数，包含原始 productUrl。
			// 这里直接转发给 chatflow，避免让模型重新生成工具入参，也避开 DeepAgent 通用工具超时。
			if a == nil || a.flow == nil {
				emitRuntimeEvent(out, chatflow.EventError, map[string]string{
					"code":      "INTERNAL_ERROR",
					"message":   "product detail runner is not initialized",
					"requestId": req.RequestID,
				}, req, traceID)
				emitRuntimeEvent(out, chatflow.EventDone, map[string]string{"requestId": req.RequestID}, req, traceID)
				return
			}
			a.runDirectDetail(ctx, out, req, traceID)
			return
		}
		// 普通对话必须有 ADK runner；runner 缺失说明 DeepAgent 初始化失败。
		if a == nil || a.runner == nil {
			emitRuntimeEvent(out, chatflow.EventError, map[string]string{
				"code":      "INTERNAL_ERROR",
				"message":   "deep agent runner is not initialized",
				"requestId": req.RequestID,
			}, req, traceID)
			emitRuntimeEvent(out, chatflow.EventDone, map[string]string{"requestId": req.RequestID}, req, traceID)
			return
		}

		// 先向前端输出 reasoning 状态，表示 DeepAgent 即将进入规划/工具选择阶段。
		emitRuntimeEvent(out, chatflow.EventStatus, map[string]string{
			"stage":   "reasoning",
			"message": "DeepAgent 正在规划下一步...",
		}, req, traceID)

		// 将历史消息和当前请求转换为 ADK messages，再把 session values 注入 ADK 上下文。
		state := &deepRunState{}
		iter := a.runner.Run(ctx, toADKMessages(req), adk.WithSessionValues(sessionValues(req)))
		for {
			event, ok := iter.Next()
			if !ok {
				break
			}
			// 每个 ADK event 都要映射成项目统一 SSE 事件。
			if shouldStop := a.emitADKEvent(ctx, out, event, req, traceID, state); shouldStop {
				break
			}
		}
		a.flushThinkFilter(out, req, traceID, state)
		a.emitRAGGuardSearchIfNeeded(ctx, out, req, traceID, state)
		if state.maxIterationsExceeded {
			a.emitFinalAnswerAfterMaxIterations(ctx, out, req, traceID, state)
		}
		// DeepAgent 正常结束后统一补免责声明和 done 事件，保持前端协议稳定。
		emitRuntimeEvent(out, chatflow.EventDisclaimer, map[string]string{"text": chatflowDisclaimerText()}, req, traceID)
		emitRuntimeEvent(out, chatflow.EventDone, map[string]string{"requestId": req.RequestID}, req, traceID)
	}()
	return out
}

// runDirectDetail 将 product_detail/product_followup 直接交给 chatflow。
// 这个分支不经过 DeepAgent 规划，专门保证详情按钮的 URL 和 SSE 行为稳定。
func (a *Agent) runDirectDetail(ctx context.Context, out chan<- agentruntime.AgentEvent, req agentruntime.AgentRequest, traceID string) {
	detailEvents := a.flow.Run(ctx, chatflow.Request{
		Message:       req.Message,
		RequestID:     req.RequestID,
		Action:        req.Action,
		ProductURL:    req.ProductURL,
		ProductName:   req.ProductName,
		AnonymousID:   req.AnonymousID,
		UserID:        req.UserID,
		ChatSessionID: req.ChatSessionID,
		Metadata:      req.Metadata,
	})
	for detailEvents != nil {
		select {
		case <-ctx.Done():
			// 客户端断开或上游取消时，补 error/done 后结束详情流。
			emitRuntimeEvent(out, chatflow.EventError, map[string]string{
				"code":      "INTERNAL_ERROR",
				"message":   ctx.Err().Error(),
				"requestId": req.RequestID,
			}, req, traceID)
			emitRuntimeEvent(out, chatflow.EventDone, map[string]string{"requestId": req.RequestID}, req, traceID)
			return
		case event, ok := <-detailEvents:
			if !ok {
				detailEvents = nil
				continue
			}
			if event.Name == "" {
				continue
			}
			// 保持 chatflow 原始事件名不变，只补 request_id/agent_id/trace_id。
			emitRuntimeEvent(out, event.Name, event.Data, req, traceID)
		}
	}
}

// emitADKEvent 处理 Eino ADK 的单个事件，并把其中的 action/output 映射为 SSE。
// 返回 true 表示当前 ADK 循环需要停止，例如已达到最大迭代次数或出现不可恢复错误。
func (a *Agent) emitADKEvent(ctx context.Context, out chan<- agentruntime.AgentEvent, event *adk.AgentEvent, req agentruntime.AgentRequest, traceID string, state *deepRunState) bool {
	if event == nil {
		return false
	}
	if event.Err != nil {
		if isMaxIterationsError(event.Err) {
			if state != nil {
				state.maxIterationsExceeded = true
			}
			logx.Printf("超过最大迭代限制", "max iterations exceeded", "deep_agent max_iterations_exceeded request_id=%s err=%v", req.RequestID, event.Err)
			emitRuntimeEvent(out, chatflow.EventStatus, map[string]string{
				"stage":   "finalizing",
				"message": "DeepAgent 正在基于已获得的信息生成最终结论...",
			}, req, traceID)
			return true
		}
		// ADK 自身错误直接转成 error event。
		logx.Printf("异常信息", "error", "deep_agent event_error request_id=%s err=%v", req.RequestID, event.Err)
		emitRuntimeEvent(out, chatflow.EventError, map[string]string{
			"code":      "INTERNAL_ERROR",
			"message":   event.Err.Error(),
			"requestId": req.RequestID,
		}, req, traceID)
		return true
	}
	if event.Action != nil {
		// action 存在表示 DeepAgent 已决定调用工具或内部动作，向前端输出状态即可，不暴露内部推理。
		logx.Printf("运行日志", "runtime log", "deep_agent action_event request_id=%s action_present=true", req.RequestID)
		emitRuntimeEvent(out, chatflow.EventStatus, map[string]string{
			"stage":   "agent_action",
			"message": "DeepAgent 已产生内部动作。",
		}, req, traceID)
	}
	if event.Output == nil || event.Output.MessageOutput == nil {
		return false
	}
	output := event.Output.MessageOutput
	if output.IsStreaming && output.MessageStream != nil {
		defer output.MessageStream.Close()
		for {
			// 流式输出可能包含 assistant delta，也可能包含 tool message。
			msg, err := output.MessageStream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return false
				}
				// 非 EOF 错误说明模型流或工具流异常，转成前端可见 error。
				logx.Printf("异常信息", "error", "deep_agent stream_error request_id=%s err=%v", req.RequestID, err)
				emitRuntimeEvent(out, chatflow.EventError, map[string]string{
					"code":      "INTERNAL_ERROR",
					"message":   err.Error(),
					"requestId": req.RequestID,
				}, req, traceID)
				return true
			}
			a.emitADKMessage(out, output.Role, output.ToolName, msg, req, traceID, state)
			select {
			case <-ctx.Done():
				// 每次发送后检查 context，避免客户端断开后 goroutine 继续阻塞。
				logx.Printf("异常信息", "error", "deep_agent context_done request_id=%s err=%v", req.RequestID, ctx.Err())
				emitRuntimeEvent(out, chatflow.EventError, map[string]string{
					"code":      "INTERNAL_ERROR",
					"message":   ctx.Err().Error(),
					"requestId": req.RequestID,
				}, req, traceID)
				return true
			default:
			}
		}
	}
	// 非流式输出也走同一套 message 映射逻辑。
	a.emitADKMessage(out, output.Role, output.ToolName, output.Message, req, traceID, state)
	return false
}

// emitADKMessage 按 ADK message role 区分工具结果、助手回答和其他消息。
func (a *Agent) emitADKMessage(out chan<- agentruntime.AgentEvent, role schema.RoleType, toolName string, msg *schema.Message, req agentruntime.AgentRequest, traceID string, state *deepRunState) {
	if msg == nil {
		return
	}
	// 部分 ADK 输出会把 toolName/role 放在外层 output，message 内为空时用外层值兜底。
	if toolName == "" {
		toolName = msg.ToolName
	}
	if role == "" {
		role = msg.Role
	}
	switch role {
	case schema.Tool:
		// 工具结果一般是 JSON，需要进一步解析成 products/sources/detail_items 等 SSE 事件。
		logx.Printf("运行日志", "runtime log", "deep_agent tool_message request_id=%s tool=%s content_chars=%d", req.RequestID, toolName, len([]rune(strings.TrimSpace(msg.Content))))
		if state != nil {
			state.recordTool(toolName, msg.Content)
		}
		emitToolResult(out, toolName, msg.Content, req, traceID)
	case schema.Assistant:
		if len(msg.ToolCalls) > 0 {
			// Assistant 携带 ToolCalls 表示模型正在选择工具，向前端输出 tool_planning 状态。
			logx.Printf("运行日志", "runtime log", "deep_agent assistant_tool_calls request_id=%s count=%d", req.RequestID, len(msg.ToolCalls))
			emitRuntimeEvent(out, chatflow.EventStatus, map[string]string{
				"stage":   "tool_planning",
				"message": "DeepAgent 正在选择保险工具...",
			}, req, traceID)
		}
		if msg.Content != "" {
			// Assistant 普通文本需要先剥离 <think>，避免内部推理进入前端回答。
			a.emitAssistantContent(out, "assistant", msg.Content, req, traceID, state)
		}
	default:
		if msg.Content != "" {
			// 非标准 role 的文本也以 delta 输出，但同样不能把 <think> 透给前端。
			a.emitAssistantContent(out, string(role), msg.Content, req, traceID, state)
		}
	}
}

func (a *Agent) emitRAGGuardSearchIfNeeded(ctx context.Context, out chan<- agentruntime.AgentEvent, req agentruntime.AgentRequest, traceID string, state *deepRunState) {
	if a == nil || a.ID() != RAGAgentID {
		return
	}
	if state != nil && state.knowledgeSearchUsed {
		return
	}
	query := buildRAGGuardQuery(req)
	if query == "" {
		logx.Printf("运行日志", "runtime log", "rag_agent guard_search_skip request_id=%s reason=empty_query", req.RequestID)
		return
	}
	if a.flow == nil || a.flow.Fallback == nil {
		logx.Printf("运行日志", "runtime log", "rag_agent guard_search_skip request_id=%s reason=searcher_unavailable query_chars=%d", req.RequestID, len([]rune(query)))
		return
	}
	timeout := a.toolTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	logx.Printf("运行日志", "runtime log", "rag_agent guard_search_start request_id=%s query_chars=%d timeout_seconds=%d", req.RequestID, len([]rune(query)), int(timeout.Seconds()))
	emitRuntimeEvent(out, chatflow.EventStatus, map[string]string{
		"stage":   "rag_guard_search",
		"message": "RAG Agent 正在补充检索当前问题...",
	}, req, traceID)

	searchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	results, err := a.flow.Fallback.Search(searchCtx, query)
	if err != nil {
		logx.Printf("异常信息", "error", "rag_agent guard_search_failed request_id=%s duration_ms=%d err=%v", req.RequestID, time.Since(startedAt).Milliseconds(), err)
		return
	}
	payload := knowledgeSearchOutput{
		Summary:  "RAG Agent 已对当前问题执行兜底检索。",
		Results:  results,
		Products: productCardsFromKnowledgeResults(searchCtx, results, a.flow.Prices),
		Sources:  uniqueSources(results),
	}
	content, err := json.Marshal(payload)
	if err != nil {
		logx.Printf("异常信息", "error", "rag_agent guard_search_marshal_failed request_id=%s results=%d err=%v", req.RequestID, len(results), err)
		return
	}
	if state != nil {
		state.recordTool(toolKnowledgeSearch, string(content))
	}
	emitToolResult(out, toolKnowledgeSearch, string(content), req, traceID)
	logx.Printf("运行日志", "runtime log", "rag_agent guard_search_success request_id=%s results=%d products=%d sources=%d duration_ms=%d", req.RequestID, len(results), len(payload.Products), len(payload.Sources), time.Since(startedAt).Milliseconds())
}

func buildRAGGuardQuery(req agentruntime.AgentRequest) string {
	query := strings.TrimSpace(buildUserQuery(req))
	if query == "" {
		query = strings.TrimSpace(req.Message)
	}
	if query == "" {
		return ""
	}
	if needsHistoryForRAGQuery(query) {
		if history := recentHistoryForRAGQuery(req.History, 2); history != "" {
			query += "\n历史补全：" + history
		}
	}
	query = expandRAGQueryAliases(query)
	return limitRunes(query, 800)
}

func needsHistoryForRAGQuery(query string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return false
	}
	phrases := []string{
		"这款", "这个", "这类", "这些", "它", "它们", "该产品", "该险",
		"上面", "刚才", "之前", "继续", "再推荐", "对比一下", "哪个更",
	}
	for _, phrase := range phrases {
		if strings.Contains(query, phrase) {
			return true
		}
	}
	return utf8.RuneCountInString(query) <= 8
}

func recentHistoryForRAGQuery(history []agentruntime.ChatMessage, maxItems int) string {
	if maxItems <= 0 || len(history) == 0 {
		return ""
	}
	parts := make([]string, 0, maxItems)
	for i := len(history) - 1; i >= 0 && len(parts) < maxItems; i-- {
		role := strings.TrimSpace(history[i].Role)
		content := strings.TrimSpace(history[i].Content)
		if content == "" {
			continue
		}
		parts = append(parts, role+"："+limitRunes(content, 160))
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "\n")
}

func expandRAGQueryAliases(query string) string {
	expanded := query
	lower := strings.ToLower(query)
	appendOnce := func(text string) {
		if !strings.Contains(expanded, text) {
			expanded += " " + text
		}
	}
	if strings.Contains(query, "慧泽") || strings.Contains(query, "慧择") || strings.Contains(lower, "huize") {
		appendOnce("慧择")
		appendOnce("huize")
		appendOnce("慧择平台")
	}
	if strings.Contains(query, "小雨伞") || strings.Contains(lower, "xiaoyusan") {
		appendOnce("小雨伞")
		appendOnce("xiaoyusan")
		appendOnce("小雨伞平台")
	}
	if strings.Contains(query, "外购药") || strings.Contains(query, "院外药") || strings.Contains(query, "特药") {
		appendOnce("外购药")
		appendOnce("院外药")
		appendOnce("特药")
	}
	if strings.Contains(query, "0免赔") || strings.Contains(query, "无免赔") || strings.Contains(query, "低免赔") {
		appendOnce("0免赔")
		appendOnce("无免赔")
		appendOnce("低免赔")
	}
	if strings.Contains(query, "家庭版") || strings.Contains(query, "多人投保") || strings.Contains(query, "家庭单") {
		appendOnce("家庭版")
		appendOnce("多人投保")
		appendOnce("家庭单")
	}
	return expanded
}

type deepRunState struct {
	maxIterationsExceeded bool
	knowledgeSearchUsed   bool
	assistantText         strings.Builder
	toolObservations      []string
	thinkFilter           *deepThinkStreamFilter
}

func (s *deepRunState) filterAssistant(text string) (string, []string) {
	if s == nil {
		filter := newDeepThinkStreamFilter()
		visible, thinks := filter.feed(text)
		tail, tailThinks := filter.flush()
		return visible + tail, append(thinks, tailThinks...)
	}
	if s.thinkFilter == nil {
		s.thinkFilter = newDeepThinkStreamFilter()
	}
	return s.thinkFilter.feed(text)
}

func (s *deepRunState) flushAssistantFilter() (string, []string) {
	if s == nil || s.thinkFilter == nil {
		return "", nil
	}
	return s.thinkFilter.flush()
}

func (s *deepRunState) recordAssistant(text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.assistantText.WriteString(text)
}

func (s *deepRunState) recordTool(toolName string, content string) {
	if s == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(toolName), toolKnowledgeSearch) {
		s.knowledgeSearchUsed = true
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	if toolName == "" {
		toolName = "unknown"
	}
	s.toolObservations = append(s.toolObservations, fmt.Sprintf("工具：%s\n结果：%s", toolName, limitRunes(content, 1800)))
	if len(s.toolObservations) > 8 {
		s.toolObservations = s.toolObservations[len(s.toolObservations)-8:]
	}
}

func (s *deepRunState) assistantSummary() string {
	if s == nil {
		return ""
	}
	return limitRunes(s.assistantText.String(), 3000)
}

func (s *deepRunState) toolContext() string {
	if s == nil || len(s.toolObservations) == 0 {
		return "（无工具结果）"
	}
	return limitRunes(strings.Join(s.toolObservations, "\n\n"), 7000)
}

func (a *Agent) emitAssistantContent(out chan<- agentruntime.AgentEvent, source string, content string, req agentruntime.AgentRequest, traceID string, state *deepRunState) {
	visible, thinks := state.filterAssistant(content)
	a.emitThinkDiagnostics(out, thinks, req, traceID)
	if strings.TrimSpace(visible) == "" {
		return
	}
	logx.Printf("运行日志", "runtime log", "deep_agent assistant_delta request_id=%s source=%s chars=%d", req.RequestID, source, len([]rune(visible)))
	if state != nil {
		state.recordAssistant(visible)
	}
	emitRuntimeEvent(out, chatflow.EventDelta, map[string]string{"text": visible}, req, traceID)
}

func (a *Agent) flushThinkFilter(out chan<- agentruntime.AgentEvent, req agentruntime.AgentRequest, traceID string, state *deepRunState) {
	visible, thinks := state.flushAssistantFilter()
	a.emitThinkDiagnostics(out, thinks, req, traceID)
	if strings.TrimSpace(visible) == "" {
		return
	}
	logx.Printf("运行日志", "runtime log", "deep_agent assistant_delta request_id=%s source=think_filter_flush chars=%d", req.RequestID, len([]rune(visible)))
	if state != nil {
		state.recordAssistant(visible)
	}
	emitRuntimeEvent(out, chatflow.EventDelta, map[string]string{"text": visible}, req, traceID)
}

func (a *Agent) emitThinkDiagnostics(out chan<- agentruntime.AgentEvent, thinks []string, req agentruntime.AgentRequest, traceID string) {
	for _, think := range thinks {
		think = strings.TrimSpace(think)
		if think == "" {
			continue
		}
		limited := limitRunes(think, 1200)
		logx.Printf("运行日志", "runtime log", "deep_agent think request_id=%s agent_id=%s trace_id=%s chars=%d content=%q", req.RequestID, req.AgentID, traceID, len([]rune(think)), limited)
		if !req.IncludeThink {
			continue
		}
		emitRuntimeEvent(out, chatflow.EventStatus, map[string]string{
			"stage":   "thinking",
			"message": "DeepAgent think 输出已记录。",
			"think":   limited,
		}, req, traceID)
	}
}

type deepThinkStreamFilter struct {
	inThink bool
	pending string
	think   strings.Builder
}

func newDeepThinkStreamFilter() *deepThinkStreamFilter {
	return &deepThinkStreamFilter{}
}

func (f *deepThinkStreamFilter) feed(text string) (string, []string) {
	if f == nil || text == "" {
		return text, nil
	}
	input := f.pending + text
	f.pending = ""
	var visible strings.Builder
	var thinks []string
	for input != "" {
		if f.inThink {
			end := strings.Index(input, "</think>")
			if end >= 0 {
				f.think.WriteString(input[:end])
				if think := strings.TrimSpace(f.think.String()); think != "" {
					thinks = append(thinks, think)
				}
				f.think.Reset()
				f.inThink = false
				input = input[end+len("</think>"):]
				continue
			}
			keep := tagPrefixSuffixLen(input, "</think>")
			if keep > 0 {
				f.think.WriteString(input[:len(input)-keep])
				f.pending = input[len(input)-keep:]
				break
			}
			f.think.WriteString(input)
			break
		}

		start := strings.Index(input, "<think>")
		if start >= 0 {
			visible.WriteString(input[:start])
			f.inThink = true
			input = input[start+len("<think>"):]
			continue
		}
		keep := tagPrefixSuffixLen(input, "<think>")
		if keep > 0 {
			visible.WriteString(input[:len(input)-keep])
			f.pending = input[len(input)-keep:]
			break
		}
		visible.WriteString(input)
		break
	}
	return visible.String(), thinks
}

func (f *deepThinkStreamFilter) flush() (string, []string) {
	if f == nil {
		return "", nil
	}
	var visible string
	if f.pending != "" {
		if f.inThink {
			f.think.WriteString(f.pending)
		} else {
			visible = f.pending
		}
		f.pending = ""
	}
	var thinks []string
	if think := strings.TrimSpace(f.think.String()); think != "" {
		thinks = append(thinks, think)
	}
	f.think.Reset()
	f.inThink = false
	return visible, thinks
}

func tagPrefixSuffixLen(text string, tag string) int {
	max := len(tag) - 1
	if len(text) < max {
		max = len(text)
	}
	for size := max; size > 0; size-- {
		if strings.HasSuffix(text, tag[:size]) {
			return size
		}
	}
	return 0
}

func (a *Agent) emitFinalAnswerAfterMaxIterations(ctx context.Context, out chan<- agentruntime.AgentEvent, req agentruntime.AgentRequest, traceID string, state *deepRunState) {
	if a == nil || a.finalModel == nil {
		logx.Printf("异常信息", "error", "deep_agent final_answer_model_missing request_id=%s", req.RequestID)
		emitRuntimeEvent(out, chatflow.EventDelta, map[string]string{"text": fallbackFinalAnswer(state)}, req, traceID)
		return
	}
	timeout := a.finalTimeout
	if timeout <= 0 {
		timeout = defaultFinalAnswerTimeout
	}
	finalCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	messages := buildMaxIterationFinalMessages(req, state, nonEmptyInstruction(a.instruction))
	startedAt := time.Now()
	msg, err := a.finalModel.Generate(finalCtx, messages)
	if err != nil {
		logx.Printf("异常信息", "error", "deep_agent final_answer_failed request_id=%s duration_ms=%d err=%v", req.RequestID, time.Since(startedAt).Milliseconds(), err)
		emitRuntimeEvent(out, chatflow.EventDelta, map[string]string{"text": fallbackFinalAnswer(state)}, req, traceID)
		return
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		logx.Printf("异常信息", "error", "deep_agent final_answer_empty request_id=%s duration_ms=%d", req.RequestID, time.Since(startedAt).Milliseconds())
		emitRuntimeEvent(out, chatflow.EventDelta, map[string]string{"text": fallbackFinalAnswer(state)}, req, traceID)
		return
	}
	logx.Printf("运行日志", "runtime log", "deep_agent final_answer_success request_id=%s chars=%d duration_ms=%d", req.RequestID, len([]rune(text)), time.Since(startedAt).Milliseconds())
	a.emitAssistantContent(out, "final_answer", text, req, traceID, state)
	a.flushThinkFilter(out, req, traceID, state)
}

func buildMaxIterationFinalMessages(req agentruntime.AgentRequest, state *deepRunState, instruction string) []*schema.Message {
	userQuestion := buildUserQuery(req)
	if userQuestion == "" {
		userQuestion = strings.TrimSpace(req.Message)
	}
	history := formatDeepHistory(req.History)
	userPrompt := fmt.Sprintf(`用户问题：
%s

近期对话：
%s

已输出的阶段性内容：
%s

已获取的工具结果：
%s

请基于以上信息直接输出给用户的最终结论。要求：
1. 不要再调用工具。
2. 不要提及内部迭代次数、系统错误或工具循环。
3. 如果信息不足，明确说明只能基于当前信息判断，并给出下一步需要补充的信息。
4. 保险责任、赔付比例、等待期、除外责任等必须提醒以保险合同条款和投保页面为准。`,
		limitRunes(userQuestion, 1200),
		history,
		emptyAsNone(state.assistantSummary()),
		state.toolContext(),
	)
	return []*schema.Message{
		schema.SystemMessage(nonEmptyInstruction(instruction) + "\n\n当前任务：基于已获得的信息生成最终回答，禁止继续调用工具。"),
		schema.UserMessage(userPrompt),
	}
}

func nonEmptyInstruction(instruction string) string {
	if instruction = strings.TrimSpace(instruction); instruction != "" {
		return instruction
	}
	return deepInstruction
}

func formatDeepHistory(history []agentruntime.ChatMessage) string {
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
		lines = append(lines, fmt.Sprintf("- %s: %s", role, limitRunes(content, 600)))
	}
	if len(lines) == 0 {
		return "（无）"
	}
	return limitRunes(strings.Join(lines, "\n"), 2500)
}

func fallbackFinalAnswer(state *deepRunState) string {
	if state != nil {
		if text := strings.TrimSpace(state.assistantSummary()); text != "" {
			return "\n\n我先基于当前已经获取到的信息给出结论：\n" + text + "\n\n以上内容可能不完整，具体保障责任、赔付条件和除外责任仍需以保险合同条款和投保页面为准。"
		}
	}
	return "我目前只能基于已经获取到的有限信息给出判断：这个问题还需要结合具体产品条款、投保页面和被保人情况确认。建议继续补充产品链接、保障责任页面或具体想比较的责任项，最终以保险合同条款为准。"
}

func isMaxIterationsError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, adk.ErrExceedMaxIterations) {
		return true
	}
	return strings.Contains(err.Error(), adk.ErrExceedMaxIterations.Error())
}

func emptyAsNone(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "（无）"
	}
	return text
}

func limitRunes(text string, max int) string {
	if max <= 0 || utf8.RuneCountInString(text) <= max {
		return strings.TrimSpace(text)
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "..."
}

// emitRuntimeEvent 是 DeepAgent 到 AgentRuntime/SSE 的统一出口，会补充 request_id、agent_id、trace_id。
func emitRuntimeEvent(out chan<- agentruntime.AgentEvent, name string, data any, req agentruntime.AgentRequest, traceID string) {
	out <- agentruntime.AgentEvent{
		Name:      name,
		Data:      agentruntime.AddTraceFields(data, req.RequestID, req.AgentID, traceID),
		RequestID: req.RequestID,
		AgentID:   req.AgentID,
		TraceID:   traceID,
	}
}

// toADKMessages 将会话历史和当前请求转换成 Eino ADK 能识别的消息数组。
func toADKMessages(req agentruntime.AgentRequest) []adk.Message {
	messages := make([]adk.Message, 0, len(req.History)+1)
	for _, item := range req.History {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		// 历史消息保留 role，帮助 DeepAgent 理解多轮上下文。
		switch strings.ToLower(strings.TrimSpace(item.Role)) {
		case "assistant":
			messages = append(messages, schema.AssistantMessage(content, nil))
		case "system":
			messages = append(messages, schema.SystemMessage(content))
		default:
			messages = append(messages, schema.UserMessage(content))
		}
	}
	query := buildUserQuery(req)
	if query != "" {
		// 当前请求始终追加在最后，确保 DeepAgent 以最新问题作为决策依据。
		messages = append(messages, schema.UserMessage(query))
	}
	return messages
}

// isDirectDetailAction 判断当前请求是否是前端明确指定的详情直达动作。
func isDirectDetailAction(action string) bool {
	switch strings.TrimSpace(action) {
	case "product_detail", "product_followup":
		return true
	default:
		return false
	}
}

// buildUserQuery 构造传给 DeepAgent 的最新用户消息。
// 普通消息直接使用用户文本；详情类消息会把产品名和链接显式拼入上下文。
func buildUserQuery(req agentruntime.AgentRequest) string {
	message := strings.TrimSpace(req.Message)
	switch strings.TrimSpace(req.Action) {
	case "product_detail", "product_followup":
		var b strings.Builder
		b.WriteString("请基于保险产品详情工具解析并回答用户问题。")
		if req.ProductName != "" {
			b.WriteString("\n产品名称：")
			b.WriteString(req.ProductName)
		}
		if req.ProductURL != "" {
			b.WriteString("\n产品链接：")
			b.WriteString(req.ProductURL)
		}
		if message != "" {
			b.WriteString("\n用户问题：")
			b.WriteString(message)
		}
		return b.String()
	default:
		return message
	}
}

// sessionValues 注入 ADK session 上下文，供工具、trace 或后续扩展读取。
func sessionValues(req agentruntime.AgentRequest) map[string]any {
	values := map[string]any{
		"request_id": req.RequestID,
		"agent_id":   req.AgentID,
	}
	if req.ChatSessionID != "" {
		values["chat_session_id"] = req.ChatSessionID
	}
	if req.AnonymousID != "" {
		values["anonymous_id"] = req.AnonymousID
	}
	if req.UserID != "" {
		values["user_id"] = req.UserID
	}
	if len(req.Metadata) > 0 {
		values["metadata"] = req.Metadata
	}
	return values
}

// chatflowDisclaimerText 保持 DeepAgent 与普通 chatflow 的免责声明一致。
func chatflowDisclaimerText() string {
	return "以上信息仅供参考，具体保障内容请以保险合同条款为准。"
}

// formatToolStatus 将工具名转换成前端可展示的 observing 状态事件。
func formatToolStatus(toolName string) map[string]string {
	switch toolName {
	case toolProductSearch:
		return map[string]string{"stage": "observing", "tool": toolName, "message": "DeepAgent 已读取产品搜索结果。"}
	case toolKnowledgeSearch:
		return map[string]string{"stage": "observing", "tool": toolName, "message": "DeepAgent 已读取知识检索结果。"}
	case toolProductDetail:
		return map[string]string{"stage": "observing", "tool": toolName, "message": "DeepAgent 已读取产品详情结果。"}
	default:
		return map[string]string{"stage": "observing", "tool": toolName, "message": fmt.Sprintf("DeepAgent 已读取工具结果：%s", toolName)}
	}
}
