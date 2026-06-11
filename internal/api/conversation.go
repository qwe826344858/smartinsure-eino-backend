package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"
	"unicode/utf8"

	"smartinsure-eino-backend/internal/agent/chatflow"
	"smartinsure-eino-backend/internal/config"
	apperrors "smartinsure-eino-backend/internal/errors"
	"smartinsure-eino-backend/internal/memory/mysqlstore"
	"smartinsure-eino-backend/internal/memory/rediscache"
)

const headerUserID = "X-User-Id"

var errStaleAnonymousSession = errors.New("anonymous session is not latest")

type conversationService struct {
	store           *mysqlstore.Store
	cache           *rediscache.Cache
	memoryLimit     int
	memoryMaxChars  int
	cacheConfigured bool
}

type conversationSession struct {
	ID            string          `json:"chat_session_id"`
	UserID        string          `json:"-"`
	AnonymousID   string          `json:"-"`
	Title         string          `json:"title"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	LastMessageAt *time.Time      `json:"last_message_at,omitempty"`
}

type persistedMessage struct {
	ID        string          `json:"id"`
	SessionID string          `json:"-"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

func newConversationService(settings config.Settings) (*conversationService, error) {
	if strings.TrimSpace(settings.MySQLDSN) == "" {
		return nil, nil
	}
	store, err := mysqlstore.Open(settings.MySQLDSN)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.EnsureSchema(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}

	var cache *rediscache.Cache
	if strings.TrimSpace(settings.RedisURL) != "" {
		cache, err = rediscache.NewFromURL(settings.RedisURL, rediscache.Options{
			MaxMessages: settings.MemoryMessageLimit,
			TTL:         rediscache.DefaultTTL,
		})
		if err != nil {
			logx.Printf("运行日志", "runtime log", "redis memory cache disabled: %v", err)
		}
	}
	return &conversationService{
		store:           store,
		cache:           cache,
		memoryLimit:     positiveOrDefault(settings.MemoryMessageLimit, rediscache.DefaultMaxMessages),
		memoryMaxChars:  positiveOrDefault(settings.MemoryMaxChars, 12000),
		cacheConfigured: cache != nil,
	}, nil
}

func identityFromRequest(r *http.Request, anonymousID string) mysqlstore.Identity {
	return mysqlstore.Identity{
		UserID:      strings.TrimSpace(r.Header.Get(headerUserID)),
		AnonymousID: strings.TrimSpace(anonymousID),
	}
}

func (c *conversationService) prepareSession(ctx context.Context, sessionID string, identity mysqlstore.Identity) (*conversationSession, error) {
	if c == nil || c.store == nil {
		return nil, serviceUnavailable("会话存储未配置")
	}
	if !identity.Valid() {
		return nil, mysqlstore.ErrInvalidIdentity
	}
	if strings.TrimSpace(sessionID) == "" {
		session, err := c.store.CurrentSession(ctx, identity)
		if err != nil {
			return nil, err
		}
		c.cacheLatestAnonymousSession(ctx, identity, session.ID)
		return toConversationSession(session), nil
	}
	session, err := c.validateAccess(ctx, sessionID, identity)
	if err != nil {
		return nil, err
	}
	return toConversationSession(session), nil
}

func (c *conversationService) currentSession(ctx context.Context, identity mysqlstore.Identity) (*conversationSession, error) {
	session, err := c.store.CurrentSession(ctx, identity)
	if err != nil {
		return nil, err
	}
	c.cacheLatestAnonymousSession(ctx, identity, session.ID)
	return toConversationSession(session), nil
}

func (c *conversationService) newSession(ctx context.Context, identity mysqlstore.Identity) (*conversationSession, error) {
	session, err := c.store.NewSession(ctx, identity)
	if err != nil {
		return nil, err
	}
	c.cacheLatestAnonymousSession(ctx, identity, session.ID)
	return toConversationSession(session), nil
}

func (c *conversationService) listSessions(ctx context.Context, identity mysqlstore.Identity, limit int) ([]conversationSession, error) {
	sessions, err := c.store.ListSessions(ctx, identity, limit)
	if err != nil {
		return nil, err
	}
	out := make([]conversationSession, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, *toConversationSession(&session))
	}
	return out, nil
}

func (c *conversationService) listMessages(ctx context.Context, sessionID string, identity mysqlstore.Identity, limit int) ([]persistedMessage, error) {
	if _, err := c.validateAccess(ctx, sessionID, identity); err != nil {
		return nil, err
	}
	messages, err := c.store.ListMessages(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]persistedMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, *toPersistedMessage(&msg))
	}
	return out, nil
}

