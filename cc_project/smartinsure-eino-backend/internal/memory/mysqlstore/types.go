package mysqlstore

import (
	"encoding/json"
	"time"
)

const (
	DefaultSessionTitle  = "新会话"
	SessionStatusActive  = "active"
	SessionStatusDeleted = "deleted"
)

type Identity struct {
	UserID      string
	AnonymousID string
}

func (i Identity) IsAuthenticated() bool {
	return i.UserID != ""
}

func (i Identity) Valid() bool {
	return i.UserID != "" || i.AnonymousID != ""
}

type Session struct {
	ID            string
	UserID        string
	AnonymousID   string
	Title         string
	Status        string
	Metadata      json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastMessageAt *time.Time
}

type Message struct {
	ID        string
	SessionID string
	Role      string
	Content   string
	Metadata  json.RawMessage
	CreatedAt time.Time
}

type CreateSessionParams struct {
	Identity Identity
	Title    string
	Metadata json.RawMessage
}

type AppendMessageParams struct {
	SessionID string
	Role      string
	Content   string
	Metadata  json.RawMessage
	CreatedAt time.Time
}

type UpdateSessionParams struct {
	Title    *string
	Metadata *json.RawMessage
}
