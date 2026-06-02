package rediscache

import (
	"encoding/json"
	"time"
)

const (
	DefaultMaxMessages = 20
	DefaultTTL         = 7 * 24 * time.Hour
)

type Options struct {
	MaxMessages int
	TTL         time.Duration
}

type Message struct {
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type Entry struct {
	ID      string
	Payload []byte
}

func applyDefaults(opts Options) Options {
	if opts.MaxMessages <= 0 {
		opts.MaxMessages = DefaultMaxMessages
	}
	if opts.TTL == 0 {
		opts.TTL = DefaultTTL
	}
	return opts
}

func TrimEntries(entries []Entry, maxMessages int) []Entry {
	if maxMessages <= 0 || len(entries) <= maxMessages {
		return append([]Entry(nil), entries...)
	}
	return append([]Entry(nil), entries[len(entries)-maxMessages:]...)
}
