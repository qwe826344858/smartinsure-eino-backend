package rediscache

import "testing"

func TestTrimEntriesKeepsNewestInOrder(t *testing.T) {
	entries := []Entry{
		{ID: "msg_1", Payload: []byte("1")},
		{ID: "msg_2", Payload: []byte("2")},
		{ID: "msg_3", Payload: []byte("3")},
		{ID: "msg_4", Payload: []byte("4")},
	}

	got := TrimEntries(entries, 2)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "msg_3" || got[1].ID != "msg_4" {
		t.Fatalf("ids = [%s %s], want [msg_3 msg_4]", got[0].ID, got[1].ID)
	}
}

func TestTrimEntriesReturnsCopy(t *testing.T) {
	entries := []Entry{{ID: "msg_1", Payload: []byte("1")}}
	got := TrimEntries(entries, 10)
	got[0].ID = "changed"

	if entries[0].ID != "msg_1" {
		t.Fatalf("TrimEntries should not mutate source slice")
	}
}

func TestApplyDefaults(t *testing.T) {
	opts := applyDefaults(Options{})
	if opts.MaxMessages != DefaultMaxMessages {
		t.Fatalf("MaxMessages = %d, want %d", opts.MaxMessages, DefaultMaxMessages)
	}
	if opts.TTL != DefaultTTL {
		t.Fatalf("TTL = %s, want %s", opts.TTL, DefaultTTL)
	}
}
