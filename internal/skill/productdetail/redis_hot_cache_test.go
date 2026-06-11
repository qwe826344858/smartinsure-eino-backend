package productdetail

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/schema"
)

func TestProductDetailRedisKeys(t *testing.T) {
	productKey := "pingan:ABC123"
	urlHash := "b65f4c61d1f75e"

	if got, want := productDetailRedisDetailKey(productKey), "product_detail:v1:pingan:ABC123"; got != want {
		t.Fatalf("detail key = %q, want %q", got, want)
	}
	if got, want := productDetailRedisAliasKey(urlHash), "product_detail_alias:v1:b65f4c61d1f75e"; got != want {
		t.Fatalf("alias key = %q, want %q", got, want)
	}
	if got, want := productDetailRedisLockKey(productKey), "product_detail_lock:v1:pingan:ABC123"; got != want {
		t.Fatalf("lock key = %q, want %q", got, want)
	}
}

func TestDetailHotCacheEnvelopeRoundTrip(t *testing.T) {
	now := time.Date(2026, 6, 9, 8, 0, 0, 0, time.UTC)
	ttl := 2 * time.Hour
	detail := schema.ProductDetail{
		ProductName: "Sample Medical",
		ProductURL:  "https://example.com/product",
		Platform:    "pingan",
		Duties: []schema.DutyItem{
			{Name: "Inpatient", Coverage: "1M", Description: "Covers inpatient care"},
		},
		CNCharCount: 1200,
		MatchRate:   0.91,
	}
	record := DetailRecord{
		ProductKey:    " pingan:ABC123 ",
		Detail:        detail,
		SourceHash:    "source-hash",
		PromptVersion: "detail_extract_v1",
		ModelName:     "detail-model",
		Status:        DetailStatusActive,
	}

	payload, err := encodeDetailHotCacheEnvelope(record, ttl, now)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}

	var envelope detailHotCacheEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if got, want := envelope.ProductKey, "pingan:ABC123"; got != want {
		t.Fatalf("product_key = %q, want %q", got, want)
	}
	if got, want := envelope.Platform, detail.Platform; got != want {
		t.Fatalf("platform = %q, want %q", got, want)
	}
	if got, want := envelope.CanonicalURL, detail.ProductURL; got != want {
		t.Fatalf("canonical_url = %q, want %q", got, want)
	}
	if got, want := envelope.ExpiresAt, now.Add(ttl); !got.Equal(want) {
		t.Fatalf("expires_at = %s, want %s", got, want)
	}
	if got, want := envelope.UpdatedAt, now; !got.Equal(want) {
		t.Fatalf("updated_at = %s, want %s", got, want)
	}

	got, ok, err := decodeDetailHotCacheEnvelope(payload, "pingan:ABC123", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !ok {
		t.Fatal("decode envelope returned miss")
	}
	wantRecord := DetailRecord{
		ProductKey:    "pingan:ABC123",
		Platform:      detail.Platform,
		CanonicalURL:  detail.ProductURL,
		Detail:        detail,
		SourceHash:    record.SourceHash,
		PromptVersion: record.PromptVersion,
		ModelName:     record.ModelName,
		Status:        record.Status,
		ExpiresAt:     now.Add(ttl),
		UpdatedAt:     now,
	}
	if !reflect.DeepEqual(got, wantRecord) {
		t.Fatalf("record mismatch\ngot:  %#v\nwant: %#v", got, wantRecord)
	}
}

func TestDetailHotCacheEnvelopeExpiredReturnsMiss(t *testing.T) {
	now := time.Date(2026, 6, 9, 8, 0, 0, 0, time.UTC)
	payload, err := encodeDetailHotCacheEnvelope(DetailRecord{
		ProductKey: "pingan:ABC123",
		Detail: schema.ProductDetail{
			ProductName: "Sample Medical",
		},
	}, time.Minute, now)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}

	got, ok, err := decodeDetailHotCacheEnvelope(payload, "pingan:ABC123", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if ok {
		t.Fatalf("decode envelope returned hit for expired payload: %#v", got)
	}
}

func TestDetailHotCacheEnvelopeRejectsMismatchedProductKey(t *testing.T) {
	now := time.Date(2026, 6, 9, 8, 0, 0, 0, time.UTC)
	payload, err := encodeDetailHotCacheEnvelope(DetailRecord{
		ProductKey: "pingan:ABC123",
		Detail: schema.ProductDetail{
			ProductName: "Sample Medical",
		},
	}, time.Minute, now)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}

	_, _, err = decodeDetailHotCacheEnvelope(payload, "pingan:OTHER", now)
	if !errors.Is(err, ErrDetailHotCacheInconsistent) {
		t.Fatalf("decode error = %v, want ErrDetailHotCacheInconsistent", err)
	}
}

func TestReleaseProductDetailLockLuaComparesOwnerBeforeDelete(t *testing.T) {
	for _, token := range []string{"GET", "DEL", "KEYS[1]", "ARGV[1]", "return 0"} {
		if !strings.Contains(releaseProductDetailLockLua, token) {
			t.Fatalf("releaseProductDetailLockLua missing %s", token)
		}
	}
	if strings.Index(releaseProductDetailLockLua, "GET") > strings.Index(releaseProductDetailLockLua, "DEL") {
		t.Fatalf("releaseProductDetailLockLua deletes before checking owner: %s", releaseProductDetailLockLua)
	}
}

func TestNewRedisLockOwnerReturnsHexToken(t *testing.T) {
	owner, err := newRedisLockOwner()
	if err != nil {
		t.Fatalf("newRedisLockOwner: %v", err)
	}
	if len(owner) != 32 {
		t.Fatalf("owner length = %d, want 32", len(owner))
	}
	if _, err := hex.DecodeString(owner); err != nil {
		t.Fatalf("owner is not hex: %v", err)
	}
}

func TestNewRedisHotCacheRejectsNilClient(t *testing.T) {
	cache, err := NewRedisHotCache(nil)
	if err == nil {
		t.Fatal("NewRedisHotCache nil client error = nil")
	}
	if cache != nil {
		t.Fatalf("cache = %#v, want nil", cache)
	}
}
