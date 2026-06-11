package embedding

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/config"

	"github.com/cloudwego/eino-ext/components/embedding/ark"
)

func TestNewArkEmbedderValidatesRequiredConfig(t *testing.T) {
	_, err := NewArkEmbedder(context.Background(), Config{Model: "ep-test"})
	if err == nil {
		t.Fatal("expected missing api key error")
	}
	_, err = NewArkEmbedder(context.Background(), Config{APIKey: "test-key"})
	if err == nil {
		t.Fatal("expected missing model error")
	}
}

func TestArkEmbedderEmptyInputDoesNotCallRemote(t *testing.T) {
	embedder, err := NewArkEmbedder(context.Background(), Config{
		APIKey:  "test-key",
		Model:   "ep-test",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := embedder.EmbedTexts(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if vectors != nil {
		t.Fatalf("vectors = %#v, want nil", vectors)
	}
}

func TestNormalizeArkBaseURLAcceptsEndpointPaths(t *testing.T) {
	got := normalizeArkBaseURL("https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal")
	want := "https://ark.cn-beijing.volces.com/api/v3"
	if got != want {
		t.Fatalf("base url = %q, want %q", got, want)
	}
	got = normalizeArkBaseURL("https://ark.cn-beijing.volces.com/api/v3/embeddings")
	if got != want {
		t.Fatalf("base url = %q, want %q", got, want)
	}
}

func TestNormalizeArkAPIType(t *testing.T) {
	if got := normalizeArkAPIType("multimodal", ""); got == nil || *got != ark.APITypeMultiModal {
		t.Fatalf("multimodal api type = %#v", got)
	}
	if got := normalizeArkAPIType("", "https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal"); got == nil || *got != ark.APITypeMultiModal {
		t.Fatalf("inferred multimodal api type = %#v", got)
	}
	if got := normalizeArkAPIType("text", ""); got == nil || *got != ark.APITypeText {
		t.Fatalf("text api type = %#v", got)
	}
}

func TestArkEmbeddingSmoke(t *testing.T) {
	if os.Getenv("ARK_EMBEDDING_SMOKE") != "1" {
		t.Skip("set ARK_EMBEDDING_SMOKE=1 to run real Ark embedding smoke")
	}
	settings := config.Load()
	dimensions := settings.EmbeddingDimensions
	if raw := os.Getenv("EMBEDDING_DIMENSIONS"); raw != "" {
		dimensions, _ = strconv.Atoi(raw)
	}
	embedder, err := NewArkEmbedder(context.Background(), Config{
		APIKey:     settings.EmbeddingAPIKey,
		APIBase:    settings.EmbeddingAPIBase,
		Model:      settings.EmbeddingModel,
		Region:     settings.EmbeddingRegion,
		APIType:    settings.EmbeddingAPIType,
		Timeout:    time.Duration(settings.EmbeddingTimeout) * time.Second,
		BatchSize:  settings.EmbeddingBatchSize,
		Dimensions: dimensions,
		RetryTimes: settings.EmbeddingRetryTimes,
	})
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := embedder.EmbedTexts(context.Background(), []string{"这是一条 Ark embedding smoke 测试文本。"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		t.Fatalf("unexpected vectors shape: %#v", vectors)
	}
	if dimensions > 0 && len(vectors[0]) != dimensions {
		t.Fatalf("vector dimension = %d, want %d", len(vectors[0]), dimensions)
	}
}
