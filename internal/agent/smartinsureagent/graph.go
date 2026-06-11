package smartinsureagent

import (
	"context"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/agent/chatflow"
	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
	"smartinsure-eino-backend/internal/compliance"
	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/schema"
)

const disclaimerText = "以上信息仅供参考，具体保障内容请以保险合同条款为准。"
const maxInvalidActionDecisions = 2

var productSearchIntents = map[string]bool{
	"product_recommendation": true,
	"product_query":          true,
	"comparison":             true,
}

type AgentGraph struct {
	reasoner      Reasoner
	tools         AgentTools
	answer        chatflow.AnswerStreamer
	maxIterations int
	toolTimeout   time.Duration
}

// NewProductionGraph 组装生产 AgentGraph。
// 它复用 chatflow.NewProduction 的搜索、回答、详情等底层能力，
// 但将执行方式从固定 workflow 切换为 Plan-Act 循环。
func NewProductionGraph() *AgentGraph {
	flow := chatflow.NewProduction()
	settings := config.Load()
	graph := newGraphFromFlow(flow, settings, newProductionReasoner(flow, settings))
	return graph
}

func NewGraphFromFlow(flow *chatflow.Flow) *AgentGraph {
	settings := config.Load()
	return newGraphFromFlow(flow, settings, nil)
}

func newGraphFromFlow(flow *chatflow.Flow, settings config.Settings, reasoner Reasoner) *AgentGraph {
	if flow == nil {
		flow = chatflow.NewProduction()
	}
	ensureAgentFlowDefaults(flow)
	maxIterations := settings.AgentMaxIterations
	if maxIterations <= 0 {
		maxIterations = 4
	}
	toolTimeout := time.Duration(settings.AgentToolTimeout) * time.Second
	if toolTimeout <= 0 {
		toolTimeout = 15 * time.Second
	}
	if reasoner == nil {
		reasoner = newHeuristicReasoner(flow.Intent, flow.Followup)
	}
	return &AgentGraph{
		reasoner:      reasoner,
		tools:         AgentTools{search: flow.Search, fallback: flow.Fallback, detail: flow.Detail},
		answer:        flow.Answer,
		maxIterations: maxIterations,
		toolTimeout:   toolTimeout,
	}
}

func ensureAgentFlowDefaults(flow *chatflow.Flow) {
	defaults := chatflow.New()
	if flow.Intent == nil {
		flow.Intent = defaults.Intent
	}
	if flow.Answer == nil {
		flow.Answer = defaults.Answer
	}
	if flow.Followup == nil {
		flow.Followup = defaults.Followup
	}
	if flow.Fallback == nil {
		flow.Fallback = defaults.Fallback
	}
}

func (g *AgentGraph) Run(ctx context.Context, req agentruntime.AgentRequest) <-chan chatflow.Event {
	// 创建 Graph 对外返回的 SSE 事件通道；调用方会从这个只读通道持续读取 status/delta/products/done 等事件。
	events := make(chan chatflow.Event)
	// 启动独立 goroutine 执行 AgentGraph，避免 Run 阻塞 HTTP handler，使 SSE 可以边执行边返回。
	go func() {
		// 无论 Graph 正常完成、提前 return，还是遇到错误退出，都关闭事件通道，通知调用方流式响应结束。
		defer close(events)
		// 复制当前 receiver，后续允许对 nil receiver 做兜底处理，避免直接使用 g 导致空指针。
		graph := g
		// 如果调用方传入的是 nil Graph，则创建生产 Graph，保证 Agent 入口仍能工作。
		if graph == nil {
			// 生产 Graph 会组装生产 chatflow、reasoner、tools、answer streamer 和配置项。
			graph = NewProductionGraph()
		}
		// 将 Runtime 层请求转换成 Graph 内部状态，然后进入真正的 Plan-Act 执行逻辑。
		graph.run(ctx, &AgentState{
			// Request 是 Graph 内部统一使用的请求对象，包含 message/action/product/session 等信息。
			Request: toGraphRequest(req),
			// Events 指向上面创建的事件通道，Graph 内部所有 SSE 事件都会通过 state.emit 写入这里。
			Events: events,
			// History 是短期记忆窗口，供 reasoner 和最终回答模型理解多轮上下文。
			History: toChatflowHistory(req.History),
		})
	}()
	// 立即返回事件通道；调用方随后通过 range 消费 goroutine 持续写入的 SSE 事件。
	return events
}

