package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/rag/embedding"
)

func TestParseArgsSupportsRepeatedURLAndMetadata(t *testing.T) {
	opts, err := parseArgs([]string{
		"--url", "https://example.com/a",
		"--url", "https://example.com/b",
		"--namespace", "insurance",
		"--source-type", "web_page",
		"--metadata-json", `{"operator":"tester"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.URLs) != 2 {
		t.Fatalf("len(URLs) = %d, want 2", len(opts.URLs))
	}
	if opts.Namespace != "insurance" || opts.SourceType != "web_page" {
		t.Fatalf("unexpected opts: %+v", opts)
	}

	metadata, err := parseMetadata(opts.MetadataJSON)
	if err != nil {
		t.Fatal(err)
	}
	if metadata["operator"] != "tester" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestLoadURLsReadsInputFileAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "urls.txt")
	if err := os.WriteFile(path, []byte("https://example.com/a\n\nhttps://example.com/b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	urls, err := loadURLs(cliOptions{
		URLs:      multiFlag{"https://example.com/a"},
		InputFile: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://example.com/a", "https://example.com/b"}
	if len(urls) != len(want) {
		t.Fatalf("len(urls) = %d, want %d: %#v", len(urls), len(want), urls)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("urls[%d] = %q, want %q", i, urls[i], want[i])
		}
	}
}

func TestParseMetadataRejectsInvalidJSON(t *testing.T) {
	if _, err := parseMetadata("{bad"); err == nil {
		t.Fatal("expected invalid metadata json error")
	}
}

func TestProviderUsesLLMFallbackOnlyForOpenAICompatible(t *testing.T) {
	if !providerUsesLLMFallback("") || !providerUsesLLMFallback(embedding.ProviderOpenAICompatible) || !providerUsesLLMFallback("openai") {
		t.Fatal("expected openai-compatible providers to use fallback")
	}
	if providerUsesLLMFallback(embedding.ProviderArk) {
		t.Fatal("ark provider must not use llm fallback credentials")
	}
}

func TestNewEmbedderDoesNotUseLLMFallbackForArk(t *testing.T) {
	_, err := newEmbedder(context.Background(), config.Settings{
		EmbeddingProvider: "ark",
		EmbeddingModel:    "ep-test",
		LLMAPIKey:         "llm-key",
		LLMAPIBase:        "https://llm.example.com/v1",
	})
	if err == nil || !strings.Contains(err.Error(), "embedding ark api key is empty") {
		t.Fatalf("expected ark api key error, got %v", err)
	}
}
