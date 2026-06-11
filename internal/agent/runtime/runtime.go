package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const DefaultAgentID = "smartinsure-advisor"

var ErrAgentNotFound = errors.New("agent not found")

// Agent 是 Runtime 可调度的最小接口，DeepAgent 和 Plan-Act Agent 都实现它。
type Agent interface {
	ID() string
	Run(ctx context.Context, req AgentRequest) <-chan AgentEvent
}

// AgentRequest 是 API 层传入 AgentRuntime 的统一请求对象。
type AgentRequest struct {
	// RequestID 用于 HTTP、SSE、日志之间串联一次请求。
	RequestID string
	// AgentID 决定 Runtime 从注册表中选择哪个 Agent。
	AgentID string
	// AnonymousID/UserID/ChatSessionID 用于会话权限和短期记忆上下文。
	AnonymousID   string
	ChatSessionID string
	UserID        string
	// Message 是用户当前输入；Action/ProductURL/ProductName 支持产品详情直达动作。
	Message     string
	Action      string
	ProductURL  string
	ProductName string
	// Metadata 预留给前端或后续工具扩展，不直接暴露给最终回答。
	Metadata map[string]any
	// History 是 API 层裁剪后的会话历史，供 Agent 构造 prompt。
	History []ChatMessage
	// TraceDisabled=true 时不生成 trace_id。
	TraceDisabled bool
}

// ChatMessage 是 Runtime 传给 Agent 的历史消息结构。
type ChatMessage struct {
	ID        string
	Role      string
	Content   string
	Metadata  map[string]any
	CreatedAt time.Time
}

// AgentEvent 是 Agent 向 API 层输出的统一事件，最终会被转成 SSE。
type AgentEvent struct {
	Name      string
	Data      any
	RequestID string
	AgentID   string
	TraceID   string
}

// Registry 保存可用 Agent，Runtime 根据 agent_id 从这里取具体实现。
type Registry struct {
	mu     sync.RWMutex
	agents map[string]Agent
}

// NewRegistry 创建空 Agent 注册表。
func NewRegistry() *Registry {
	return &Registry{agents: map[string]Agent{}}
}

// Register 把 Agent 注册到 Runtime，ID 必须稳定且非空。
func (r *Registry) Register(agent Agent) error {
	if r == nil {
		return errors.New("registry is nil")
	}
	if agent == nil || agent.ID() == "" {
		return errors.New("agent id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[agent.ID()] = agent
	return nil
}

// Get 根据 agent_id 查找 Agent，实现并发安全读取。
func (r *Registry) Get(id string) (Agent, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent, ok := r.agents[id]
	return agent, ok
}

// Runtime 是 API 到具体 Agent 的调度层，负责补默认 agent_id/request_id。
type Runtime struct {
	registry *Registry
}

// New 创建 Runtime；未传注册表时使用空注册表兜底。
func New(registry *Registry) *Runtime {
	if registry == nil {
		registry = NewRegistry()
	}
	return &Runtime{registry: registry}
}

// Run 根据请求里的 agent_id 找到对应 Agent，并返回该 Agent 的事件流。
func (r *Runtime) Run(ctx context.Context, req AgentRequest) (<-chan AgentEvent, error) {
	if r == nil || r.registry == nil {
		return nil, ErrAgentNotFound
	}
	// 未指定 agent_id 时走默认 Plan-Act Agent；/api/agent/deep-chat 会在 API 层强制指定 DeepAgent ID。
	agentID := req.AgentID
	if agentID == "" {
		agentID = DefaultAgentID
	}
	agent, ok := r.registry.Get(agentID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}
	req.AgentID = agentID
	if req.RequestID == "" {
		// request_id 缺失时兜底生成，保证 SSE 和日志仍可关联。
		req.RequestID = newID("req")
	}
	return agent.Run(ctx, req), nil
}

// AddTraceFields 给任意 SSE data 补充 request_id、agent_id、trace_id。
// 如果 data 不是对象，会包到 value 字段里，避免破坏事件输出。
func AddTraceFields(data any, requestID, agentID, traceID string) any {
	payload := map[string]any{}
	raw, err := json.Marshal(data)
	if err == nil {
		_ = json.Unmarshal(raw, &payload)
	}
	if len(payload) == 0 {
		payload["value"] = data
	}
	if requestID != "" {
		if _, ok := payload["requestId"]; !ok {
			payload["requestId"] = requestID
		}
		if _, ok := payload["request_id"]; !ok {
			payload["request_id"] = requestID
		}
	}
	if agentID != "" {
		payload["agent_id"] = agentID
	}
	if traceID != "" {
		payload["trace_id"] = traceID
	}
	return payload
}

// NewTraceID 生成一次 Agent 执行链路的 trace_id。
func NewTraceID() string {
	return newID("trace")
}

// newID 生成带前缀的随机 ID；随机数不可用时退化为时间戳。
func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
