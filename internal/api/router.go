package api

import (
	"encoding/json"
	"net/http"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/agent/chatflow"
	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
	"smartinsure-eino-backend/internal/agent/smartinsureagent"
	"smartinsure-eino-backend/internal/agent/smartinsuredeep"
	"smartinsure-eino-backend/internal/config"
	apperrors "smartinsure-eino-backend/internal/errors"
	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/middleware"
	"smartinsure-eino-backend/internal/stream"
)

type Server struct {
	flow          chatflow.Runner
	agentRuntime  *agentruntime.Runtime
	agentSettings config.Settings
	conversation  *conversationService
}

func NewServer(flow chatflow.Runner) *Server {
	settings := config.Load()
	agentGraph := smartinsureagent.NewProductionGraph()
	if flow == nil {
		flow = chatflow.NewProductionRunner()
	} else if baseFlow, ok := flow.(*chatflow.Flow); ok {
		agentGraph = smartinsureagent.NewGraphFromFlow(baseFlow)
	}
	registry := agentruntime.NewRegistry()
	if err := registry.Register(smartinsureagent.New(agentGraph)); err != nil {
		logx.Printf("运行日志", "runtime log", "agent registry disabled: %v", err)
	}
	if deepAgent, err := smartinsuredeep.NewProduction(); err != nil {
		logx.Printf("运行日志", "runtime log", "deep agent registry disabled: %v", err)
	} else if err := registry.Register(deepAgent); err != nil {
		logx.Printf("运行日志", "runtime log", "deep agent registry disabled: %v", err)
	}
	if ragAgent, err := smartinsuredeep.NewRAGProduction(); err != nil {
		logx.Printf("运行日志", "runtime log", "rag agent registry disabled: %v", err)
	} else if err := registry.Register(ragAgent); err != nil {
		logx.Printf("运行日志", "runtime log", "rag agent registry disabled: %v", err)
	}
	conversation, err := newConversationService(settings)
	if err != nil {
		logx.Printf("运行日志", "runtime log", "conversation service disabled: %v", err)
	}
	return &Server{
		flow:          flow,
		agentRuntime:  agentruntime.New(registry),
		agentSettings: settings,
		conversation:  conversation,
	}
}

func NewHandler(flow chatflow.Runner) http.Handler {
	server := NewServer(flow)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/healthz", server.healthz)
	mux.HandleFunc("/api/suggestions", server.suggestions)
	mux.HandleFunc("/api/providers", server.providers)
	mux.HandleFunc("/api/chat/session/current", server.currentChatSession)
	mux.HandleFunc("/api/chat/sessions", server.chatSessions)
	mux.HandleFunc("/api/chat/sessions/", server.chatSessionMessages)
	mux.HandleFunc("/api/agent/chat", server.agentChat)
	mux.HandleFunc("/api/agent/deep-chat", server.agentDeepChat)
	mux.HandleFunc("/api/chat/rag-agent", server.agentRAGChat)
	mux.HandleFunc("/chat/rag-agent", server.agentRAGChat)
	mux.HandleFunc("/api/chat", server.chat)
	return middleware.Chain(mux, middleware.RequestID, middleware.CORS)
}

type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Time    string `json:"time"`
}

type suggestionsResponse struct {
	Suggestions []string `json:"suggestions"`
}

type providersResponse struct {
	Providers []ProviderInfo `json:"providers"`
	Available []string       `json:"available"`
}

type ProviderInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Model       string   `json:"model,omitempty"`
	Available   bool     `json:"available"`
	IsDefault   bool     `json:"is_default,omitempty"`
	Stages      []string `json:"stages,omitempty"`
}

type chatRequest struct {
	Message         string         `json:"message"`
	SessionID       string         `json:"sessionId"`
	ChatSessionID   string         `json:"chat_session_id"`
	AnonymousID     string         `json:"anonymous_id"`
	RequestID       string         `json:"requestId"`
	Action          string         `json:"action"`
	ProductURL      string         `json:"productUrl"`
	ProductURLAlt   string         `json:"product_url"`
	ProductName     string         `json:"productName"`
	ProductNameAlt  string         `json:"product_name"`
	AgentID         string         `json:"agent_id"`
	Stream          bool           `json:"stream"`
	IncludeThink    bool           `json:"include_think"`
	IncludeThinkAlt bool           `json:"includeThink"`
	Metadata        map[string]any `json:"metadata"`
}