func (g *AgentGraph) run(ctx context.Context, state *AgentState) {
	// 防御性判断：如果 state 为空，Graph 没有请求信息、SSE 通道和 scratchpad，无法继续执行。
	if state == nil {
		// state 为空时无法安全写 error event，因此直接返回。
		return
	}
	// 防御性判断：如果 Graph receiver 为空，说明调用方没有正确初始化 AgentGraph。
	if g == nil {
		// 尽量通过 SSE error 把初始化问题返回给调用方，requestId 用于前端和日志关联。
		state.emit(ctx, errorEvent("INTERNAL_ERROR", "agent graph is not initialized", state.Request.RequestID))
		// Graph 本身不存在时无法继续执行任何节点，直接结束本次请求。
		return
	}
	startedAt := time.Now()
	logx.Printf("运行日志", "runtime log", "agent_graph request_start request_id=%s action=%s history=%d max_iterations=%d tool_timeout_seconds=%d", state.Request.RequestID, state.Request.Action, len(state.History), g.maxIterations, int(g.toolTimeout.Seconds()))
	defer func() {
		logx.Printf("运行日志", "runtime log", "agent_graph request_end request_id=%s action=%s iterations=%d steps=%d duration_ms=%d", state.Request.RequestID, state.Request.Action, state.Iteration, len(state.Steps), time.Since(startedAt).Milliseconds())
	}()
	// 先执行会话校验：如果请求携带 chat_session_id，必须同时有 anonymous_id 或 user_id。
	if !g.sessionValidateNode(ctx, state) {
		// sessionValidateNode 内部已经输出 error/done，这里只负责停止后续 Plan-Act。
		return
	}
	// 加载短期记忆。当前历史消息由 API 层读取并裁剪，这里保留为 Graph 的 memory_load 节点。
	g.memoryLoadNode(ctx, state)

	// 产品详情/产品追问来自前端按钮点击，属于“直达工具”场景，不需要先进入 planner 推理。
	if g.isDetailAction(state.Request.Action) {
		logx.Printf("运行日志", "runtime log", "agent_graph direct_detail_route request_id=%s action=%s product_url_present=%t product_name_present=%t", state.Request.RequestID, state.Request.Action, strings.TrimSpace(state.Request.ProductURL) != "", strings.TrimSpace(state.Request.ProductName) != "")
		// 构造标准 AgentAction，复用 executeProductDetail 的统一执行逻辑。
		action := AgentAction{
			// 直达详情固定映射为 product_detail action。
			Name: ActionProductDetail,
			// Input 使用 map，和 LLM planner 输出的 action_input 结构保持一致。
			Input: map[string]any{
				// product_url 是详情解析工具的核心输入。
				"product_url": state.Request.ProductURL,
				// product_name 用于详情解析和前端展示，不存在时工具也可以只依赖 URL。
				"product_name": state.Request.ProductName,
			},
		}
		// 创建本轮执行 step，用于记录 action、输入、时间、observation 和错误。
		step := AgentStep{
			// 保存本轮实际执行的 action。
			Action: action,
			// 保存 action 输入快照，便于后续排查和 observation 上下文构造。
			ActionInput: action.Input,
			// 记录详情工具开始执行的时间。
			StartedAt: time.Now(),
		}
		// 执行产品详情工具。直达详情模式下该方法会直接输出 detail_items/delta/done。
		g.executeProductDetail(ctx, state, &step)
		// 记录详情工具结束时间。
		step.FinishedAt = time.Now()
		if step.Err != "" {
			logx.Printf("运行日志", "runtime log", "agent_graph direct_detail_failed request_id=%s duration_ms=%d err=%s", state.Request.RequestID, step.FinishedAt.Sub(step.StartedAt).Milliseconds(), step.Err)
		} else {
			logx.Printf("运行日志", "runtime log", "agent_graph direct_detail_done request_id=%s duration_ms=%d", state.Request.RequestID, step.FinishedAt.Sub(step.StartedAt).Milliseconds())
		}
		// 将详情执行记录写入 scratchpad，便于后端观测和问题排查。
		state.appendStep(step)
		// 直达详情已经完成事件输出，本次 Graph 执行结束。
		return
	}

	// 记录连续无效决策次数；非法 action 和重复 action 都会计入，避免模型空转。
	invalidDecisions := 0
	// Plan-Act 主循环：每轮先规划 action，再执行 action，再把 observation 写回 state。
	for state.Iteration = 0; state.Iteration < g.maxIterations; state.Iteration++ {
		// 向前端发送 reasoning 状态，表示 Agent 正在根据当前上下文规划下一步。
		if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventStatus, Data: map[string]string{"stage": "reasoning", "message": "正在规划下一步..."}}); err != nil {
			// 写事件失败通常表示客户端断开或 context 已取消，直接结束 goroutine。
			return
		}
		// 调用 reasoner 读取当前 state、history、steps observation，决定下一步 action。
		decision, err := g.reasoner.Next(ctx, state)
		if err != nil {
			// reasoner 自身异常属于内部错误，输出 error event 给调用方。
			state.emit(ctx, errorEvent("INTERNAL_ERROR", err.Error(), state.Request.RequestID))
			// 无法得到下一步规划时不能继续执行工具，直接结束。
			return
		}
		// 对 reasoner 输出做后端强校验：action 必须在白名单内，action_input 必须符合 schema。
		action, err := validateDecision(decision, state.Request)
		if err != nil {
			logx.Printf("运行日志", "runtime log", "agent_graph decision_invalid request_id=%s iteration=%d err=%v", state.Request.RequestID, state.Iteration, err)
			// 校验失败也写入 scratchpad，下一轮或日志能看到模型输出为何不可执行。
			state.appendStep(AgentStep{
				// Thought 只进入后端 scratchpad，不会通过 SSE 暴露给前端。
				Thought: decision.Thought,
				// 保存原始 action_input，便于定位字段缺失、类型错误或额外字段。
				ActionInput: decision.ActionInput,
				// observation 记录校验失败原因，给后续 repair/fallback 提供上下文。
				Observation: AgentObservation{Summary: err.Error()},
				// Err 标记该 step 失败，避免 hasAction 把它视为成功执行过的 action。
				Err: err.Error(),
				// schema 校验失败没有真正执行工具，因此开始时间取当前时间。
				StartedAt: time.Now(),
				// schema 校验失败没有真正执行工具，因此结束时间也取当前时间。
				FinishedAt: time.Now(),
			})
			// 连续无效决策次数加一，防止模型持续输出非法 JSON/action。
			invalidDecisions++
			// 达到连续无效上限后跳出 Plan-Act 循环，后续走 fallbackFinalAnswer。
			if invalidDecisions >= maxInvalidActionDecisions {
				break
			}
			// 未达到上限时继续下一轮，让 reasoner 基于错误 observation 重新规划。
			continue
		}
		logx.Printf("运行日志", "runtime log", "agent_graph decision_valid request_id=%s iteration=%d action=%s", state.Request.RequestID, state.Iteration, action.Name)
		// 检查同一工具和同一核心输入是否已经尝试过，避免重复请求外部工具造成空转。
		if state.hasAttemptedActionWithInput(action) {
			logx.Printf("运行日志", "runtime log", "agent_graph action_duplicate request_id=%s iteration=%d action=%s", state.Request.RequestID, state.Iteration, action.Name)
			// 重复调用同一工具输入通常意味着模型空转。
			// 这里不再执行工具，而是写入 observation，让下一轮 planner 改选其他 action 或 final_answer。
			// 构造一个“被拦截”的 step，明确记录本轮不是成功工具执行。
			step := AgentStep{
				// 保存 reasoner 的 thought，但仅用于后端 scratchpad。
				Thought: decision.Thought,
				// 保存被拦截的 action。
				Action: action,
				// 保存被拦截 action 的输入。
				ActionInput: action.Input,
				// observation 告诉下一轮 reasoner：该工具输入已经重复，应换工具或 final_answer。
				Observation: AgentObservation{
					Summary: "重复工具调用已拦截，要求 reasoner 基于已有 observation 重新规划。",
					Data: map[string]any{
						// 记录 action 名称，方便 planner 和日志知道是哪类 action 重复。
						"action": string(action.Name),
					},
				},
				// Err 标记本 step 为失败/拦截，不计入成功 action。
				Err: "duplicate action suppressed",
				// 记录重复调用拦截开始时间。
				StartedAt: time.Now(),
				// 记录重复调用拦截结束时间。
				FinishedAt: time.Now(),
			}
			// 将重复调用拦截记录写入 scratchpad，下一轮 reasoner 会读取到该 observation。
			state.appendStep(step)
			// 向前端发送 observing 状态，说明系统已跳过重复工具调用并准备重新规划。
			if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventStatus, Data: map[string]string{"stage": "observing", "tool": string(action.Name), "message": "已跳过重复工具调用，正在重新规划..."}}); err != nil {
				// 写 SSE 失败通常表示连接断开，直接结束。
				return
			}
			// 重复调用也计入无效决策，避免模型连续重复同一个 action。
			invalidDecisions++
			// 达到连续无效上限后跳出循环，统一进入 fallbackFinalAnswer。
			if invalidDecisions >= maxInvalidActionDecisions {
				break
			}
			// 未达到上限时继续下一轮，让 reasoner 基于重复调用 observation 重新规划。
			continue
		}
		// 当前 action 合法且不是重复调用，清空连续无效计数。
		invalidDecisions = 0

		// 创建正常执行 step，记录本轮 action 的执行过程和结果。
		step := AgentStep{
			// 保存 reasoner 的内部规划理由，仅用于后端 scratchpad。
			Thought: decision.Thought,
			// 保存已通过校验的 action。
			Action: action,
			// 保存已通过校验和归一化的 action input。
			ActionInput: action.Input,
			// 记录 action 开始执行时间。
			StartedAt: time.Now(),
		}
		// 执行 action；工具类 action 通常返回 false，结束类 action 会返回 true。
		done := g.executeAction(ctx, state, &step)
		// action 执行结束后记录完成时间。
		step.FinishedAt = time.Now()
		if step.Err != "" {
			logx.Printf("运行日志", "runtime log", "agent_graph action_failed request_id=%s iteration=%d action=%s duration_ms=%d err=%s", state.Request.RequestID, state.Iteration, action.Name, step.FinishedAt.Sub(step.StartedAt).Milliseconds(), step.Err)
		} else {
			logx.Printf("运行日志", "runtime log", "agent_graph action_done request_id=%s iteration=%d action=%s duration_ms=%d done=%t", state.Request.RequestID, state.Iteration, action.Name, step.FinishedAt.Sub(step.StartedAt).Milliseconds(), done)
		}
		// 将本轮 action 的 observation/error 写入 scratchpad，供下一轮 reasoner 或最终回答使用。
		state.appendStep(step)
		// 如果 action 已经完成本次请求，例如 ask_followup/final_answer/直达详情，则结束 Graph。
		if done {
			return
		}
	}

	// 循环结束但没有正常 final_answer，说明达到最大轮次或无效决策上限，使用已有 observation 兜底回答。
	g.fallbackFinalAnswer(ctx, state)
}

