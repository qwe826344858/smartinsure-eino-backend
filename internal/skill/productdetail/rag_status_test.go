package productdetail

import (
	"context"
	"errors"
	"testing"
)

type recordingRAGIngestor struct {
	err   error
	calls int
}

func (i *recordingRAGIngestor) IngestProductDetail(context.Context, StoredProductDetailWithSource) error {
	i.calls++
	return i.err
}

func TestRAGStatusTrackingIngestorMarksIngested(t *testing.T) {
	repo := &fakeDetailRepository{}
	inner := &recordingRAGIngestor{}
	ingestor := NewRAGStatusTrackingIngestor(inner, repo)

	err := ingestor.IngestProductDetail(context.Background(), StoredProductDetailWithSource{
		Detail: StoredProductDetail{
			ProductKey: "unknown:url:abc",
			SourceHash: "source-hash",
		},
	})

	if err != nil {
		t.Fatalf("IngestProductDetail error = %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls)
	}
	if repo.ragStateCalls != 2 || repo.lastRAGState.Status != RAGIngestStatusIngested || repo.lastRAGState.SourceHash != "source-hash" {
		t.Fatalf("unexpected rag state calls=%d last=%#v", repo.ragStateCalls, repo.lastRAGState)
	}
}

func TestRAGStatusTrackingIngestorMarksFailed(t *testing.T) {
	repo := &fakeDetailRepository{}
	inner := &recordingRAGIngestor{err: errors.New("pgvector down")}
	ingestor := NewRAGStatusTrackingIngestor(inner, repo)

	err := ingestor.IngestProductDetail(context.Background(), StoredProductDetailWithSource{
		Detail: StoredProductDetail{
			ProductKey: "unknown:url:abc",
			SourceHash: "source-hash",
		},
	})

	if err == nil {
		t.Fatal("expected ingest error")
	}
	if repo.ragStateCalls != 2 || repo.lastRAGState.Status != RAGIngestStatusFailed || repo.lastRAGState.Error == "" {
		t.Fatalf("unexpected rag state calls=%d last=%#v", repo.ragStateCalls, repo.lastRAGState)
	}
}
