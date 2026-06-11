package productdetail

import (
	"testing"
	"time"

	"smartinsure-eino-backend/internal/schema"
)

func TestCacheSetGetAndOverwrite(t *testing.T) {
	cache := NewCache(time.Hour)
	url := "https://example.com/product/1"
	first := schema.ProductDetail{ProductName: "A"}
	second := schema.ProductDetail{ProductName: "B"}

	cache.Set(url, first)
	cache.Set(url, second)

	got, ok := cache.Get(url)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.ProductName != "B" {
		t.Fatalf("ProductName = %q, want B", got.ProductName)
	}
	if cache.Size() != 1 {
		t.Fatalf("Size = %d, want 1", cache.Size())
	}
}

func TestCacheExpiresEntries(t *testing.T) {
	cache := NewCache(20 * time.Millisecond)
	url := "https://example.com/product/2"
	cache.Set(url, schema.ProductDetail{ProductName: "医疗险"})

	if _, ok := cache.Get(url); !ok {
		t.Fatal("expected cache hit before TTL")
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := cache.Get(url); ok {
		t.Fatal("expected cache miss after TTL")
	}
}

func TestCacheClearExpiredAndClear(t *testing.T) {
	cache := NewCache(20 * time.Millisecond)
	cache.Set("https://example.com/a", schema.ProductDetail{ProductName: "A"})
	cache.Set("https://example.com/b", schema.ProductDetail{ProductName: "B"})
	time.Sleep(40 * time.Millisecond)

	cache.defaultTTL = time.Hour
	cache.Set("https://example.com/c", schema.ProductDetail{ProductName: "C"})

	if cleaned := cache.ClearExpired(); cleaned != 2 {
		t.Fatalf("ClearExpired = %d, want 2", cleaned)
	}
	if cache.Size() != 1 {
		t.Fatalf("Size = %d, want 1", cache.Size())
	}
	cache.Clear()
	if cache.Size() != 0 {
		t.Fatalf("Size after Clear = %d, want 0", cache.Size())
	}
}
