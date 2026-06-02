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

type Agent interface {
	ID() string
	Run(ctx context.Context, req AgentRequest) <-chan AgentEvent
}

type AgentRequest struct {
	RequestID     string
	AgentID       string
	AnonymousID   string
	ChatSessionID string
	UserID        string
	Message       string
	Action        string
	ProductURL    string
	ProductName   string
	Metadata      map[string]any
	History       []ChatMessage
	TraceDisabled bool
}

type ChatMessage struct {
	ID        string
	Role      string
	Content   string
	Metadata  map[string]any
	CreatedAt time.Time
}

type AgentEvent struct {
	Name      string
	Data      any
	RequestID string
	AgentID   string
	TraceID   string
}

type Registry struct {
	mu     sync.RWMutex
	agents map[string]Agent
}

func NewRegistry() *Registry {
	return &Registry{agents: map[string]Agent{}}
}

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

func (r *Registry) Get(id string) (Agent, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent, ok := r.agents[id]
	return agent, ok
}

type Runtime struct {
	registry *Registry
}

func New(registry *Registry) *Runtime {
	if registry == nil {
		registry = NewRegistry()
	}
	return &Runtime{registry: registry}
}

func (r *Runtime) Run(ctx context.Context, req AgentRequest) (<-chan AgentEvent, error) {
	if r == nil || r.registry == nil {
		return nil, ErrAgentNotFound
	}
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
		req.RequestID = newID("req")
	}
	return agent.Run(ctx, req), nil
}

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

func NewTraceID() string {
	return newID("trace")
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
