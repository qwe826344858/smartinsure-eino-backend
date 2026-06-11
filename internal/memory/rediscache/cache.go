package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrCacheMiss         = errors.New("redis memory cache miss")
	ErrInconsistentCache = errors.New("redis memory cache inconsistent")
)

type Cache struct {
	client      redis.UniversalClient
	maxMessages int
	ttl         time.Duration
}

func New(client redis.UniversalClient, opts Options) (*Cache, error) {
	if client == nil {
		return nil, errors.New("redis client is nil")
	}
	opts = applyDefaults(opts)
	return &Cache{
		client:      client,
		maxMessages: opts.MaxMessages,
		ttl:         opts.TTL,
	}, nil
}

func NewClientFromURL(redisURL string) (*redis.Client, error) {
	if redisURL == "" {
		return nil, errors.New("redis url is empty")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	return redis.NewClient(opts), nil
}

func NewClientFromEnv() (*redis.Client, error) {
	return NewClientFromURL(os.Getenv("REDIS_URL"))
}

func NewFromURL(redisURL string, opts Options) (*Cache, error) {
	client, err := NewClientFromURL(redisURL)
	if err != nil {
		return nil, err
	}
	cache, err := New(client, opts)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return cache, nil
}

func NewFromEnv(opts Options) (*Cache, error) {
	client, err := NewClientFromEnv()
	if err != nil {
		return nil, err
	}
	cache, err := New(client, opts)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return cache, nil
}

func (c *Cache) Close() error {
	return c.client.Close()
}

func (c *Cache) AppendMessage(ctx context.Context, sessionID string, msg Message) error {
	if msg.ID == "" {
		return errors.New("message id is empty")
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	return c.AppendRawMessage(ctx, sessionID, Entry{ID: msg.ID, Payload: payload})
}

func (c *Cache) AppendRawMessage(ctx context.Context, sessionID string, entry Entry) error {
	if sessionID == "" {
		return errors.New("session id is empty")
	}
	if entry.ID == "" {
		return errors.New("message id is empty")
	}
	if len(entry.Payload) == 0 {
		return errors.New("message payload is empty")
	}
	script := redis.NewScript(appendMessageLua)
	if err := script.Run(
		ctx,
		c.client,
		[]string{MessageIDsKey(sessionID), MessagesKey(sessionID)},
		entry.ID,
		string(entry.Payload),
		c.maxMessages,
		ttlSeconds(c.ttl),
	).Err(); err != nil {
		return fmt.Errorf("append redis memory message: %w", err)
	}
	return nil
}

func (c *Cache) DeleteLastIfExpected(ctx context.Context, sessionID, expectedID string) (bool, error) {
	if sessionID == "" {
		return false, errors.New("session id is empty")
	}
	if expectedID == "" {
		return false, errors.New("expected message id is empty")
	}
	script := redis.NewScript(deleteLastIfExpectedLua)
	result, err := script.Run(
		ctx,
		c.client,
		[]string{MessageIDsKey(sessionID), MessagesKey(sessionID)},
		expectedID,
		ttlSeconds(c.ttl),
	).Int()
	if err != nil {
		return false, fmt.Errorf("delete redis memory message: %w", err)
	}
	return result == 1, nil
}

func (c *Cache) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	entries, err := c.GetRawMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages := make([]Message, 0, len(entries))
	for _, entry := range entries {
		var msg Message
		if err := json.Unmarshal(entry.Payload, &msg); err != nil {
			return nil, fmt.Errorf("%w: decode message %s: %v", ErrInconsistentCache, entry.ID, err)
		}
		if msg.ID == "" {
			msg.ID = entry.ID
		}
		if msg.ID != entry.ID {
			return nil, fmt.Errorf("%w: message id mismatch %s != %s", ErrInconsistentCache, entry.ID, msg.ID)
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func (c *Cache) GetRawMessages(ctx context.Context, sessionID string) ([]Entry, error) {
	if sessionID == "" {
		return nil, errors.New("session id is empty")
	}
	ids, err := c.client.LRange(ctx, MessageIDsKey(sessionID), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("read redis memory ids: %w", err)
	}
	if len(ids) == 0 {
		return nil, ErrCacheMiss
	}

	fields := make([]string, len(ids))
	copy(fields, ids)
	values, err := c.client.HMGet(ctx, MessagesKey(sessionID), fields...).Result()
	if err != nil {
		return nil, fmt.Errorf("read redis memory messages: %w", err)
	}
	if len(values) != len(ids) {
		return nil, fmt.Errorf("%w: ids/messages length mismatch", ErrInconsistentCache)
	}

	entries := make([]Entry, 0, len(ids))
	for i, value := range values {
		if value == nil {
			return nil, fmt.Errorf("%w: missing message %s", ErrInconsistentCache, ids[i])
		}
		payload, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: unexpected message payload type %T", ErrInconsistentCache, value)
		}
		if payload == "" {
			return nil, fmt.Errorf("%w: empty message %s", ErrInconsistentCache, ids[i])
		}
		entries = append(entries, Entry{ID: ids[i], Payload: []byte(payload)})
	}
	return entries, nil
}

func (c *Cache) RebuildMessages(ctx context.Context, sessionID string, messages []Message) error {
	entries := make([]Entry, 0, len(messages))
	for _, msg := range messages {
		if msg.ID == "" {
			return errors.New("message id is empty")
		}
		payload, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("marshal message %s: %w", msg.ID, err)
		}
		entries = append(entries, Entry{ID: msg.ID, Payload: payload})
	}
	return c.RebuildRawMessages(ctx, sessionID, entries)
}

func (c *Cache) RebuildRawMessages(ctx context.Context, sessionID string, entries []Entry) error {
	if sessionID == "" {
		return errors.New("session id is empty")
	}
	entries = TrimEntries(entries, c.maxMessages)

	idsKey := MessageIDsKey(sessionID)
	messagesKey := MessagesKey(sessionID)
	pipe := c.client.TxPipeline()
	pipe.Del(ctx, idsKey, messagesKey)
	if len(entries) > 0 {
		for _, entry := range entries {
			if entry.ID == "" {
				return errors.New("message id is empty")
			}
			if len(entry.Payload) == 0 {
				return errors.New("message payload is empty")
			}
			pipe.HSet(ctx, messagesKey, entry.ID, string(entry.Payload))
			pipe.RPush(ctx, idsKey, entry.ID)
		}
		if c.ttl > 0 {
			pipe.Expire(ctx, idsKey, c.ttl)
			pipe.Expire(ctx, messagesKey, c.ttl)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("rebuild redis memory messages: %w", err)
	}
	return nil
}

func (c *Cache) SetLatestAnonymousSession(ctx context.Context, anonymousID, sessionID string) error {
	if anonymousID == "" {
		return errors.New("anonymous id is empty")
	}
	if sessionID == "" {
		return errors.New("session id is empty")
	}
	key := LatestAnonymousSessionKey(anonymousID)
	if err := c.client.Set(ctx, key, sessionID, c.ttl).Err(); err != nil {
		return fmt.Errorf("set latest anonymous session: %w", err)
	}
	return nil
}

func (c *Cache) GetLatestAnonymousSession(ctx context.Context, anonymousID string) (string, error) {
	if anonymousID == "" {
		return "", errors.New("anonymous id is empty")
	}
	sessionID, err := c.client.Get(ctx, LatestAnonymousSessionKey(anonymousID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrCacheMiss
	}
	if err != nil {
		return "", fmt.Errorf("get latest anonymous session: %w", err)
	}
	if sessionID == "" {
		return "", ErrCacheMiss
	}
	return sessionID, nil
}

func ttlSeconds(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	return int64(ttl.Seconds())
}
