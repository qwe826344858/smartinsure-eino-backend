package api

import (
	"encoding/json"
	"strings"

	"smartinsure-eino-backend/internal/agent/chatflow"
)

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

type assistantAccumulator struct {
	chunks   []string
	metadata map[string]any
}

func (a *assistantAccumulator) capture(event chatflow.Event) {
	switch event.Name {
	case chatflow.EventDelta:
		if text := deltaText(event.Data); text != "" {
			a.chunks = append(a.chunks, text)
		}
	case chatflow.EventProducts, chatflow.EventSources, chatflow.EventDisclaimer, chatflow.EventDetailItems:
		if a.metadata == nil {
			a.metadata = map[string]any{}
		}
		a.metadata[event.Name] = event.Data
	}
}

func (a assistantAccumulator) text() string {
	return strings.Join(a.chunks, "")
}

func (a assistantAccumulator) shouldPersist() bool {
	return strings.TrimSpace(a.text()) != "" || len(a.metadata) > 0
}

func (a assistantAccumulator) metadataJSON() json.RawMessage {
	if len(a.metadata) == 0 {
		return nil
	}
	raw, err := json.Marshal(a.metadata)
	if err != nil {
		return nil
	}
	return raw
}

func deltaText(data any) string {
	switch value := data.(type) {
	case map[string]string:
		return value["text"]
	case map[string]any:
		if text, ok := value["text"].(string); ok {
			return text
		}
	case struct{ Text string }:
		return value.Text
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return payload.Text
}

func withChatSessionID(event chatflow.Event, sessionID string) chatflow.Event {
	if sessionID == "" || event.Name != chatflow.EventDone {
		return event
	}
	raw, err := json.Marshal(event.Data)
	if err != nil {
		return event
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return event
	}
	payload["chat_session_id"] = sessionID
	event.Data = payload
	return event
}
