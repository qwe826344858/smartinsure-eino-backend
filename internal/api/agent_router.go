package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"smartinsure-eino-backend/internal/logx"
	"strings"

	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
	"smartinsure-eino-backend/internal/agent/smartinsuredeep"
	apperrors "smartinsure-eino-backend/internal/errors"
	"smartinsure-eino-backend/internal/middleware"
	"smartinsure-eino-backend/internal/stream"
)

// agentChat 是通用 AgentRuntime 入口，默认使用配置中的 AGENT_DEFAULT_ID。
func (s *Server) agentChat(w http.ResponseWriter, r *http.Request) {
	s.serveAgentChat(w, r, "")
}

// agentDeepChat 是 DeepAgent 专用入口，强制路由到 smartinsure-deep-advisor。
func (s *Server) agentDeepChat(w http.ResponseWriter, r *http.Request) {
	s.serveAgentChat(w, r, smartinsuredeep.DefaultID)
}

// agentRAGChat 是 RAG 产品匹配 Agent 入口，强制路由到 smartinsure-rag-advisor。
func (s *Server) agentRAGChat(w http.ResponseWriter, r *http.Request) {
	s.serveAgentChat(w, r, smartinsuredeep.RAGAgentID)
}

// serveAgentChat 负责 Agent/DeepAgent 共享的 HTTP -> AgentRuntime -> SSE 全链路。
func (s *Server) serveAgentChat(w http.ResponseWriter, r *http.Request, forcedAgentID string) {
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
	// 兼容前端 productUrl/product_url、productName/product_name 两套字段命名。
	body.normalizeAgentFields()
	if body.Action == "" && strings.TrimSpace(body.Message) == "" {
		apperrors.WriteHTTP(w, apperrors.InvalidArgument("message 不能为空"))
		return
	}

	requestID := requestIDFromHTTP(r, body.RequestID)
	if requestID != "" {
		w.Header().Set("X-Request-Id", requestID)
	}

	// 解析会话身份。带 session 或身份时启用会话持久化和历史读取；否则按无状态请求处理。
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
		// 历史消息裁剪到 Agent 记忆窗口，避免 DeepAgent prompt 过长。
		chatHistory, err := s.conversation.loadHistoryAfterUserMessage(r.Context(), sessionID, userMessage)
		if err != nil {
			writeConversationError(w, err)
			return
		}
		history = limitAgentHistory(toAgentHistory(chatHistory), s.agentSettings.AgentMemoryWindow)
	}

	// Agent 接口必须使用 SSE writer，后续事件直接透传给前端。
	writer, ok := stream.NewWriter(w)
	if !ok {
		apperrors.WriteHTTP(w, apperrors.Internal("当前 ResponseWriter 不支持流式刷新"))
		return
	}

	// forcedAgentID 非空时说明命中 /api/agent/deep-chat，忽略请求里的 agent_id。
	agentID := strings.TrimSpace(forcedAgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(body.AgentID)
		if agentID == "" {
			agentID = s.agentSettings.AgentDefaultID
		}
	}
	logx.Printf("运行日志", "runtime log", "api agent_chat start request_id=%s agent_id=%s forced_agent=%t action=%s memory_enabled=%t session_id=%s history=%d product_url_present=%t product_name_present=%t message_chars=%d", requestID, agentID, strings.TrimSpace(forcedAgentID) != "", body.Action, memoryEnabled, sessionID, len(history), strings.TrimSpace(body.ProductURL) != "", strings.TrimSpace(body.ProductName) != "", len([]rune(message)))
	// 构造 AgentRuntime 请求。DeepAgent 具体执行逻辑在 smartinsuredeep.Agent.Run。
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
		IncludeThink:  body.IncludeThink,
	})
	if err != nil {
		if errors.Is(err, agentruntime.ErrAgentNotFound) {
			logx.Printf("运行日志", "runtime log", "api agent_chat runtime_error request_id=%s agent_id=%s err=agent_not_found", requestID, agentID)
			apperrors.WriteHTTP(w, apperrors.New("NOT_FOUND", "Agent 不存在", http.StatusNotFound))
			return
		}
		logx.Printf("运行日志", "runtime log", "api agent_chat runtime_error request_id=%s agent_id=%s err=%v", requestID, agentID, err)
		apperrors.WriteHTTP(w, apperrors.Internal(err.Error()))
		return
	}

	var assistant assistantAccumulator
	eventCount := 0
	defer func() {
		logx.Printf("运行日志", "runtime log", "api agent_chat end request_id=%s agent_id=%s action=%s events=%d memory_enabled=%t session_id=%s", requestID, agentID, body.Action, eventCount, memoryEnabled, sessionID)
	}()
	for event := range events {
		eventCount++
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
		// 只有生成了有效 assistant 内容时才持久化，避免 status/done 这类控制事件污染会话。
		if _, err := s.conversation.appendMessage(r.Context(), session, "assistant", assistant.text(), assistant.metadataJSON()); err != nil {
			logx.Printf("运行日志", "runtime log", "failed to persist assistant message: %v", err)
		}
	}
}

// normalizeAgentFields 兼容蛇形和驼峰命名，避免前端字段变体导致工具拿不到 URL/产品名。
func (r *chatRequest) normalizeAgentFields() {
	if r.ProductURL == "" {
		r.ProductURL = r.ProductURLAlt
	}
	if r.ProductName == "" {
		r.ProductName = r.ProductNameAlt
	}
	if r.IncludeThinkAlt {
		r.IncludeThink = true
	}
}

// requestIDFromHTTP 按请求体、middleware、Header 的优先级提取 request_id。
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
