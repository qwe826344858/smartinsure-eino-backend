package mysqlstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSplitSQLStatementsIncludesBothTables(t *testing.T) {
	statements := splitSQLStatements(schemaSQL)
	if len(statements) != 2 {
		t.Fatalf("expected 2 schema statements, got %d", len(statements))
	}
	if !strings.Contains(statements[0], "CREATE TABLE IF NOT EXISTS chat_sessions") {
		t.Fatalf("sessions schema missing: %s", statements[0])
	}
	if !strings.Contains(statements[1], "CREATE TABLE IF NOT EXISTS chat_messages") {
		t.Fatalf("messages schema missing: %s", statements[1])
	}
	if !strings.Contains(statements[0], "metadata JSON NULL") || !strings.Contains(statements[1], "metadata JSON NULL") {
		t.Fatal("schema should use MySQL JSON columns for metadata")
	}
}

func TestNormalizeLimit(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "fallback", in: 0, want: 20},
		{name: "negative fallback", in: -1, want: 20},
		{name: "value", in: 10, want: 10},
		{name: "cap", in: 500, want: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeLimit(tt.in, 20, 100); got != tt.want {
				t.Fatalf("normalizeLimit(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestCanAccessSession(t *testing.T) {
	session := &Session{ID: "chat_1", UserID: "user_1", AnonymousID: "anon_1"}
	if !canAccessSession(session, Identity{UserID: "user_1"}) {
		t.Fatal("authenticated owner should access session")
	}
	if canAccessSession(session, Identity{UserID: "user_2"}) {
		t.Fatal("different authenticated user should not access session")
	}
	if !canAccessSession(session, Identity{AnonymousID: "anon_1"}) {
		t.Fatal("anonymous owner should access session before latest-session check")
	}
	if canAccessSession(session, Identity{AnonymousID: "anon_2"}) {
		t.Fatal("different anonymous user should not access session")
	}
}

func TestReverseMessages(t *testing.T) {
	messages := []Message{{ID: "msg_3"}, {ID: "msg_2"}, {ID: "msg_1"}}
	reverseMessages(messages)
	if messages[0].ID != "msg_1" || messages[2].ID != "msg_3" {
		t.Fatalf("messages not reversed: %+v", messages)
	}
}

func TestNullJSON(t *testing.T) {
	empty := nullJSON(nil)
	if empty.Valid {
		t.Fatal("empty metadata should map to SQL NULL")
	}
	metadata := nullJSON(json.RawMessage(`{"source":"test"}`))
	if !metadata.Valid || metadata.String != `{"source":"test"}` {
		t.Fatalf("unexpected metadata value: %+v", metadata)
	}
}

func TestNewIDPrefixes(t *testing.T) {
	if id := newID("chat"); !strings.HasPrefix(id, "chat_") {
		t.Fatalf("chat id missing prefix: %s", id)
	}
	if id := newID("msg"); !strings.HasPrefix(id, "msg_") {
		t.Fatalf("message id missing prefix: %s", id)
	}
}

func TestStoreClockIsUTCByDefault(t *testing.T) {
	store := New(nil)
	now := store.now()
	if now.Location() != time.UTC {
		t.Fatalf("default clock should use UTC, got %s", now.Location())
	}
}