var suggestions = []string{
	"重疾险和医疗险有什么区别？",
	"百万医疗险怎么选？",
	"什么是免赔额？",
	"意外险通常保哪些场景？",
	"等待期是什么意思？",
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Service: "backend",
		Time:    time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) suggestions(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, suggestionsResponse{Suggestions: suggestions})
}

func (s *Server) providers(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	settings := config.Load()
	registry, err := llm.LoadRegistry("configs/llm_providers.yaml", settings)
	if err != nil {
		writeJSON(w, http.StatusOK, providersResponse{Providers: []ProviderInfo{}, Available: []string{}})
		return
	}
	statuses := registry.ListProviders()
	providers := make([]ProviderInfo, 0, len(statuses))
	for _, status := range statuses {
		providers = append(providers, ProviderInfo{
			Name:        status.Name,
			Description: status.Description,
			Model:       status.Model,
			Available:   status.Available,
			IsDefault:   status.IsDefault,
			Stages:      status.Stages,
		})
	}
	writeJSON(w, http.StatusOK, providersResponse{Providers: providers, Available: registry.AvailableProviders()})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}

	var body chatRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		apperrors.WriteHTTP(w, apperrors.InvalidArgument("请求体不是合法 JSON"))
		return
	}
	if body.Action == "" && strings.TrimSpace(body.Message) == "" {
		apperrors.WriteHTTP(w, apperrors.InvalidArgument("message 不能为空"))
		return
	}

	requestID := body.RequestID
	if requestID == "" {
		requestID = middleware.RequestIDFromContext(r.Context())
	}
	if requestID == "" {
		requestID = r.Header.Get("X-Request-Id")
	}
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
	var history []chatflow.ChatMessage
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
		history, err = s.conversation.loadHistoryAfterUserMessage(r.Context(), sessionID, userMessage)
		if err != nil {
			writeConversationError(w, err)
			return
		}
	}
	logx.Printf("运行日志", "runtime log", "api chat start request_id=%s action=%s memory_enabled=%t session_id=%s history=%d product_url_present=%t product_name_present=%t message_chars=%d", requestID, body.Action, memoryEnabled, sessionID, len(history), strings.TrimSpace(body.ProductURL) != "", strings.TrimSpace(body.ProductName) != "", len([]rune(message)))

	writer, ok := stream.NewWriter(w)
	if !ok {
		apperrors.WriteHTTP(w, apperrors.Internal("当前 ResponseWriter 不支持流式刷新"))
		return
	}

	events := s.flow.Run(r.Context(), chatflow.Request{
		Message:       message,
		RequestID:     requestID,
		Action:        body.Action,
		ProductURL:    body.ProductURL,
		ProductName:   body.ProductName,
		AnonymousID:   identity.AnonymousID,
		UserID:        identity.UserID,
		ChatSessionID: sessionID,
		History:       history,
	})
	var assistant assistantAccumulator
	eventCount := 0
	defer func() {
		logx.Printf("运行日志", "runtime log", "api chat end request_id=%s action=%s events=%d memory_enabled=%t session_id=%s", requestID, body.Action, eventCount, memoryEnabled, sessionID)
	}()
	for event := range events {
		eventCount++
		if memoryEnabled {
			assistant.capture(event)
			event = withChatSessionID(event, sessionID)
		}
		if err := writer.Write(stream.Event{Name: event.Name, Data: event.Data}); err != nil {
			return
		}
	}
	if memoryEnabled && assistant.shouldPersist() {
		if _, err := s.conversation.appendMessage(r.Context(), session, "assistant", assistant.text(), assistant.metadataJSON()); err != nil {
			logx.Printf("运行日志", "runtime log", "failed to persist assistant message: %v", err)
		}
	}
}

func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	apperrors.WriteHTTP(w, apperrors.New("METHOD_NOT_ALLOWED", "method not allowed", http.StatusMethodNotAllowed))
	return false
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
