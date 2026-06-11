package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	apperrors "smartinsure-eino-backend/internal/errors"
)

type sessionRequest struct {
	AnonymousID string `json:"anonymous_id"`
}

type sessionResponse struct {
	ChatSessionID string `json:"chat_session_id"`
	Title         string `json:"title"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	LastMessageAt string `json:"last_message_at,omitempty"`
}

type sessionsResponse struct {
	Sessions []sessionResponse `json:"sessions"`
}

type messagesResponse struct {
	ChatSessionID string             `json:"chat_session_id"`
	Messages      []persistedMessage `json:"messages"`
}

func (s *Server) currentChatSession(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	if s.conversation == nil {
		apperrors.WriteHTTP(w, serviceUnavailable("会话存储未配置"))
		return
	}
	body, ok := decodeSessionRequest(w, r)
	if !ok {
		return
	}
	session, err := s.conversation.currentSession(r.Context(), identityFromRequest(r, body.AnonymousID))
	if err != nil {
		writeConversationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSessionResponse(session))
}

func (s *Server) chatSessions(w http.ResponseWriter, r *http.Request) {
	if s.conversation == nil {
		apperrors.WriteHTTP(w, serviceUnavailable("会话存储未配置"))
		return
	}
	switch r.Method {
	case http.MethodPost:
		body, ok := decodeSessionRequest(w, r)
		if !ok {
			return
		}
		session, err := s.conversation.newSession(r.Context(), identityFromRequest(r, body.AnonymousID))
		if err != nil {
			writeConversationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSessionResponse(session))
	case http.MethodGet:
		identity := identityFromRequest(r, r.URL.Query().Get("anonymous_id"))
		limit := queryInt(r, "limit", 20)
		sessions, err := s.conversation.listSessions(r.Context(), identity, limit)
		if err != nil {
			writeConversationError(w, err)
			return
		}
		out := make([]sessionResponse, 0, len(sessions))
		for i := range sessions {
			out = append(out, toSessionResponse(&sessions[i]))
		}
		writeJSON(w, http.StatusOK, sessionsResponse{Sessions: out})
	default:
		w.Header().Set("Allow", "GET, POST")
		apperrors.WriteHTTP(w, apperrors.New("METHOD_NOT_ALLOWED", "method not allowed", http.StatusMethodNotAllowed))
	}
}

func (s *Server) chatSessionMessages(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if s.conversation == nil {
		apperrors.WriteHTTP(w, serviceUnavailable("会话存储未配置"))
		return
	}
	sessionID, ok := parseSessionMessagesPath(r.URL.Path)
	if !ok {
		apperrors.WriteHTTP(w, apperrors.New("NOT_FOUND", "not found", http.StatusNotFound))
		return
	}
	identity := identityFromRequest(r, r.URL.Query().Get("anonymous_id"))
	messages, err := s.conversation.listMessages(r.Context(), sessionID, identity, queryInt(r, "limit", 50))
	if err != nil {
		writeConversationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, messagesResponse{ChatSessionID: sessionID, Messages: messages})
}

func decodeSessionRequest(w http.ResponseWriter, r *http.Request) (sessionRequest, bool) {
	var body sessionRequest
	if r.Body == nil {
		return body, true
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		apperrors.WriteHTTP(w, apperrors.InvalidArgument("请求体不是合法 JSON"))
		return body, false
	}
	return body, true
}

func parseSessionMessagesPath(path string) (string, bool) {
	rest := strings.TrimPrefix(path, "/api/chat/sessions/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "messages" {
		return "", false
	}
	return parts[0], true
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func toSessionResponse(session *conversationSession) sessionResponse {
	resp := sessionResponse{
		ChatSessionID: session.ID,
		Title:         session.Title,
		CreatedAt:     session.CreatedAt.Format(timeFormat),
		UpdatedAt:     session.UpdatedAt.Format(timeFormat),
	}
	if session.LastMessageAt != nil {
		resp.LastMessageAt = session.LastMessageAt.Format(timeFormat)
	}
	return resp
}
