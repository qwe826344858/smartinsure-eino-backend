package productdetail

import (
	"strings"
	"testing"
)

func TestProductKeyerNormalizesURL(t *testing.T) {
	raw := "HTTPS://Example.COM:443/Product/123/?utm_source=ad&id=10&spm=abc&productCode=ABC&share_token=x&_rag_check=1&session_id=s#frag"

	normalized, err := NormalizeProductURL(raw)
	if err != nil {
		t.Fatalf("NormalizeProductURL error = %v", err)
	}

	want := "https://example.com/Product/123?id=10&productCode=ABC"
	if normalized != want {
		t.Fatalf("normalized = %q, want %q", normalized, want)
	}
}

func TestProductKeyerBuildsPinganKeyFromFragmentCode(t *testing.T) {
	raw := "https://baoxian.pingan.com/pa18shopnst/quote/pc/index.html?utm_source=ad#/ZP021636"

	identity, err := NewProductKeyer().Key(raw)
	if err != nil {
		t.Fatalf("Key error = %v", err)
	}

	wantURL := "https://baoxian.pingan.com/pa18shopnst/quote/pc/index.html"
	if identity.NormalizedURL != wantURL {
		t.Fatalf("NormalizedURL = %q, want %q", identity.NormalizedURL, wantURL)
	}
	if identity.Platform != ProductPlatformPingan {
		t.Fatalf("Platform = %q, want %q", identity.Platform, ProductPlatformPingan)
	}
	if identity.ProductCode != "ZP021636" {
		t.Fatalf("ProductCode = %q", identity.ProductCode)
	}
	if identity.ProductKey != "pingan:ZP021636" {
		t.Fatalf("ProductKey = %q", identity.ProductKey)
	}
	if identity.URLHash != ProductURLHash(wantURL) {
		t.Fatalf("URLHash = %q, want hash of normalized URL", identity.URLHash)
	}
}

func TestProductKeyerFallsBackToNormalizedURLHash(t *testing.T) {
	raw := "example.com/product/1?from=share&id=9"

	identity, err := NewProductKeyer().Key(raw)
	if err != nil {
		t.Fatalf("Key error = %v", err)
	}

	wantURL := "https://example.com/product/1?id=9"
	wantHash := ProductURLHash(wantURL)
	if identity.NormalizedURL != wantURL {
		t.Fatalf("NormalizedURL = %q, want %q", identity.NormalizedURL, wantURL)
	}
	if identity.Platform != ProductPlatformUnknown {
		t.Fatalf("Platform = %q, want unknown", identity.Platform)
	}
	if identity.ProductKey != "unknown:url:"+wantHash {
		t.Fatalf("ProductKey = %q, want unknown:url:%s", identity.ProductKey, wantHash)
	}
}

func TestInferProductPlatformRecognizesKnownHosts(t *testing.T) {
	cases := map[string]string{
		"https://baoxian.pingan.com/x": ProductPlatformPingan,
		"https://www.xiaoyusan.com/x":  ProductPlatformXiaoyusan,
		"https://www.huize.com/x":      ProductPlatformHuize,
		"https://example.com/x":        ProductPlatformUnknown,
	}
	for rawURL, want := range cases {
		if got := InferProductPlatform(rawURL); got != want {
			t.Fatalf("InferProductPlatform(%q) = %q, want %q", rawURL, got, want)
		}
	}
}

func TestProductKeyerRejectsInvalidURLs(t *testing.T) {
	for _, rawURL := range []string{"", "/relative/product", "ftp://example.com/product"} {
		_, err := NewProductKeyer().Key(rawURL)
		if err == nil {
			t.Fatalf("Key(%q) expected error", rawURL)
		}
		if !strings.Contains(err.Error(), "productdetail:") {
			t.Fatalf("Key(%q) error = %v, want productdetail prefix", rawURL, err)
		}
	}
}
