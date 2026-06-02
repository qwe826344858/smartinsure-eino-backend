package smartinsureagent

import (
	"context"

	"smartinsure-eino-backend/internal/agent/chatflow"
	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
)

const DefaultID = agentruntime.DefaultAgentID

// Agent 是 SmartInsureAdvisorAgent 在 AgentRuntime 层的适配器。
// 这一层只负责协议转换、trace 字段追加和调用 AgentGraph，
// 不在这里做保险业务判断，避免 Runtime 适配和业务规划耦合。
type Agent struct {
	id    string
	graph *AgentGraph
}

func New(graph *AgentGraph) *Agent {
	if graph == nil {
		graph = NewProductionGraph()
	}
	return &Agent{id: DefaultID, graph: graph}
}

func (a *Agent) ID() string {
	if a == nil || a.id == "" {
		return DefaultID
	}
	return a.id
}

func (a *Agent) Run(ctx context.Context, req agentruntime.AgentRequest) <-chan agentruntime.AgentEvent {
	out := make(chan agentruntime.AgentEvent)
	go func() {
		defer close(out)
		if req.AgentID == "" {
			req.AgentID = a.ID()
		}
		traceID := ""
		if !req.TraceDisabled {
			traceID = agentruntime.NewTraceID()
		}
		graph := a.graph
		if graph == nil {
			graph = NewProductionGraph()
		}
		// Graph 输出的是 chatflow.Event；AgentRuntime 对外统一为 AgentEvent，
		// 并在 Data 中补充 requestId/request_id/agent_id/trace_id，供前端和日志排查使用。
		for event := range graph.Run(ctx, req) {
			out <- agentruntime.AgentEvent{
				Name:      event.Name,
				Data:      agentruntime.AddTraceFields(event.Data, req.RequestID, req.AgentID, traceID),
				RequestID: req.RequestID,
				AgentID:   req.AgentID,
				TraceID:   traceID,
			}
		}
	}()
	return out
}

func toGraphRequest(req agentruntime.AgentRequest) chatflow.Request {
	return chatflow.Request{
		Message:       req.Message,
		RequestID:     req.RequestID,
		Action:        req.Action,
		ProductURL:    req.ProductURL,
		ProductName:   req.ProductName,
		AnonymousID:   req.AnonymousID,
		UserID:        req.UserID,
		ChatSessionID: req.ChatSessionID,
		Metadata:      req.Metadata,
		History:       toChatflowHistory(req.History),
	}
}

func toChatflowHistory(history []agentruntime.ChatMessage) []chatflow.ChatMessage {
	out := make([]chatflow.ChatMessage, 0, len(history))
	for _, item := range history {
		out = append(out, chatflow.ChatMessage{
			ID:        item.ID,
			Role:      item.Role,
			Content:   item.Content,
			Metadata:  item.Metadata,
			CreatedAt: item.CreatedAt,
		})
	}
	return out
}
