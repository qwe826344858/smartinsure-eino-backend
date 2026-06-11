package productdetail

import (
	"context"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/logx"
)

type RAGStatusTrackingIngestor struct {
	inner      ProductDetailRAGIngestor
	repository DetailRepository
	now        func() time.Time
}

func NewRAGStatusTrackingIngestor(inner ProductDetailRAGIngestor, repository DetailRepository) ProductDetailRAGIngestor {
	if inner == nil || repository == nil {
		return inner
	}
	return &RAGStatusTrackingIngestor{
		inner:      inner,
		repository: repository,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

func (i *RAGStatusTrackingIngestor) IngestProductDetail(ctx context.Context, record StoredProductDetailWithSource) error {
	if i == nil || i.inner == nil {
		return nil
	}
	productKey := strings.TrimSpace(record.Detail.ProductKey)
	sourceHash := strings.TrimSpace(record.Detail.SourceHash)
	i.update(ctx, productKey, RAGIngestStatusIngesting, sourceHash, "")
	err := i.inner.IngestProductDetail(ctx, record)
	if err != nil {
		i.update(ctx, productKey, RAGIngestStatusFailed, sourceHash, err.Error())
		return err
	}
	i.update(ctx, productKey, RAGIngestStatusIngested, sourceHash, "")
	return nil
}

func (i *RAGStatusTrackingIngestor) update(ctx context.Context, productKey, status, sourceHash, message string) {
	if i == nil || i.repository == nil || strings.TrimSpace(productKey) == "" {
		return
	}
	now := time.Now().UTC()
	if i.now != nil {
		now = i.now()
	}
	if err := i.repository.UpdateRAGIngestState(ctx, UpdateRAGIngestStateInput{
		ProductKey: productKey,
		Status:     status,
		SourceHash: sourceHash,
		Error:      message,
		UpdatedAt:  now,
	}); err != nil {
		logx.Printf("运行日志", "runtime log", "product_detail rag_worker_state_update_failed product_key=%s status=%s source_hash=%s err=%v", logShort(productKey), status, logShort(sourceHash), err)
		return
	}
	logx.Printf("运行日志", "runtime log", "product_detail rag_worker_state_update_success product_key=%s status=%s source_hash=%s has_error=%t", logShort(productKey), status, logShort(sourceHash), strings.TrimSpace(message) != "")
}
