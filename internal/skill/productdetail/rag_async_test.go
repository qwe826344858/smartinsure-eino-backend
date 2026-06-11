package productdetail

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type blockingRAGIngestor struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (i *blockingRAGIngestor) IngestProductDetail(context.Context, StoredProductDetailWithSource) error {
	i.mu.Lock()
	i.calls++
	i.mu.Unlock()
	signalTestChan(i.started)
	if i.release != nil {
		<-i.release
	}
	return nil
}

func (i *blockingRAGIngestor) callCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.calls
}

func TestAsyncProductDetailRAGIngestorRunsWorker(t *testing.T) {
	inner := &blockingRAGIngestor{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	ingestor, err := NewAsyncProductDetailRAGIngestor(inner, AsyncRAGIngestorConfig{
		Workers:   1,
		QueueSize: 2,
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("NewAsyncProductDetailRAGIngestor error = %v", err)
	}
	defer ingestor.Close()

	if err := ingestor.IngestProductDetail(context.Background(), StoredProductDetailWithSource{}); err != nil {
		t.Fatalf("IngestProductDetail error = %v", err)
	}
	waitSignal(t, inner.started, "rag worker started")
	close(inner.release)
	waitForCondition(t, "rag call count", func() bool { return inner.callCount() == 1 })
}

func TestAsyncProductDetailRAGIngestorRejectsWhenQueueFull(t *testing.T) {
	inner := &blockingRAGIngestor{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	ingestor, err := NewAsyncProductDetailRAGIngestor(inner, AsyncRAGIngestorConfig{
		Workers:   1,
		QueueSize: 1,
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("NewAsyncProductDetailRAGIngestor error = %v", err)
	}
	defer ingestor.Close()

	if err := ingestor.IngestProductDetail(context.Background(), StoredProductDetailWithSource{}); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}
	waitSignal(t, inner.started, "rag worker started")
	if err := ingestor.IngestProductDetail(context.Background(), StoredProductDetailWithSource{}); err != nil {
		t.Fatalf("second enqueue should fill queue, got %v", err)
	}
	if err := ingestor.IngestProductDetail(context.Background(), StoredProductDetailWithSource{}); !errors.Is(err, ErrProductDetailRAGQueueFull) {
		t.Fatalf("third enqueue err = %v, want queue full", err)
	}
	close(inner.release)
}

func waitForCondition(t *testing.T, name string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", name)
}