func (g *AgentGraph) executeAction(ctx context.Context, state *AgentState, step *AgentStep) bool {
	// 返回值表示本次请求是否已经结束。
	// 工具类 action 通常返回 false，让 Graph 带着 observation 继续 replan；
	// ask_followup/final_answer/直达详情会返回 true，直接结束 SSE。
	switch step.Action.Name {
	case ActionProductSearch:
		return g.executeProductSearch(ctx, state, step)
	case ActionKnowledgeSearch:
		return g.executeKnowledgeSearch(ctx, state, step)
	case ActionProductDetail:
		return g.executeProductDetail(ctx, state, step)
	case ActionAskFollowup:
		text := compliance.Sanitize(strings.TrimSpace(stringInput(step.Action.Input, "question")))
		if text == "" {
			text = "为了给出更准确的建议，请补充保障对象、年龄、预算和已有保障情况。"
		}
		step.Observation = AgentObservation{Summary: "已向用户追问缺失信息。"}
		state.emit(ctx, chatflow.Event{Name: chatflow.EventDelta, Data: map[string]string{"text": text}})
		state.emit(ctx, chatflow.Event{Name: chatflow.EventDone, Data: map[string]string{"requestId": state.Request.RequestID}})
		return true
	case ActionFinalAnswer:
		step.Observation = AgentObservation{Summary: "开始输出最终回答。"}
		g.finalAnswer(ctx, state, step.Action)
		return true
	default:
		step.Err = "unsupported action"
		return false
	}
}

