package api

import (
	"smartinsure-eino-backend/internal/agent/chatflow"
	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
)

func toAgentHistory(history []chatflow.ChatMessage) []agentruntime.ChatMessage {
	out := make([]agentruntime.ChatMessage, 0, len(history))
	for _, item := range history {
		out = append(out, agentruntime.ChatMessage{
			ID:        item.ID,
			Role:      item.Role,
			Content:   item.Content,
			Metadata:  item.Metadata,
			CreatedAt: item.CreatedAt,
		})
	}
	return out
}

func limitAgentHistory(history []agentruntime.ChatMessage, limit int) []agentruntime.ChatMessage {
	if limit <= 0 || len(history) <= limit {
		return history
	}
	return history[len(history)-limit:]
}

func agentEventToChatflowEvent(event agentruntime.AgentEvent) chatflow.Event {
	return chatflow.Event{Name: event.Name, Data: event.Data}
}
