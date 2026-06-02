package productdetail

import (
	"sync"
	"time"

	"smartinsure-eino-backend/internal/schema"
)

const DefaultCacheTTL = 24 * time.Hour

type CacheEntry struct {
	Detail    schema.ProductDetail
	CreatedAt time.Time
	TTL       time.Duration
}

func (e CacheEntry) Expired(now time.Time) bool {
	return now.Sub(e.CreatedAt) > e.TTL
}

type ProductDetailCache struct {
	mu         sync.Mutex
	store      map[string]CacheEntry
	defaultTTL time.Duration
	now        func() time.Time
}

func NewCache(defaultTTL time.Duration) *ProductDetailCache {
	if defaultTTL <= 0 {
		defaultTTL = DefaultCacheTTL
	}
	return &ProductDetailCache{
		store:      map[string]CacheEntry{},
		defaultTTL: defaultTTL,
		now:        time.Now,
	}
}

func NewProductDetailCache(defaultTTL time.Duration) *ProductDetailCache {
	return NewCache(defaultTTL)
}

func (c *ProductDetailCache) Get(url string) (schema.ProductDetail, bool) {
	if c == nil {
		return schema.ProductDetail{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.store[url]
	if !ok {
		return schema.ProductDetail{}, false
	}
	if entry.Expired(c.now()) {
		delete(c.store, url)
		return schema.ProductDetail{}, false
	}
	return entry.Detail, true
}

func (c *ProductDetailCache) Set(url string, detail schema.ProductDetail) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[url] = CacheEntry{
		Detail:    detail,
		CreatedAt: c.now(),
		TTL:       c.defaultTTL,
	}
}

func (c *ProductDetailCache) ClearExpired() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	cleaned := 0
	for key, entry := range c.store {
		if entry.Expired(now) {
			delete(c.store, key)
			cleaned++
		}
	}
	return cleaned
}

func (c *ProductDetailCache) Size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.store)
}

func (c *ProductDetailCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store = map[string]CacheEntry{}
}

var DefaultCache = NewCache(DefaultCacheTTL)
