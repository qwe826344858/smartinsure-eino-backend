package chatflow

import (
	"context"
	"errors"
	"fmt"
	"io"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	toolsearch "smartinsure-eino-backend/internal/tool/search"
)

const (
	// graphNodeRoute 是入口路由节点，负责根据 action 判断进入产品详情还是普通对话。
	graphNodeRoute = "action_router"
	// graphNodeDetail 是产品详情节点，复用原 DetailRunner 处理产品详情和产品追问。
	graphNodeDetail = "product_detail"
	// graphNodeIntentPrompt 为意图识别构造模型输入。
	graphNodeIntentPrompt = "intent_prompt"
	// graphNodeIntentModel 是 Eino ChatModelNode 意图识别模型节点。
	graphNodeIntentModel = "intent_model"
	// graphNodeIntentParse 解析意图模型输出并写入 graphState。
	graphNodeIntentParse = "intent_parse"
	// graphNodeIntentGate 是意图分流闸口。
	graphNodeIntentGate = "intent_gate"
	// graphNodeOutOfScope 是超范围收口节点，用于直接回复非保险相关问题。
	graphNodeOutOfScope = "out_of_scope"
	// graphNodeFollowupPrompt 为缺槽追问构造模型输入。
	graphNodeFollowupPrompt = "followup_prompt"
	// graphNodeFollowupModel 是 Eino ChatModelNode 缺槽追问模型节点。
	graphNodeFollowupModel = "followup_model"
	// graphNodeFollowupEmit 输出缺槽追问 delta/done。
	graphNodeFollowupEmit = "followup_emit"
	// graphNodeSearchTool 是确定性搜索工具边界。
	graphNodeSearchTool = "search_tool"
	// graphNodeAnswerPrompt 为回答生成构造模型输入。
	graphNodeAnswerPrompt = "answer_prompt"
	// graphNodeAnswerModel 是 Eino ChatModelNode 回答模型节点。
	graphNodeAnswerModel = "answer_model"
	// graphNodeAnswerEmit 把回答模型流式输出转成 SSE delta。
	graphNodeAnswerEmit = "answer_stream_emit"
	// graphNodeFinish 是普通对话收尾节点，负责输出来源、免责声明和 done。
	graphNodeFinish = "finish"
)

type GraphFlow struct {
	flow       *Flow
	models     GraphChatModels
	searchTool SearchTool
	runnable   compose.Runnable[*graphState, *graphState]
}

type graphRunState struct {
	State *graphState
}

type GraphOption func(*graphFlowConfig)

type graphFlowConfig struct {
	models     GraphChatModels
	searchTool SearchTool
}

type GraphChatModels struct {
	Intent   einomodel.BaseChatModel
	Followup einomodel.BaseChatModel
	Answer   einomodel.BaseChatModel
}

func WithGraphChatModels(models GraphChatModels) GraphOption {
	return func(cfg *graphFlowConfig) {
		cfg.models = models
	}
}

func WithGraphSearchTool(tool SearchTool) GraphOption {
	return func(cfg *graphFlowConfig) {
		cfg.searchTool = tool
	}
}

// graphState 是 Eino Graph 节点之间传递的可变状态。
// Request 和 Events 在一次请求中固定不变；其他字段由 intent/search
// 节点逐步写入，再被 answer/finish 节点消费。
type graphState struct {
	Request Request
	Events  chan<- Event

	Intent  IntentResult
	Results []SearchResultItem
	Sources []SourceItem

	SearchOutput toolsearch.SearchToolOutput
}

func NewGraphFlow(flow *Flow, opts ...GraphOption) (*GraphFlow, error) {
	if flow == nil {
		flow = New()
	}
	flow.ensureDefaults()

	cfg := graphFlowConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	models := cfg.models.withFallback(flow)
	searchTool := cfg.searchTool
	if searchTool == nil {
		searchTool = newInProcessSearchTool(flow)
	}

	graphFlow := &GraphFlow{flow: flow, models: models, searchTool: searchTool}
	runnable, err := graphFlow.compile(context.Background())
	if err != nil {
		return nil, err
	}
	graphFlow.runnable = runnable
	return graphFlow, nil
}