func (g *AgentGraph) sessionValidateNode(ctx context.Context, state *AgentState) bool {
	if strings.TrimSpace(state.Request.ChatSessionID) != "" &&
		strings.TrimSpace(state.Request.AnonymousID) == "" &&
		strings.TrimSpace(state.Request.UserID) == "" {
		state.emit(ctx, errorEvent("INVALID_ARGUMENT", "chat_session_id 需要 anonymous_id 或 user_id", state.Request.RequestID))
		state.emit(ctx, chatflow.Event{Name: chatflow.EventDone, Data: map[string]string{"requestId": state.Request.RequestID}})
		return false
	}
	return true
}

func (g *AgentGraph) memoryLoadNode(_ context.Context, _ *AgentState) {
	// API 层已完成 MySQL/Redis 读取与窗口裁剪；AgentGraph 以 Request.History 作为 memory_load 输入。
}

func (g *AgentGraph) executeProductSearch(ctx context.Context, state *AgentState, step *AgentStep) bool {
	if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventStatus, Data: map[string]string{"stage": "tool_running", "tool": string(ActionProductSearch), "message": "正在搜索保险产品..."}}); err != nil {
		return true
	}
	query := stringInput(step.Action.Input, "query")
	logx.Printf("运行日志", "runtime log", "agent_graph tool_start request_id=%s action=%s query_chars=%d", state.Request.RequestID, ActionProductSearch, len([]rune(strings.TrimSpace(query))))
	toolCtx, cancel := context.WithTimeout(ctx, g.toolTimeout)
	defer cancel()
	toolStartedAt := time.Now()
	obs, err := g.tools.ProductSearch(toolCtx, query)
	if err != nil {
		step.Err = err.Error()
		step.Observation = AgentObservation{Summary: err.Error()}
		logx.Printf("运行日志", "runtime log", "agent_graph tool_failed request_id=%s action=%s duration_ms=%d err=%v", state.Request.RequestID, ActionProductSearch, time.Since(toolStartedAt).Milliseconds(), err)
		return false
	}
	logx.Printf("运行日志", "runtime log", "agent_graph tool_success request_id=%s action=%s products=%d duration_ms=%d", state.Request.RequestID, ActionProductSearch, len(obs.Products), time.Since(toolStartedAt).Milliseconds())
	step.Observation = observationFromProductSearch(obs)
	state.Products = obs.Products
	if len(obs.Products) > 0 {
		state.emit(ctx, chatflow.Event{Name: chatflow.EventProducts, Data: map[string]any{"items": obs.Products}})
	}
	if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventStatus, Data: map[string]string{"stage": "observing", "tool": string(ActionProductSearch), "message": "已读取产品搜索结果。"}}); err != nil {
		return true
	}
	return false
}

