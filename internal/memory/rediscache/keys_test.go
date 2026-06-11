package rediscache

import "testing"

func TestKeys(t *testing.T) {
	sessionID := "chat_123"
	anonymousID := "anon_456"

	tests := map[string]string{
		"ids":            MessageIDsKey(sessionID),
		"messages":       MessagesKey(sessionID),
		"state":          SessionStateKey(sessionID),
		"owner":          SessionOwnerKey(sessionID),
		"latest_session": LatestAnonymousSessionKey(anonymousID),
	}

	want := map[string]string{
		"ids":            "chat:session:chat_123:message_ids",
		"messages":       "chat:session:chat_123:messages",
		"state":          "chat:session:chat_123:state",
		"owner":          "chat:session:chat_123:owner",
		"latest_session": "chat:anon:anon_456:latest_session",
	}

	for name, got := range tests {
		if got != want[name] {
			t.Fatalf("%s key = %q, want %q", name, got, want[name])
		}
	}
}
