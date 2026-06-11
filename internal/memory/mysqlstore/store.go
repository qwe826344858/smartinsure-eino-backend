package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

var (
	ErrInvalidIdentity = errors.New("mysqlstore: user_id or anonymous_id is required")
	ErrNotFound        = errors.New("mysqlstore: not found")
	ErrForbidden       = errors.New("mysqlstore: forbidden")
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	return New(db), nil
}

func New(db *sql.DB) *Store {
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	for _, stmt := range splitSQLStatements(schemaSQL) {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, params CreateSessionParams) (*Session, error) {
	if !params.Identity.Valid() {
		return nil, ErrInvalidIdentity
	}
	now := s.now()
	title := strings.TrimSpace(params.Title)
	if title == "" {
		title = DefaultSessionTitle
	}
	session := &Session{
		ID:          newID("chat"),
		UserID:      params.Identity.UserID,
		AnonymousID: params.Identity.AnonymousID,
		Title:       title,
		Status:      SessionStatusActive,
		Metadata:    normalizeJSON(params.Metadata),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO chat_sessions (id, user_id, anonymous_id, title, status, metadata, created_at, updated_at, last_message_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		session.ID,
		nullString(session.UserID),
		nullString(session.AnonymousID),
		nullString(session.Title),
		session.Status,
		nullJSON(session.Metadata),
		session.CreatedAt,
		session.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Store) CurrentSession(ctx context.Context, identity Identity) (*Session, error) {
	session, err := s.latestSession(ctx, identity)
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.CreateSession(ctx, CreateSessionParams{Identity: identity})
}

func (s *Store) NewSession(ctx context.Context, identity Identity) (*Session, error) {
	return s.CreateSession(ctx, CreateSessionParams{Identity: identity})
}

func (s *Store) ListSessions(ctx context.Context, identity Identity, limit int) ([]Session, error) {
	if !identity.Valid() {
		return nil, ErrInvalidIdentity
	}
	limit = normalizeLimit(limit, 20, 100)
	if !identity.IsAuthenticated() {
		session, err := s.GetLatestAnonymousSession(ctx, identity.AnonymousID)
		if errors.Is(err, ErrNotFound) {
			return []Session{}, nil
		}
		if err != nil {
			return nil, err
		}
		return []Session{*session}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, user_id, anonymous_id, title, status, metadata, created_at, updated_at, last_message_at
FROM chat_sessions
WHERE user_id = ? AND status = ?
ORDER BY updated_at DESC
LIMIT ?`, identity.UserID, SessionStatusActive, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSessions(rows)
}

func (s *Store) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, user_id, anonymous_id, title, status, metadata, created_at, updated_at, last_message_at
FROM chat_sessions
WHERE id = ? AND status = ?`, sessionID, SessionStatusActive)
	return scanSession(row)
}

func (s *Store) GetLatestAnonymousSession(ctx context.Context, anonymousID string) (*Session, error) {
	if anonymousID == "" {
		return nil, ErrInvalidIdentity
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, user_id, anonymous_id, title, status, metadata, created_at, updated_at, last_message_at
FROM chat_sessions
WHERE anonymous_id = ? AND status = ?
ORDER BY updated_at DESC
LIMIT 1`, anonymousID, SessionStatusActive)
	return scanSession(row)
}

func (s *Store) ValidateAccess(ctx context.Context, sessionID string, identity Identity) error {
	if !identity.Valid() {
		return ErrInvalidIdentity
	}
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if canAccessSession(session, identity) {
		if !identity.IsAuthenticated() {
			latest, err := s.GetLatestAnonymousSession(ctx, identity.AnonymousID)
			if err != nil {
				return err
			}
			if latest.ID != session.ID {
				return ErrForbidden
			}
		}
		return nil
	}
	return ErrForbidden
}

func (s *Store) AppendMessage(ctx context.Context, params AppendMessageParams) (*Message, error) {
	if params.SessionID == "" {
		return nil, fmt.Errorf("mysqlstore: session_id is required")
	}
	if params.Role == "" {
		return nil, fmt.Errorf("mysqlstore: role is required")
	}
	if params.CreatedAt.IsZero() {
		params.CreatedAt = s.now()
	}
	message := &Message{
		ID:        newID("msg"),
		SessionID: params.SessionID,
		Role:      params.Role,
		Content:   params.Content,
		Metadata:  normalizeJSON(params.Metadata),
		CreatedAt: params.CreatedAt,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `
INSERT INTO chat_messages (id, session_id, role, content, metadata, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		message.ID,
		message.SessionID,
		message.Role,
		message.Content,
		nullJSON(message.Metadata),
		message.CreatedAt,
	); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE chat_sessions
SET updated_at = ?, last_message_at = ?
WHERE id = ? AND status = ?`, message.CreatedAt, message.CreatedAt, message.SessionID, SessionStatusActive); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return message, nil
}

func (s *Store) ListMessages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	limit = normalizeLimit(limit, 50, 200)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, session_id, role, content, metadata, created_at
FROM chat_messages
WHERE session_id = ?
ORDER BY created_at ASC
LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *Store) ListRecentMessages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	limit = normalizeLimit(limit, 20, 100)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, session_id, role, content, metadata, created_at
FROM chat_messages
WHERE session_id = ?
ORDER BY created_at DESC
LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	reverseMessages(messages)
	return messages, nil
}

func (s *Store) TouchSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE chat_sessions
SET updated_at = ?
WHERE id = ? AND status = ?`, s.now(), sessionID, SessionStatusActive)
	return err
}

func (s *Store) UpdateSession(ctx context.Context, sessionID string, params UpdateSessionParams) error {
	if params.Title == nil && params.Metadata == nil {
		return s.TouchSession(ctx, sessionID)
	}
	sets := []string{"updated_at = ?"}
	args := []any{s.now()}
	if params.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, nullString(strings.TrimSpace(*params.Title)))
	}
	if params.Metadata != nil {
		metadata := normalizeJSON(*params.Metadata)
		sets = append(sets, "metadata = ?")
		args = append(args, nullJSON(metadata))
	}
	args = append(args, sessionID, SessionStatusActive)
	_, err := s.db.ExecContext(ctx, `
UPDATE chat_sessions
SET `+strings.Join(sets, ", ")+`
WHERE id = ? AND status = ?`, args...)
	return err
}

func (s *Store) UpdateSessionTitle(ctx context.Context, sessionID string, title string) error {
	return s.UpdateSession(ctx, sessionID, UpdateSessionParams{Title: &title})
}

func (s *Store) UpdateSessionMetadata(ctx context.Context, sessionID string, metadata json.RawMessage) error {
	return s.UpdateSession(ctx, sessionID, UpdateSessionParams{Metadata: &metadata})
}

func (s *Store) latestSession(ctx context.Context, identity Identity) (*Session, error) {
	if !identity.Valid() {
		return nil, ErrInvalidIdentity
	}
	if identity.IsAuthenticated() {
		row := s.db.QueryRowContext(ctx, `
SELECT id, user_id, anonymous_id, title, status, metadata, created_at, updated_at, last_message_at
FROM chat_sessions
WHERE user_id = ? AND status = ?
ORDER BY updated_at DESC
LIMIT 1`, identity.UserID, SessionStatusActive)
		return scanSession(row)
	}
	return s.GetLatestAnonymousSession(ctx, identity.AnonymousID)
}

func canAccessSession(session *Session, identity Identity) bool {
	if session == nil || !identity.Valid() {
		return false
	}
	if identity.IsAuthenticated() {
		return session.UserID == identity.UserID
	}
	return session.AnonymousID == identity.AnonymousID
}

func newID(prefix string) string {
	return prefix + "_" + uuid.NewString()
}

func normalizeLimit(limit, fallback, max int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > max {
		return max
	}
	return limit
}

func normalizeJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	copied := make(json.RawMessage, len(raw))
	copy(copied, raw)
	return copied
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func nullJSON(value json.RawMessage) sql.NullString {
	if len(value) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: string(value), Valid: true}
}

func splitSQLStatements(sqlText string) []string {
	parts := strings.Split(sqlText, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt != "" {
			statements = append(statements, stmt)
		}
	}
	return statements
}

func reverseMessages(messages []Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}