func (g *AgentGraph) executeKnowledgeSearch(ctx context.Context, state *AgentState, step *AgentStep) bool {
	if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventStatus, Data: map[string]string{"stage": "tool_running", "tool": string(ActionKnowledgeSearch), "message": "正在检索保险知识..."}}); err != nil {
		return true
	}
	query := stringInput(step.Action.Input, "query")
	logx.Printf("运行日志", "runtime log", "agent_graph tool_start request_id=%s action=%s query_chars=%d", state.Request.RequestID, ActionKnowledgeSearch, len([]rune(strings.TrimSpace(query))))
	toolCtx, cancel := context.WithTimeout(ctx, g.toolTimeout)
	defer cancel()
	toolStartedAt := time.Now()
	obs, err := g.tools.KnowledgeSearch(toolCtx, query)
	if err != nil {
		step.Err = err.Error()
		step.Observation = AgentObservation{Summary: err.Error()}
		logx.Printf("运行日志", "runtime log", "agent_graph tool_failed request_id=%s action=%s duration_ms=%d err=%v", state.Request.RequestID, ActionKnowledgeSearch, time.Since(toolStartedAt).Milliseconds(), err)
		return false
	}
	logx.Printf("运行日志", "runtime log", "agent_graph tool_success request_id=%s action=%s results=%d sources=%d duration_ms=%d", state.Request.RequestID, ActionKnowledgeSearch, len(obs.Results), len(obs.Sources), time.Since(toolStartedAt).Milliseconds())
	step.Observation = observationFromKnowledgeSearch(obs)
	state.Results = obs.Results
	state.Sources = obs.Sources
	if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventStatus, Data: map[string]string{"stage": "observing", "tool": string(ActionKnowledgeSearch), "message": "已读取知识检索结果。"}}); err != nil {
		return true
	}
	return false
}

