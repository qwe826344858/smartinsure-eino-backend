package productdetail

import (
	"context"
	"errors"
	"smartinsure-eino-backend/internal/logx"
	"sync"
	"time"
)

var ErrProductDetailRAGQueueFull = errors.New("productdetail: rag ingest queue is full")

type AsyncRAGIngestorConfig struct {
	Workers   int
	QueueSize int
	Timeout   time.Duration
}

type AsyncProductDetailRAGIngestor struct {
	inner   ProductDetailRAGIngestor
	queue   chan StoredProductDetailWithSource
	timeout time.Duration
	once    sync.Once
}

func NewAsyncProductDetailRAGIngestor(inner ProductDetailRAGIngestor, cfg AsyncRAGIngestorConfig) (*AsyncProductDetailRAGIngestor, error) {
	if inner == nil {
		return nil, errors.New("productdetail: async rag ingestor inner is nil")
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = 2
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = 100
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ingestor := &AsyncProductDetailRAGIngestor{
		inner:   inner,
		queue:   make(chan StoredProductDetailWithSource, queueSize),
		timeout: timeout,
	}
	logx.Printf("运行日志", "runtime log", "product_detail rag_async_start workers=%d queue_size=%d timeout_seconds=%d", workers, queueSize, int(timeout.Seconds()))
	for i := 0; i < workers; i++ {
		go ingestor.worker(i + 1)
	}
	return ingestor, nil
}

func (i *AsyncProductDetailRAGIngestor) IngestProductDetail(ctx context.Context, record StoredProductDetailWithSource) error {
	if i == nil || i.inner == nil || i.queue == nil {
		return errors.New("productdetail: async rag ingestor is nil")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case i.queue <- record:
		logx.Printf("运行日志", "runtime log", "product_detail rag_async_enqueue product_key=%s queue_depth=%d", logShort(record.Detail.ProductKey), len(i.queue))
		return nil
	default:
		logx.Printf("运行日志", "runtime log", "product_detail rag_async_queue_full product_key=%s queue_depth=%d", logShort(record.Detail.ProductKey), len(i.queue))
		return ErrProductDetailRAGQueueFull
	}
}

func (i *AsyncProductDetailRAGIngestor) Close() {
	if i == nil {
		return
	}
	i.once.Do(func() {
		close(i.queue)
	})
}

func (i *AsyncProductDetailRAGIngestor) worker(workerID int) {
	for record := range i.queue {
		startedAt := time.Now()
		logx.Printf("运行日志", "runtime log", "product_detail rag_async_worker_start worker=%d product_key=%s queue_depth=%d", workerID, logShort(record.Detail.ProductKey), len(i.queue))
		ctx, cancel := context.WithTimeout(context.Background(), i.timeout)
		if err := i.inner.IngestProductDetail(ctx, record); err != nil {
			logx.Printf("运行日志", "runtime log", "product_detail rag_async_worker_failed worker=%d product_key=%s duration_ms=%d err=%v", workerID, logShort(record.Detail.ProductKey), time.Since(startedAt).Milliseconds(), err)
		} else {
			logx.Printf("运行日志", "runtime log", "product_detail rag_async_worker_success worker=%d product_key=%s duration_ms=%d", workerID, logShort(record.Detail.ProductKey), time.Since(startedAt).Milliseconds())
		}
		cancel()
	}
}
