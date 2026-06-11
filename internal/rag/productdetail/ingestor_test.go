package productdetail

import (
	"context"
	"errors"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/rag/store"
)

type fakeEmbedder struct {
	texts [][]string
	err   error
}

func (e *fakeEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float64, error) {
	e.texts = append(e.texts, append([]string(nil), texts...))
	if e.err != nil {
		return nil, e.err
	}
	vectors := make([][]float64, len(texts))
	for idx := range texts {
		vectors[idx] = []float64{float64(idx + 1), 0.5}
	}
	return vectors, nil
}

type fakeStore struct {
	ensureCalls int
	lastInput   store.DocumentInput
	err         error
}

func (s *fakeStore) EnsureSchema(context.Context) error {
	s.ensureCalls++
	return nil
}

func (s *fakeStore) UpsertDocumentWithChunks(_ context.Context, input store.DocumentInput) (int64, error) {
	s.lastInput = input
	if s.err != nil {
		return 0, s.err
	}
	return 42, nil
}

func TestIngestorIngestsFormattedProductDetail(t *testing.T) {
	embedder := &fakeEmbedder{}
	st := &fakeStore{}
	ingestor, err := NewIngestor(embedder, st, IngestConfig{
		EnsureSchema:       true,
		SourceChunkSize:    80,
		SourceChunkOverlap: 10,
	})
	if err != nil {
		t.Fatalf("NewIngestor error = %v", err)
	}
	ingestor.now = func() time.Time { return time.Date(2026, 6, 10, 11, 0, 0, 0, time.UTC) }

	result, err := ingestor.Ingest(context.Background(), sampleDetailInput())
	if err != nil {
		t.Fatalf("Ingest error = %v", err)
	}
	if result.Status != "success" || result.DocumentID != 42 || result.ChunkCount == 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if st.ensureCalls != 1 {
		t.Fatalf("EnsureSchema calls = %d, want 1", st.ensureCalls)
	}
	if st.lastInput.Namespace != DefaultNamespace || st.lastInput.SourceType != DefaultSourceType {
		t.Fatalf("store namespace/source_type = %q/%q", st.lastInput.Namespace, st.lastInput.SourceType)
	}
	if st.lastInput.SourceURL != "productdetail://huize:104006:108504" {
		t.Fatalf("store SourceURL = %q", st.lastInput.SourceURL)
	}
	if len(embedder.texts) != 1 || len(embedder.texts[0]) != len(st.lastInput.Chunks) {
		t.Fatalf("embedder texts/store chunks mismatch: %#v/%d", embedder.texts, len(st.lastInput.Chunks))
	}
	if len(st.lastInput.Chunks[0].Embedding) == 0 {
		t.Fatalf("first chunk embedding is empty")
	}
}

func TestIngestorSkipsIneligibleProductDetail(t *testing.T) {
	input := sampleDetailInput()
	input.Detail.MatchRate = 0.1
	embedder := &fakeEmbedder{}
	st := &fakeStore{}
	ingestor, _ := NewIngestor(embedder, st, IngestConfig{MinMatchRate: 0.6})

	result, err := ingestor.Ingest(context.Background(), input)
	if err != nil {
		t.Fatalf("Ingest error = %v", err)
	}
	if result.Status != "skipped" || result.Message == "" {
		t.Fatalf("unexpected skipped result: %#v", result)
	}
	if len(embedder.texts) != 0 || len(st.lastInput.Chunks) != 0 {
		t.Fatal("ineligible detail should not embed or store")
	}
}

func TestIngestorPropagatesStoreError(t *testing.T) {
	ingestor, _ := NewIngestor(&fakeEmbedder{}, &fakeStore{err: errors.New("db down")}, IngestConfig{})
	result, err := ingestor.Ingest(context.Background(), sampleDetailInput())
	if err == nil {
		t.Fatal("expected store error")
	}
	if result.Status != "failed" || result.Message != "db down" {
		t.Fatalf("unexpected result: %#v", result)
	}
}