func (g *AgentGraph) executeProductDetail(ctx context.Context, state *AgentState, step *AgentStep) bool {
	action := step.Action
	directDetailAction := g.isDetailAction(state.Request.Action)
	if strings.TrimSpace(stringInput(action.Input, "product_url")) == "" {
		step.Err = "productUrl 不能为空"
		step.Observation = AgentObservation{Summary: step.Err}
		state.emit(ctx, errorEvent("INVALID_ARGUMENT", "productUrl 不能为空", state.Request.RequestID))
		if directDetailAction {
			state.emit(ctx, chatflow.Event{Name: chatflow.EventDone, Data: map[string]string{"requestId": state.Request.RequestID}})
			return true
		}
		return false
	}
	if g.tools.detail == nil {
		step.Err = "产品详情/追问 Skill 尚未配置"
		step.Observation = AgentObservation{Summary: step.Err}
		state.emit(ctx, chatflow.Event{Name: chatflow.EventError, Data: map[string]any{
			"code":      "NOT_IMPLEMENTED",
			"message":   "产品详情/追问 Skill 尚未配置",
			"requestId": state.Request.RequestID,
		}})
		if directDetailAction {
			state.emit(ctx, chatflow.Event{Name: chatflow.EventDone, Data: map[string]string{"requestId": state.Request.RequestID}})
			return true
		}
		return false
	}

	if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventStatus, Data: map[string]string{"stage": "tool_running", "tool": string(ActionProductDetail), "message": "正在解析产品详情..."}}); err != nil {
		step.Err = err.Error()
		step.Observation = AgentObservation{Summary: err.Error()}
		return true
	}
	productURL := strings.TrimSpace(stringInput(action.Input, "product_url"))
	logx.Printf("运行日志", "runtime log", "agent_graph tool_start request_id=%s action=%s direct=%t product_url_present=%t", state.Request.RequestID, ActionProductDetail, directDetailAction, productURL != "")
	toolCtx, cancel := context.WithTimeout(ctx, g.toolTimeout)
	defer cancel()
	toolStartedAt := time.Now()
	detailEvents := g.tools.detail.Run(toolCtx, chatflow.DetailRequest{
		ProductURL:   productURL,
		ProductName:  strings.TrimSpace(stringInput(action.Input, "product_name")),
		UserQuestion: strings.TrimSpace(state.Request.Message),
		RequestID:    state.Request.RequestID,
		Action:       state.Request.Action,
	})
	sawDone := false
	collector := productDetailObservationCollector{}
	for detailEvents != nil {
		select {
		case <-toolCtx.Done():
			step.Err = toolCtx.Err().Error()
			step.Observation = collector.observation("产品详情解析超时或被取消。", step.Err)
			logx.Printf("运行日志", "runtime log", "agent_graph tool_timeout request_id=%s action=%s direct=%t duration_ms=%d err=%v", state.Request.RequestID, ActionProductDetail, directDetailAction, time.Since(toolStartedAt).Milliseconds(), toolCtx.Err())
			state.emit(ctx, errorEvent("INTERNAL_ERROR", toolCtx.Err().Error(), state.Request.RequestID))
			if directDetailAction {
				state.emit(ctx, chatflow.Event{Name: chatflow.EventDone, Data: map[string]string{"requestId": state.Request.RequestID}})
				return true
			}
			return false
		case event, ok := <-detailEvents:
			if !ok {
				detailEvents = nil
				continue
			}
			if event.Name == "" {
				continue
			}
			if event.Name == chatflow.EventDone {
				sawDone = true
			}
			collector.observe(event)
			if !directDetailAction && event.Name == chatflow.EventDelta {
				continue
			}
			state.emit(ctx, event)
		}
	}
	step.Observation = collector.observation("已执行产品详情解析。", "")
	logx.Printf("运行日志", "runtime log", "agent_graph tool_success request_id=%s action=%s direct=%t duration_ms=%d saw_done=%t", state.Request.RequestID, ActionProductDetail, directDetailAction, time.Since(toolStartedAt).Milliseconds(), sawDone)
	if !directDetailAction {
		if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventStatus, Data: map[string]string{"stage": "observing", "tool": string(ActionProductDetail), "message": "已读取产品详情结果。"}}); err != nil {
			step.Err = err.Error()
			return true
		}
		return false
	}
	if !sawDone {
		state.emit(ctx, chatflow.Event{Name: chatflow.EventDone, Data: map[string]string{"requestId": state.Request.RequestID}})
	}
	return true
}