func (g *GraphFlow) Run(ctx context.Context, req Request) <-chan Event {
	events := make(chan Event)
	go func() {
		defer close(events)
		if g == nil || g.runnable == nil {
			events <- errorEvent("INTERNAL_ERROR", "eino graph orchestrator is not initialized", req.RequestID)
			return
		}
		g.flow.ensureDefaults()
		state := &graphState{Request: req, Events: events}
		stream, err := g.runnable.Stream(ctx, state)
		if err != nil {
			if ctx.Err() == nil {
				events <- errorEvent("INTERNAL_ERROR", err.Error(), req.RequestID)
			}
			return
		}
		defer stream.Close()
		for {
			if _, err := stream.Recv(); err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				if ctx.Err() == nil {
					events <- errorEvent("INTERNAL_ERROR", err.Error(), req.RequestID)
				}
				return
			}
		}
	}()
	return events
}

func (g *GraphFlow) compile(ctx context.Context) (compose.Runnable[*graphState, *graphState], error) {
	graph := compose.NewGraph[*graphState, *graphState](
		compose.WithGenLocalState(func(context.Context) *graphRunState {
			return &graphRunState{}
		}),
	)

	// 节点时序：
	// 1. action_router 只判断当前请求是否为产品详情 action。
	// 2. product_detail 完全委托给已有详情 runner，执行结束后直接收口。
	// 3. intent_prompt -> intent_model -> intent_parse 使用 ChatModelNode 完成意图识别。
	// 4. search_tool 先输出产品卡片，再把 fallback 来源写入 graphState。
	// 5. answer_prompt -> answer_model -> answer_stream_emit 使用 ChatModelNode 流式输出 delta。
	if err := graph.AddLambdaNode(graphNodeRoute, compose.InvokableLambda(g.routeNode)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeDetail, compose.InvokableLambda(g.detailNode)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeIntentPrompt, compose.InvokableLambda(g.intentPromptNode)); err != nil {
		return nil, err
	}
	if err := graph.AddChatModelNode(graphNodeIntentModel, g.models.Intent, compose.WithNodeName(graphNodeIntentModel)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeIntentParse, compose.InvokableLambda(g.intentParseNode)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeIntentGate, compose.InvokableLambda(g.intentGateNode)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeOutOfScope, compose.InvokableLambda(g.outOfScopeNode)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeFollowupPrompt, compose.InvokableLambda(g.followupPromptNode)); err != nil {
		return nil, err
	}
	if err := graph.AddChatModelNode(graphNodeFollowupModel, g.models.Followup, compose.WithNodeName(graphNodeFollowupModel)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeFollowupEmit, compose.InvokableLambda(g.followupEmitNode)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeSearchTool, compose.InvokableLambda(g.searchToolNode)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeAnswerPrompt, compose.InvokableLambda(g.answerPromptNode)); err != nil {
		return nil, err
	}
	if err := graph.AddChatModelNode(graphNodeAnswerModel, g.models.Answer, compose.WithNodeName(graphNodeAnswerModel)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeAnswerEmit, compose.CollectableLambda(g.answerStreamEmitNode)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(graphNodeFinish, compose.InvokableLambda(g.finishNode)); err != nil {
		return nil, err
	}

	// 入口分支：
	// START -> action_router -> product_detail -> END
	// START -> action_router -> intent_prompt -> ...
	if err := graph.AddEdge(compose.START, graphNodeRoute); err != nil {
		return nil, err
	}
	if err := graph.AddBranch(graphNodeRoute, compose.NewGraphBranch(routeAction, map[string]bool{
		graphNodeDetail:       true,
		graphNodeIntentPrompt: true,
	})); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeDetail, compose.END); err != nil {
		return nil, err
	}
	// 意图分支：
	// out_of_scope 和 followup 在输出面向用户的 delta 后立即结束。
	// 普通推荐/查询/对比请求继续进入 search_tool -> answer_model -> finish。
	if err := graph.AddEdge(graphNodeIntentPrompt, graphNodeIntentModel); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeIntentModel, graphNodeIntentParse); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeIntentParse, graphNodeIntentGate); err != nil {
		return nil, err
	}
	if err := graph.AddBranch(graphNodeIntentGate, compose.NewGraphBranch(routeIntent, map[string]bool{
		graphNodeOutOfScope:     true,
		graphNodeFollowupPrompt: true,
		graphNodeSearchTool:     true,
		compose.END:             true,
	})); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeOutOfScope, compose.END); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeFollowupPrompt, graphNodeFollowupModel); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeFollowupModel, graphNodeFollowupEmit); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeFollowupEmit, compose.END); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeSearchTool, graphNodeAnswerPrompt); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeAnswerPrompt, graphNodeAnswerModel); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeAnswerModel, graphNodeAnswerEmit); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeAnswerEmit, graphNodeFinish); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(graphNodeFinish, compose.END); err != nil {
		return nil, err
	}
	return graph.Compile(ctx, compose.WithGraphName("smartinsure_chatflow"))
}