func (c *conversationService) validateAccess(ctx context.Context, sessionID string, identity mysqlstore.Identity) (*mysqlstore.Session, error) {
	if !identity.Valid() {
		return nil, mysqlstore.ErrInvalidIdentity
	}
	session, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if identity.IsAuthenticated() {
		if session.UserID != identity.UserID {
			return nil, mysqlstore.ErrForbidden
		}
		return session, nil
	}
	if session.AnonymousID != identity.AnonymousID {
		return nil, mysqlstore.ErrForbidden
	}
	latest, err := c.store.GetLatestAnonymousSession(ctx, identity.AnonymousID)
	if err != nil {
		return nil, err
	}
	if latest.ID != session.ID {
		return nil, errStaleAnonymousSession
	}
	return session, nil
}

func (c *conversationService) appendMessage(ctx context.Context, session *conversationSession, role, content string, metadata json.RawMessage) (*persistedMessage, error) {
	return c.appendMessageWithCache(ctx, session, role, content, metadata, true)
}

func (c *conversationService) appendUserMessage(ctx context.Context, session *conversationSession, content string, metadata json.RawMessage) (*persistedMessage, error) {
	return c.appendMessageWithCache(ctx, session, "user", content, metadata, false)
}

func (c *conversationService) appendMessageWithCache(ctx context.Context, session *conversationSession, role, content string, metadata json.RawMessage, updateCache bool) (*persistedMessage, error) {
	if session == nil || session.ID == "" {
		return nil, mysqlstore.ErrNotFound
	}
	msg, err := c.store.AppendMessage(ctx, mysqlstore.AppendMessageParams{
		SessionID: session.ID,
		Role:      role,
		Content:   content,
		Metadata:  metadata,
	})
	if err != nil {
		return nil, err
	}
	if role == "user" && shouldUpdateSessionTitle(session.Title) {
		title := titleFromMessage(content)
		if title != "" {
			if err := c.store.UpdateSessionTitle(ctx, session.ID, title); err != nil {
				logx.Printf("运行日志", "runtime log", "failed to update chat session title: %v", err)
			} else {
				session.Title = title
			}
		}
	}
	persisted := toPersistedMessage(msg)
	if updateCache {
		c.appendCache(ctx, persisted)
	}
	return persisted, nil
}

func (c *conversationService) loadHistoryAfterUserMessage(ctx context.Context, sessionID string, userMessage *persistedMessage) ([]chatflow.ChatMessage, error) {
	if userMessage == nil {
		return c.loadHistory(ctx, sessionID, "")
	}
	if c.cache != nil {
		cached, err := c.cache.GetMessages(ctx, sessionID)
		if err == nil {
			c.appendCache(ctx, userMessage)
			return trimHistoryByChars(chatflowFromPersisted(persistedFromRedis(cached), ""), c.memoryMaxChars), nil
		}
		if !errors.Is(err, rediscache.ErrCacheMiss) && !errors.Is(err, rediscache.ErrInconsistentCache) {
			logx.Printf("运行日志", "runtime log", "redis memory read failed before user append, falling back to mysql: %v", err)
		}
	}
	messages, err := c.store.ListRecentMessages(ctx, sessionID, c.memoryLimit)
	if err != nil {
		return nil, err
	}
	out := make([]persistedMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, *toPersistedMessage(&msg))
	}
	c.rebuildCache(ctx, sessionID, out)
	return trimHistoryByChars(chatflowFromPersisted(out, userMessage.ID), c.memoryMaxChars), nil
}

func (c *conversationService) loadHistory(ctx context.Context, sessionID, excludeMessageID string) ([]chatflow.ChatMessage, error) {
	messages, err := c.loadCachedMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return trimHistoryByChars(chatflowFromPersisted(messages, excludeMessageID), c.memoryMaxChars), nil
}

func chatflowFromPersisted(messages []persistedMessage, excludeMessageID string) []chatflow.ChatMessage {
	history := make([]chatflow.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.ID == excludeMessageID {
			continue
		}
		history = append(history, toChatflowMessage(msg))
	}
	return history
}

func (c *conversationService) loadCachedMessages(ctx context.Context, sessionID string) ([]persistedMessage, error) {
	if c.cache != nil {
		cached, err := c.cache.GetMessages(ctx, sessionID)
		if err == nil {
			return persistedFromRedis(cached), nil
		}
		if !errors.Is(err, rediscache.ErrCacheMiss) && !errors.Is(err, rediscache.ErrInconsistentCache) {
			logx.Printf("运行日志", "runtime log", "redis memory read failed, falling back to mysql: %v", err)
		}
	}
	messages, err := c.store.ListRecentMessages(ctx, sessionID, c.memoryLimit)
	if err != nil {
		return nil, err
	}
	out := make([]persistedMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, *toPersistedMessage(&msg))
	}
	c.rebuildCache(ctx, sessionID, out)
	return out, nil
}

func (c *conversationService) appendCache(ctx context.Context, msg *persistedMessage) {
	if c.cache == nil || msg == nil {
		return
	}
	if err := c.cache.AppendMessage(ctx, msg.SessionID, rediscache.Message{
		ID:        msg.ID,
		Role:      msg.Role,
		Content:   msg.Content,
		Metadata:  msg.Metadata,
		CreatedAt: msg.CreatedAt,
	}); err != nil {
		logx.Printf("运行日志", "runtime log", "redis memory append failed: %v", err)
	}
}