func (g *AgentGraph) finalAnswer(ctx context.Context, state *AgentState, action AgentAction) {
	answerText := compliance.Sanitize(strings.TrimSpace(stringInput(action.Input, "answer_text")))
	if answerText != "" && len(state.Steps) == 0 && len(state.Products) == 0 && len(state.Results) == 0 {
		state.emit(ctx, chatflow.Event{Name: chatflow.EventDelta, Data: map[string]string{"text": answerText}})
		g.finishNode(ctx, state)
		return
	}

	if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventStatus, Data: map[string]string{"stage": "answering", "message": "正在生成回答..."}}); err != nil {
		return
	}
	intentName := state.Intent.Intent
	if intentName == "" {
		intentName = "knowledge_explain"
	}
	chunks, errs := g.answer.Stream(ctx, chatflow.AnswerInput{
		// finalAnswerMessage 会把 answer_brief、产品和 steps observation 拼入上下文，
		// 让最终回答真正使用 Plan-Act 过程中积累的信息。
		Message: finalAnswerMessage(state, action),
		Intent:  intentName,
		Results: state.Results,
		History: state.History,
	})
	for chunks != nil || errs != nil {
		select {
		case <-ctx.Done():
			state.emit(ctx, errorEvent("INTERNAL_ERROR", ctx.Err().Error(), state.Request.RequestID))
			return
		case chunk, ok := <-chunks:
			if !ok {
				chunks = nil
				continue
			}
			if chunk != "" {
				if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventDelta, Data: map[string]string{"text": chunk}}); err != nil {
					return
				}
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				state.emit(ctx, errorEvent("INTERNAL_ERROR", err.Error(), state.Request.RequestID))
				return
			}
		}
	}
	g.finishNode(ctx, state)
}

func (g *AgentGraph) fallbackFinalAnswer(ctx context.Context, state *AgentState) {
	g.finalAnswer(ctx, state, AgentAction{Name: ActionFinalAnswer, Input: map[string]any{"answer_brief": "工具调用次数已达上限，基于当前信息输出建议。"}})
}