func routeAction(_ context.Context, state *graphState) (string, error) {
	if state != nil && isDetailAction(state.Request.Action) {
		return graphNodeDetail, nil
	}
	return graphNodeIntentPrompt, nil
}

func routeIntent(_ context.Context, state *graphState) (string, error) {
	if state == nil {
		return compose.END, nil
	}
	if state.Intent.Intent == "" {
		return compose.END, nil
	}
	if state.Intent.Intent == "out_of_scope" {
		return graphNodeOutOfScope, nil
	}
	if state.Intent.NeedsFollowup {
		return graphNodeFollowupPrompt, nil
	}
	return graphNodeSearchTool, nil
}

func (g *GraphFlow) routeNode(ctx context.Context, state *graphState) (*graphState, error) {
	if state == nil {
		return nil, fmt.Errorf("graph state is nil")
	}
	if err := setCurrentGraphState(ctx, state); err != nil {
		return state, err
	}
	return state, nil
}

func (g *GraphFlow) detailNode(ctx context.Context, state *graphState) (*graphState, error) {
	// 产品详情保持原有流式协议。detail runner 自己管理内部时序，
	// 并输出 status/detail_items/delta/done。
	g.flow.runDetail(ctx, state.Request, state.Events)
	return state, nil
}

func (g *GraphFlow) intentGateNode(_ context.Context, state *graphState) (*graphState, error) {
	return state, nil
}

func (g *GraphFlow) outOfScopeNode(ctx context.Context, state *graphState) (*graphState, error) {
	if err := state.emit(ctx, Event{Name: EventDelta, Data: map[string]string{"text": "我目前专注于保险咨询。您如果想了解重疾险、医疗险、条款解读或产品对比，我可以继续帮您。"}}); err != nil {
		return state, err
	}
	return state, state.emit(ctx, Event{Name: EventDone, Data: map[string]string{"requestId": state.Request.RequestID}})
}

func (g *GraphFlow) finishNode(ctx context.Context, state *graphState) (*graphState, error) {
	// finish 是普通对话路径中唯一负责收口的节点，确保 lite 和 graph
	// 两种模式下 sources/disclaimer/done 的顺序一致。
	if len(state.Sources) > 0 {
		if err := state.emit(ctx, Event{Name: EventSources, Data: map[string]any{"items": state.Sources}}); err != nil {
			return state, err
		}
	}
	if err := state.emit(ctx, Event{Name: EventDisclaimer, Data: map[string]string{"text": disclaimerText}}); err != nil {
		return state, err
	}
	return state, state.emit(ctx, Event{Name: EventDone, Data: map[string]string{"requestId": state.Request.RequestID}})
}

func (s *graphState) emit(ctx context.Context, event Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.Events <- event:
		return nil
	}
}

func isDetailAction(action string) bool {
	return action == "product_detail" || action == "product_followup"
}

func setCurrentGraphState(ctx context.Context, state *graphState) error {
	return compose.ProcessState[*graphRunState](ctx, func(_ context.Context, run *graphRunState) error {
		run.State = state
		return nil
	})
}

func currentGraphState(ctx context.Context) (*graphState, error) {
	var state *graphState
	err := compose.ProcessState[*graphRunState](ctx, func(_ context.Context, run *graphRunState) error {
		state = run.State
		return nil
	})
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("graph state is not initialized")
	}
	return state, nil
}
