package ingest

import (
	"context"
	"errors"
	"testing"

	"smartinsure-eino-backend/internal/rag/chunker"
	"smartinsure-eino-backend/internal/rag/store"
)

type fakeFetcher map[string]string

func (f fakeFetcher) Fetch(ctx context.Context, url string) (string, error) {
	if html, ok := f[url]; ok {
		return html, nil
	}
	return "", errors.New("not found")
}

type fakeEmbedder struct {
	texts []string
}

func (e *fakeEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float64, error) {
	e.texts = append(e.texts, texts...)
	vectors := make([][]float64, len(texts))
	for i := range texts {
		vectors[i] = []float64{float64(i + 1), float64(len([]rune(texts[i])))}
	}
	return vectors, nil
}

type fakeStore struct {
	ensured bool
	inputs  []store.DocumentInput
}

func (s *fakeStore) EnsureSchema(ctx context.Context) error {
	s.ensured = true
	return nil
}

func (s *fakeStore) UpsertDocumentWithChunks(ctx context.Context, input store.DocumentInput) (int64, error) {
	s.inputs = append(s.inputs, input)
	return int64(len(s.inputs)), nil
}

func TestIngestURLsOrchestratesPipeline(t *testing.T) {
	fetcher := fakeFetcher{
		"https://example.com/a": `<html><head><title>测试标题</title></head><body><p>这是第一段保险内容，包含足够中文。</p><p>这是第二段保险内容，继续补充中文。</p></body></html>`,
	}
	embedder := &fakeEmbedder{}
	st := &fakeStore{}
	service, err := NewService(fetcher, embedder, st, Config{
		Namespace:    "default",
		SourceType:   "web_page",
		MinCNChars:   5,
		ChunkSize:    12,
		ChunkOverlap: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := service.IngestURLs(context.Background(), []string{
		" https://example.com/a ",
		"https://example.com/a",
	}, Options{
		Namespace: "ns",
		Metadata:  map[string]any{"operator": "tester"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.ensured {
		t.Fatal("schema was not ensured")
	}
	if len(results) != 1 || results[0].Status != "success" || results[0].DocumentID != 1 {
		t.Fatalf("unexpected results: %+v", results)
	}
	if len(st.inputs) != 1 {
		t.Fatalf("store inputs=%d", len(st.inputs))
	}
	input := st.inputs[0]
	if input.Namespace != "ns" || input.SourceType != "web_page" || input.Title != "测试标题" {
		t.Fatalf("unexpected document input: %+v", input)
	}
	if input.Metadata["operator"] != "tester" || input.Metadata["cn_count"].(int) == 0 {
		t.Fatalf("metadata not merged: %+v", input.Metadata)
	}
	if len(input.Chunks) == 0 || len(embedder.texts) != len(input.Chunks) {
		t.Fatalf("chunks/embed mismatch: chunks=%d texts=%d", len(input.Chunks), len(embedder.texts))
	}
	firstChunk := input.Chunks[0]
	if firstChunk.Metadata["source_url"] != "https://example.com/a" || firstChunk.Metadata["operator"] != "tester" {
		t.Fatalf("chunk metadata not merged: %+v", firstChunk.Metadata)
	}
	if firstChunk.ChunkIndex != 0 || len(firstChunk.Embedding) != 2 {
		t.Fatalf("unexpected chunk record: %+v", firstChunk)
	}
}

func TestIngestURLSkipsShortChineseContent(t *testing.T) {
	fetcher := fakeFetcher{
		"https://example.com/short": `<html><head><title>short</title></head><body><p>太短</p></body></html>`,
	}
	service, err := NewService(fetcher, &fakeEmbedder{}, &fakeStore{}, Config{
		Namespace:    "default",
		MinCNChars:   10,
		ChunkSize:    20,
		ChunkOverlap: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.IngestURL(context.Background(), "https://example.com/short", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "skipped" || result.CNCharCount != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestExtractTitle(t *testing.T) {
	title := ExtractTitle(`<html><title>  A &amp; B <span>保险</span>  </title></html>`)
	if title != "A & B 保险" {
		t.Fatalf("ExtractTitle()=%q", title)
	}
}

func TestDeduplicateURLs(t *testing.T) {
	got := DeduplicateURLs([]string{" a ", "", "b", "a", "b/"})
	want := []string{"a", "b", "b/"}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestNewServiceWithCustomChunker(t *testing.T) {
	splitter, err := chunker.New(5, 1)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewServiceWithChunker(fakeFetcher{}, splitter, &fakeEmbedder{}, &fakeStore{}, Config{Namespace: "ns"})
	if err != nil {
		t.Fatal(err)
	}
	if service.chunker != splitter {
		t.Fatal("custom chunker not used")
	}
}

func TestWithDefaultsAppliesChunkOverlap(t *testing.T) {
	cfg := withDefaults(Config{Namespace: "ns", ChunkSize: 1200})
	if cfg.ChunkOverlap == 0 {
		t.Fatal("ChunkOverlap default was not applied")
	}
	if cfg.ChunkOverlap >= cfg.ChunkSize {
		t.Fatalf("ChunkOverlap = %d should be less than ChunkSize = %d", cfg.ChunkOverlap, cfg.ChunkSize)
	}
}