func finalAnswerMessage(state *AgentState, action AgentAction) string {
	if state == nil {
		return ""
	}
	base := strings.TrimSpace(state.Request.Message)
	brief := strings.TrimSpace(stringInput(action.Input, "answer_brief"))
	hasContext := brief != "" || len(state.Products) > 0 || len(state.Steps) > 0
	if !hasContext {
		return base
	}

	var builder strings.Builder
	builder.WriteString("用户原始问题:\n")
	builder.WriteString(base)
	if brief != "" {
		builder.WriteString("\n\n【最终回答重点】\n")
		builder.WriteString(brief)
	}
	if len(state.Products) > 0 {
		builder.WriteString("\n\n【已展示产品卡片】\n")
		builder.WriteString(formatPlannerProducts(state.Products))
	}
	if len(state.Steps) > 0 {
		builder.WriteString("\n\n【Agent 工具观察】\n")
		builder.WriteString(formatPlannerScratchpad(state.Steps, 600))
	}
	builder.WriteString("\n\n请基于以上 observation 输出面向用户的最终保险建议；不要暴露内部推理，不要编造来源。")
	return builder.String()
}

func (g *AgentGraph) finishNode(ctx context.Context, state *AgentState) {
	if len(state.Sources) > 0 {
		if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventSources, Data: map[string]any{"items": state.Sources}}); err != nil {
			return
		}
	}
	if err := state.emit(ctx, chatflow.Event{Name: chatflow.EventDisclaimer, Data: map[string]string{"text": disclaimerText}}); err != nil {
		return
	}
	state.emit(ctx, chatflow.Event{Name: chatflow.EventDone, Data: map[string]string{"requestId": state.Request.RequestID}})
}

func (g *AgentGraph) isDetailAction(action string) bool {
	return action == "product_detail" || action == "product_followup"
}

func errorEvent(code, message, requestID string) chatflow.Event {
	return chatflow.Event{Name: chatflow.EventError, Data: map[string]string{"code": code, "message": message, "requestId": requestID}}
}

func uniqueSources(results []chatflow.SearchResultItem) []chatflow.SourceItem {
	seen := make(map[string]bool, len(results))
	sources := make([]chatflow.SourceItem, 0, len(results))
	for _, result := range results {
		if result.URL == "" || seen[result.URL] {
			continue
		}
		seen[result.URL] = true
		sources = append(sources, chatflow.SourceItem{
			Title:      result.Title,
			URL:        result.URL,
			Site:       result.Site,
			ProductURL: firstNonEmptyString(result.ProductURL, result.URL),
		})
	}
	return sources
}

type productDetailObservationCollector struct {
	detailEvents int
	deltaChunks  int
	deltaChars   int
	productName  string
	dutyCount    int
}

func (c *productDetailObservationCollector) observe(event chatflow.Event) {
	switch event.Name {
	case chatflow.EventDetailItems:
		c.detailEvents++
		switch data := event.Data.(type) {
		case map[string]any:
			c.captureDetailMap(data)
		case map[string]string:
			if c.productName == "" {
				c.productName = data["product_name"]
			}
		case schema.SSEDetailItemsPayload:
			c.productName = firstNonEmptyString(c.productName, data.ProductName)
			c.dutyCount += len(data.Duties)
		}
	case chatflow.EventDelta:
		if text := eventText(event.Data); text != "" {
			c.deltaChunks++
			c.deltaChars += len([]rune(text))
		}
	}
}

func (c *productDetailObservationCollector) captureDetailMap(data map[string]any) {
	if c.productName == "" {
		if name, ok := data["product_name"].(string); ok {
			c.productName = name
		}
	}
	if duties, ok := data["duties"].([]map[string]any); ok {
		c.dutyCount += len(duties)
		return
	}
	if duties, ok := data["duties"].([]any); ok {
		c.dutyCount += len(duties)
	}
}

func (c productDetailObservationCollector) observation(summary, errText string) AgentObservation {
	data := map[string]any{
		"detail_event_count": c.detailEvents,
		"delta_chunks":       c.deltaChunks,
		"delta_chars":        c.deltaChars,
	}
	if c.productName != "" {
		data["product_name"] = c.productName
	}
	if c.dutyCount > 0 {
		data["duty_count"] = c.dutyCount
	}
	if errText != "" {
		data["error"] = errText
	}
	return AgentObservation{Summary: summary, Data: data}
}

func eventText(data any) string {
	switch payload := data.(type) {
	case map[string]string:
		return payload["text"]
	case map[string]any:
		if text, ok := payload["text"].(string); ok {
			return text
		}
	case schema.SSEDeltaPayload:
		return payload.Text
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
