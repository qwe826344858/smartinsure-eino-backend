package smartinsureagent

import (
	"context"
	"time"

	"smartinsure-eino-backend/internal/agent/chatflow"
)

type AgentState struct {
	Request chatflow.Request
	Events  chan<- chatflow.Event

	// Steps 是本次请求内的 scratchpad：每次 reasoner 选择 action 后，
	// Graph 都会记录 action、输入、observation、错误和耗时。
	// planner 下一轮会读取这些 observation，但 thought 不会暴露给前端。
	History     []chatflow.ChatMessage
	Intent      chatflow.IntentResult
	Plan        string
	Steps       []AgentStep
	Products    []chatflow.ProductCard
	Results     []chatflow.SearchResultItem
	Sources     []chatflow.SourceItem
	FinalAnswer string
	Iteration   int
}

type AgentStep struct {
	// Thought 只用于后端内部排查和下一轮规划上下文，不写入用户可见 SSE。
	Thought     string
	Action      AgentAction
	ActionInput map[string]any
	Observation AgentObservation
	StartedAt   time.Time
	FinishedAt  time.Time
	Err         string
}

type AgentObservation struct {
	Summary string
	Data    map[string]any
}

func (s *AgentState) appendStep(step AgentStep) {
	s.Steps = append(s.Steps, step)
}

func (s *AgentState) hasAction(name AgentActionName) bool {
	for _, step := range s.Steps {
		if step.Action.Name == name && step.Err == "" {
			return true
		}
	}
	return false
}

func (s *AgentState) hasAttemptedAction(name AgentActionName) bool {
	for _, step := range s.Steps {
		if step.Action.Name == name {
			return true
		}
	}
	return false
}

func (s *AgentState) hasAttemptedActionWithInput(action AgentAction) bool {
	fingerprint := actionFingerprint(action)
	if fingerprint == "" {
		return false
	}
	for _, step := range s.Steps {
		if actionFingerprint(step.Action) == fingerprint {
			return true
		}
	}
	return false
}

func (s *AgentState) lastAction() AgentActionName {
	for i := len(s.Steps) - 1; i >= 0; i-- {
		if s.Steps[i].Err == "" {
			return s.Steps[i].Action.Name
		}
	}
	return ""
}

func (s *AgentState) emit(ctx context.Context, event chatflow.Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.Events <- event:
		return nil
	}
}
