package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
	apperrors "smartinsure-eino-backend/internal/errors"
	"smartinsure-eino-backend/internal/middleware"
	"smartinsure-eino-backend/internal/stream"
)

func (s *Server) agentChat(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	if !s.agentSettings.AgentChatEnabled {
		apperrors.WriteHTTP(w, apperrors.NotImplemented("Agent 新链路未启用"))
		return
	}

	var body chatRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		apperrors.WriteHTTP(w, apperrors.InvalidArgument("请求体不是合法 JSON"))
		return
	}
	body.normalizeAgentFields()
	if body.Action == "" && strings.TrimSpace(body.Message) == "" {
		apperrors.WriteHTTP(w, apperrors.InvalidArgument("message 不能为空"))
		return
	}

	requestID := requestIDFromHTTP(r, body.RequestID)
	if requestID != "" {
		w.Header().Set("X-Request-Id", requestID)
	}

	message := strings.TrimSpace(body.Message)
	sessionID := strings.TrimSpace(body.ChatSessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(body.SessionID)
	}
	identity := identityFromRequest(r, body.AnonymousID)
	memoryEnabled := sessionID != "" || identity.Valid()
	var userMessage *persistedMessage
	var history []agentruntime.ChatMessage
	var session *conversationSession
	if memoryEnabled {
		if s.conversation == nil {
			apperrors.WriteHTTP(w, serviceUnavailable("会话存储未配置"))
			return
		}
		var err error
		session, err = s.conversation.prepareSession(r.Context(), sessionID, identity)
		if err != nil {
			writeConversationError(w, err)
			return
		}
		sessionID = session.ID
		w.Header().Set("X-Chat-Session-Id", sessionID)

		if message != "" {
			userMessage, err = s.conversation.appendUserMessage(r.Context(), session, message, nil)
			if err != nil {
				writeConversationError(w, err)
				return
			}
		}
		chatHistory, err := s.conversation.loadHistoryAfterUserMessage(r.Context(), sessionID, userMessage)
		if err != nil {
			writeConversationError(w, err)
			return
		}
		history = limitAgentHistory(toAgentHistory(chatHistory), s.agentSettings.AgentMemoryWindow)
	}

	writer, ok := stream.NewWriter(w)
	if !ok {
		apperrors.WriteHTTP(w, apperrors.Internal("当前 ResponseWriter 不支持流式刷新"))
		return
	}

	agentID := strings.TrimSpace(body.AgentID)
	if agentID == "" {
		agentID = s.agentSettings.AgentDefaultID
	}
	events, err := s.agentRuntime.Run(r.Context(), agentruntime.AgentRequest{
		RequestID:     requestID,
		AgentID:       agentID,
		AnonymousID:   identity.AnonymousID,
		ChatSessionID: sessionID,
		UserID:        identity.UserID,
		Message:       message,
		Action:        body.Action,
		ProductURL:    body.ProductURL,
		ProductName:   body.ProductName,
		Metadata:      body.Metadata,
		History:       history,
		TraceDisabled: !s.agentSettings.AgentTraceEnabled,
	})
	if err != nil {
		if errors.Is(err, agentruntime.ErrAgentNotFound) {
			apperrors.WriteHTTP(w, apperrors.New("NOT_FOUND", "Agent 不存在", http.StatusNotFound))
			return
		}
		apperrors.WriteHTTP(w, apperrors.Internal(err.Error()))
		return
	}

	var assistant assistantAccumulator
	for event := range events {
		// AgentRuntime 已经把 agent_id/trace_id 注入 event.Data。
		// API 层只负责会话持久化、补 chat_session_id，并保持 SSE 事件名不变传给前端。
		chatEvent := agentEventToChatflowEvent(event)
		if memoryEnabled {
			assistant.capture(chatEvent)
			chatEvent = withChatSessionID(chatEvent, sessionID)
		}
		if err := writer.Write(stream.Event{Name: chatEvent.Name, Data: chatEvent.Data}); err != nil {
			return
		}
	}
	if memoryEnabled && assistant.shouldPersist() {
		if _, err := s.conversation.appendMessage(r.Context(), session, "assistant", assistant.text(), assistant.metadataJSON()); err != nil {
			log.Printf("failed to persist assistant message: %v", err)
		}
	}
}

func (r *chatRequest) normalizeAgentFields() {
	if r.ProductURL == "" {
		r.ProductURL = r.ProductURLAlt
	}
	if r.ProductName == "" {
		r.ProductName = r.ProductNameAlt
	}
}

func requestIDFromHTTP(r *http.Request, bodyRequestID string) string {
	requestID := bodyRequestID
	if requestID == "" {
		requestID = middleware.RequestIDFromContext(r.Context())
	}
	if requestID == "" {
		requestID = r.Header.Get("X-Request-Id")
	}
	return requestID
}
