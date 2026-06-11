package productdetail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"smartinsure-eino-backend/internal/schema"
)

var ErrDetailHotCacheInconsistent = errors.New("product detail hot cache inconsistent")

type RedisHotCache struct {
	client   redis.UniversalClient
	now      func() time.Time
	newOwner func() (string, error)
}

type RedisDetailHotCache = RedisHotCache

type detailHotCacheEnvelope struct {
	ProductKey    string               `json:"product_key"`
	Platform      string               `json:"platform,omitempty"`
	CanonicalURL  string               `json:"canonical_url,omitempty"`
	Detail        schema.ProductDetail `json:"detail"`
	SourceHash    string               `json:"source_hash,omitempty"`
	PromptVersion string               `json:"prompt_version,omitempty"`
	ModelName     string               `json:"model_name,omitempty"`
	Status        string               `json:"status,omitempty"`
	ExpiresAt     time.Time            `json:"expires_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
}

type redisProductDetailLock struct {
	client redis.UniversalClient
	key    string
	owner  string
}

var _ DetailHotCache = (*RedisHotCache)(nil)

const releaseProductDetailLockLua = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`

func NewRedisHotCache(client redis.UniversalClient) (*RedisHotCache, error) {
	if client == nil {
		return nil, errors.New("redis client is nil")
	}
	return &RedisHotCache{
		client:   client,
		now:      time.Now,
		newOwner: newRedisLockOwner,
	}, nil
}

func NewRedisDetailHotCache(client redis.UniversalClient) (*RedisHotCache, error) {
	return NewRedisHotCache(client)
}

func NewRedisHotCacheFromURL(redisURL string) (*RedisHotCache, error) {
	if strings.TrimSpace(redisURL) == "" {
		return nil, errors.New("redis url is empty")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	return NewRedisHotCache(redis.NewClient(opts))
}

func NewRedisHotCacheFromEnv() (*RedisHotCache, error) {
	return NewRedisHotCacheFromURL(os.Getenv("REDIS_URL"))
}

func NewRedisDetailHotCacheFromURL(redisURL string) (*RedisHotCache, error) {
	return NewRedisHotCacheFromURL(redisURL)
}

func (c *RedisHotCache) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

func (c *RedisHotCache) Get(ctx context.Context, productKey string) (DetailRecord, bool, error) {
	productKey, err := requireRedisHotCacheNonEmpty("product key", productKey)
	if err != nil {
		return DetailRecord{}, false, err
	}
	if c == nil || c.client == nil {
		return DetailRecord{}, false, errors.New("redis hot cache is nil")
	}

	payload, err := c.client.Get(ctx, productDetailRedisDetailKey(productKey)).Bytes()
	if errors.Is(err, redis.Nil) {
		return DetailRecord{}, false, nil
	}
	if err != nil {
		return DetailRecord{}, false, fmt.Errorf("get product detail hot cache: %w", err)
	}
	record, ok, err := decodeDetailHotCacheEnvelope(payload, productKey, c.clockNow())
	if err != nil {
		return DetailRecord{}, false, err
	}
	return record, ok, nil
}

func (c *RedisHotCache) Set(ctx context.Context, record DetailRecord, ttl time.Duration) error {
	productKey, err := requireRedisHotCacheNonEmpty("product key", record.ProductKey)
	if err != nil {
		return err
	}
	if err := requireRedisHotCachePositiveTTL(ttl); err != nil {
		return err
	}
	if c == nil || c.client == nil {
		return errors.New("redis hot cache is nil")
	}

	record.ProductKey = productKey
	payload, err := encodeDetailHotCacheEnvelope(record, ttl, c.clockNow())
	if err != nil {
		return err
	}
	if err := c.client.Set(ctx, productDetailRedisDetailKey(productKey), string(payload), ttl).Err(); err != nil {
		return fmt.Errorf("set product detail hot cache: %w", err)
	}
	return nil
}

func (c *RedisHotCache) GetAlias(ctx context.Context, normalizedURLHash string) (string, bool, error) {
	normalizedURLHash, err := requireRedisHotCacheNonEmpty("normalized url hash", normalizedURLHash)
	if err != nil {
		return "", false, err
	}
	if c == nil || c.client == nil {
		return "", false, errors.New("redis hot cache is nil")
	}

	productKey, err := c.client.Get(ctx, productDetailRedisAliasKey(normalizedURLHash)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get product detail alias hot cache: %w", err)
	}
	productKey = strings.TrimSpace(productKey)
	if productKey == "" {
		return "", false, nil
	}
	return productKey, true, nil
}

func (c *RedisHotCache) SetAlias(ctx context.Context, normalizedURLHash, productKey string, ttl time.Duration) error {
	normalizedURLHash, err := requireRedisHotCacheNonEmpty("normalized url hash", normalizedURLHash)
	if err != nil {
		return err
	}
	productKey, err = requireRedisHotCacheNonEmpty("product key", productKey)
	if err != nil {
		return err
	}
	if err := requireRedisHotCachePositiveTTL(ttl); err != nil {
		return err
	}
	if c == nil || c.client == nil {
		return errors.New("redis hot cache is nil")
	}

	if err := c.client.Set(ctx, productDetailRedisAliasKey(normalizedURLHash), productKey, ttl).Err(); err != nil {
		return fmt.Errorf("set product detail alias hot cache: %w", err)
	}
	return nil
}

func (c *RedisHotCache) TryLock(ctx context.Context, productKey string, ttl time.Duration) (DetailLock, bool, error) {
	productKey, err := requireRedisHotCacheNonEmpty("product key", productKey)
	if err != nil {
		return nil, false, err
	}
	if err := requireRedisHotCachePositiveTTL(ttl); err != nil {
		return nil, false, err
	}
	if c == nil || c.client == nil {
		return nil, false, errors.New("redis hot cache is nil")
	}

	owner, err := c.lockOwner()
	if err != nil {
		return nil, false, err
	}
	key := productDetailRedisLockKey(productKey)
	ok, err := c.client.SetNX(ctx, key, owner, ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("acquire product detail lock: %w", err)
	}
	if !ok {
		return nil, false, nil
	}
	return &redisProductDetailLock{client: c.client, key: key, owner: owner}, true, nil
}

func (l *redisProductDetailLock) Release(ctx context.Context) error {
	if l == nil || l.client == nil {
		return nil
	}
	if strings.TrimSpace(l.key) == "" || strings.TrimSpace(l.owner) == "" {
		return nil
	}
	script := redis.NewScript(releaseProductDetailLockLua)
	if err := script.Run(ctx, l.client, []string{l.key}, l.owner).Err(); err != nil {
		return fmt.Errorf("release product detail lock: %w", err)
	}
	return nil
}

func ProductDetailRedisKey(productKey string) string {
	return productDetailRedisDetailKey(productKey)
}

func ProductDetailAliasRedisKey(normalizedURLHash string) string {
	return productDetailRedisAliasKey(normalizedURLHash)
}

func ProductDetailLockRedisKey(productKey string) string {
	return productDetailRedisLockKey(productKey)
}

func productDetailRedisDetailKey(productKey string) string {
	return fmt.Sprintf("product_detail:v1:%s", productKey)
}

func productDetailRedisAliasKey(normalizedURLHash string) string {
	return fmt.Sprintf("product_detail_alias:v1:%s", normalizedURLHash)
}

func productDetailRedisLockKey(productKey string) string {
	return fmt.Sprintf("product_detail_lock:v1:%s", productKey)
}

func encodeDetailHotCacheEnvelope(record DetailRecord, ttl time.Duration, now time.Time) ([]byte, error) {
	productKey, err := requireRedisHotCacheNonEmpty("product key", record.ProductKey)
	if err != nil {
		return nil, err
	}
	if err := requireRedisHotCachePositiveTTL(ttl); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	record.ProductKey = productKey
	if record.Platform == "" {
		record.Platform = strings.TrimSpace(record.Detail.Platform)
	}
	if record.CanonicalURL == "" {
		record.CanonicalURL = strings.TrimSpace(record.Detail.ProductURL)
	}
	if record.Detail.Platform == "" {
		record.Detail.Platform = record.Platform
	}
	if record.Detail.ProductURL == "" {
		record.Detail.ProductURL = record.CanonicalURL
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}
	if record.ExpiresAt.IsZero() {
		record.ExpiresAt = now.Add(ttl)
	}
	return json.Marshal(detailHotCacheEnvelope{
		ProductKey:    record.ProductKey,
		Platform:      record.Platform,
		CanonicalURL:  record.CanonicalURL,
		Detail:        record.Detail,
		SourceHash:    record.SourceHash,
		PromptVersion: record.PromptVersion,
		ModelName:     record.ModelName,
		Status:        record.Status,
		ExpiresAt:     record.ExpiresAt.UTC(),
		UpdatedAt:     record.UpdatedAt.UTC(),
	})
}

func decodeDetailHotCacheEnvelope(payload []byte, expectedProductKey string, now time.Time) (DetailRecord, bool, error) {
	expectedProductKey = strings.TrimSpace(expectedProductKey)
	if len(payload) == 0 {
		return DetailRecord{}, false, fmt.Errorf("%w: empty envelope", ErrDetailHotCacheInconsistent)
	}
	var envelope detailHotCacheEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return DetailRecord{}, false, fmt.Errorf("%w: decode envelope: %v", ErrDetailHotCacheInconsistent, err)
	}
	envelope.ProductKey = strings.TrimSpace(envelope.ProductKey)
	if envelope.ProductKey == "" {
		return DetailRecord{}, false, fmt.Errorf("%w: missing product_key", ErrDetailHotCacheInconsistent)
	}
	if expectedProductKey != "" && envelope.ProductKey != expectedProductKey {
		return DetailRecord{}, false, fmt.Errorf("%w: product_key mismatch %s != %s", ErrDetailHotCacheInconsistent, envelope.ProductKey, expectedProductKey)
	}
	if !envelope.ExpiresAt.IsZero() {
		if now.IsZero() {
			now = time.Now()
		}
		if !now.Before(envelope.ExpiresAt) {
			return DetailRecord{}, false, nil
		}
	}
	record := DetailRecord{
		ProductKey:    envelope.ProductKey,
		Platform:      envelope.Platform,
		CanonicalURL:  envelope.CanonicalURL,
		Detail:        envelope.Detail,
		SourceHash:    envelope.SourceHash,
		PromptVersion: envelope.PromptVersion,
		ModelName:     envelope.ModelName,
		Status:        envelope.Status,
		ExpiresAt:     envelope.ExpiresAt,
		UpdatedAt:     envelope.UpdatedAt,
	}
	if record.Platform == "" {
		record.Platform = strings.TrimSpace(record.Detail.Platform)
	}
	if record.CanonicalURL == "" {
		record.CanonicalURL = strings.TrimSpace(record.Detail.ProductURL)
	}
	if record.Detail.Platform == "" {
		record.Detail.Platform = record.Platform
	}
	if record.Detail.ProductURL == "" {
		record.Detail.ProductURL = record.CanonicalURL
	}
	return record, true, nil
}

func (c *RedisHotCache) clockNow() time.Time {
	if c != nil && c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *RedisHotCache) lockOwner() (string, error) {
	if c != nil && c.newOwner != nil {
		return c.newOwner()
	}
	return newRedisLockOwner()
}

func newRedisLockOwner() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate product detail lock owner: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func requireRedisHotCacheNonEmpty(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is empty", name)
	}
	return value, nil
}

func requireRedisHotCachePositiveTTL(ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("ttl must be positive")
	}
	return nil
}