func (c *conversationService) rebuildCache(ctx context.Context, sessionID string, messages []persistedMessage) {
	if c.cache == nil {
		return
	}
	redisMessages := make([]rediscache.Message, 0, len(messages))
	for _, msg := range messages {
		redisMessages = append(redisMessages, rediscache.Message{
			ID:        msg.ID,
			Role:      msg.Role,
			Content:   msg.Content,
			Metadata:  msg.Metadata,
			CreatedAt: msg.CreatedAt,
		})
	}
	if err := c.cache.RebuildMessages(ctx, sessionID, redisMessages); err != nil {
		logx.Printf("运行日志", "runtime log", "redis memory rebuild failed: %v", err)
	}
}

func (c *conversationService) cacheLatestAnonymousSession(ctx context.Context, identity mysqlstore.Identity, sessionID string) {
	if c.cache == nil || identity.IsAuthenticated() || identity.AnonymousID == "" || sessionID == "" {
		return
	}
	if err := c.cache.SetLatestAnonymousSession(ctx, identity.AnonymousID, sessionID); err != nil {
		logx.Printf("运行日志", "runtime log", "redis latest anonymous session cache failed: %v", err)
	}
}

func toConversationSession(session *mysqlstore.Session) *conversationSession {
	if session == nil {
		return nil
	}
	return &conversationSession{
		ID:            session.ID,
		UserID:        session.UserID,
		AnonymousID:   session.AnonymousID,
		Title:         session.Title,
		Metadata:      session.Metadata,
		CreatedAt:     session.CreatedAt,
		UpdatedAt:     session.UpdatedAt,
		LastMessageAt: session.LastMessageAt,
	}
}

func toPersistedMessage(msg *mysqlstore.Message) *persistedMessage {
	if msg == nil {
		return nil
	}
	return &persistedMessage{
		ID:        msg.ID,
		SessionID: msg.SessionID,
		Role:      msg.Role,
		Content:   msg.Content,
		Metadata:  msg.Metadata,
		CreatedAt: msg.CreatedAt,
	}
}

func persistedFromRedis(messages []rediscache.Message) []persistedMessage {
	out := make([]persistedMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, persistedMessage{
			ID:        msg.ID,
			Role:      msg.Role,
			Content:   msg.Content,
			Metadata:  msg.Metadata,
			CreatedAt: msg.CreatedAt,
		})
	}
	return out
}

func toChatflowMessage(msg persistedMessage) chatflow.ChatMessage {
	return chatflow.ChatMessage{
		ID:        msg.ID,
		Role:      msg.Role,
		Content:   msg.Content,
		Metadata:  rawMetadataMap(msg.Metadata),
		CreatedAt: msg.CreatedAt,
	}
}

func rawMetadataMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil
	}
	return data
}

func trimHistoryByChars(history []chatflow.ChatMessage, maxChars int) []chatflow.ChatMessage {
	if maxChars <= 0 || len(history) == 0 {
		return history
	}
	total := 0
	start := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		count := utf8.RuneCountInString(history[i].Content)
		if start != len(history) && total+count > maxChars {
			break
		}
		total += count
		start = i
		if total >= maxChars {
			break
		}
	}
	if start < 0 {
		start = 0
	}
	return append([]chatflow.ChatMessage(nil), history[start:]...)
}

func shouldUpdateSessionTitle(title string) bool {
	title = strings.TrimSpace(title)
	return title == "" || title == mysqlstore.DefaultSessionTitle
}

func titleFromMessage(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) == 0 {
		return ""
	}
	if len(runes) > 20 {
		runes = runes[:20]
	}
	return string(runes)
}

func positiveOrDefault(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func serviceUnavailable(message string) *apperrors.AppError {
	if message == "" {
		message = "服务暂不可用"
	}
	return apperrors.New("SERVICE_UNAVAILABLE", message, http.StatusServiceUnavailable)
}

func writeConversationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mysqlstore.ErrInvalidIdentity):
		apperrors.WriteHTTP(w, apperrors.InvalidArgument("缺少 anonymous_id 或登录用户标识"))
	case errors.Is(err, mysqlstore.ErrNotFound):
		apperrors.WriteHTTP(w, apperrors.New("NOT_FOUND", "会话不存在", http.StatusNotFound))
	case errors.Is(err, errStaleAnonymousSession):
		apperrors.WriteHTTP(w, apperrors.New("SESSION_STALE", "匿名用户只能访问最新会话，请重新获取当前会话", http.StatusConflict))
	case errors.Is(err, mysqlstore.ErrForbidden):
		apperrors.WriteHTTP(w, apperrors.New("FORBIDDEN", "无权访问该会话", http.StatusForbidden))
	default:
		if appErr, ok := err.(*apperrors.AppError); ok {
			apperrors.WriteHTTP(w, appErr)
			return
		}
		apperrors.WriteHTTP(w, apperrors.Internal(err.Error()))
	}
}
