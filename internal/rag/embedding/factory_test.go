package embedding

import (
	"context"
	"testing"
)

func TestNewEmbedderSelectsOpenAICompatibleByDefault(t *testing.T) {
	embedder, err := NewEmbedder(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := embedder.(*Client); !ok {
		t.Fatalf("embedder type = %T, want *Client", embedder)
	}
}

func TestNewEmbedderSelectsArk(t *testing.T) {
	embedder, err := NewEmbedder(context.Background(), Config{
		Provider: ProviderArk,
		APIKey:   "test-key",
		Model:    "ep-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := embedder.(*ArkEmbedder); !ok {
		t.Fatalf("embedder type = %T, want *ArkEmbedder", embedder)
	}
}

func TestNewEmbedderRejectsUnknownProvider(t *testing.T) {
	_, err := NewEmbedder(context.Background(), Config{Provider: "unknown"})
	if err == nil {
		t.Fatal("expected unknown provider error")
	}
}
