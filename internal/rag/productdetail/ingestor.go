package productdetail

import (
	"context"
	"errors"
	"fmt"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/rag/store"
)

type Embedder interface {
	EmbedTexts(ctx context.Context, texts []string) ([][]float64, error)
}

type Store interface {
	EnsureSchema(ctx context.Context) error
	UpsertDocumentWithChunks(ctx context.Context, input store.DocumentInput) (int64, error)
}

type IngestConfig struct {
	Namespace          string
	SourceType         string
	MinMatchRate       float64
	MaxTagChunks       int
	SourceChunkSize    int
	SourceChunkOverlap int
	EnsureSchema       bool
}

type IngestResult struct {
	ProductKey string
	Status     string
	DocumentID int64
	ChunkCount int
	Message    string
}

type Ingestor struct {
	embedder Embedder
	store    Store
	cfg      IngestConfig
	now      func() time.Time
}

func NewIngestor(embedder Embedder, st Store, cfg IngestConfig) (*Ingestor, error) {
	if strings.TrimSpace(cfg.Namespace) == "" {
		cfg.Namespace = DefaultNamespace
	}
	if strings.TrimSpace(cfg.SourceType) == "" {
		cfg.SourceType = DefaultSourceType
	}
	if cfg.MinMatchRate <= 0 {
		cfg.MinMatchRate = 0.6
	}
	return &Ingestor{
		embedder: embedder,
		store:    st,
		cfg:      cfg,
		now:      func() time.Time { return time.Now().UTC() },
	}, nil
}

func (i *Ingestor) Ingest(ctx context.Context, input DetailInput) (IngestResult, error) {
	startedAt := time.Now()
	result := IngestResult{ProductKey: strings.TrimSpace(input.ProductKey)}
	if err := i.ready(); err != nil {
		result.Status = "failed"
		result.Message = err.Error()
		logx.Printf("运行日志", "runtime log", "rag product_detail ingest_failed product_key=%s stage=ready duration_ms=%d err=%v", logShort(result.ProductKey), time.Since(startedAt).Milliseconds(), err)
		return result, err
	}
	logx.Printf("运行日志", "runtime log", "rag product_detail ingest_start namespace=%s product_key=%s product_name=%q duties=%d match_rate=%.3f cn_chars=%d", i.cfg.Namespace, logShort(result.ProductKey), input.ProductName, len(input.Detail.Duties), input.Detail.MatchRate, input.Detail.CNCharCount)
	if i.cfg.EnsureSchema {
		if err := i.store.EnsureSchema(ctx); err != nil {
			result.Status = "failed"
			result.Message = err.Error()
			logx.Printf("运行日志", "runtime log", "rag product_detail ingest_failed product_key=%s stage=ensure_schema duration_ms=%d err=%v", logShort(result.ProductKey), time.Since(startedAt).Milliseconds(), err)
			return result, err
		}
	}

	ok, reason := Eligible(input, i.cfg.MinMatchRate, i.now())
	if !ok {
		result.Status = "skipped"
		result.Message = reason
		logx.Printf("运行日志", "runtime log", "rag product_detail ingest_skipped product_key=%s reason=%q match_rate=%.3f min_match_rate=%.3f duration_ms=%d", logShort(result.ProductKey), reason, input.Detail.MatchRate, i.cfg.MinMatchRate, time.Since(startedAt).Milliseconds())
		return result, nil
	}

	doc, err := Format(input, Options{
		Namespace:          i.cfg.Namespace,
		SourceType:         i.cfg.SourceType,
		MaxTagChunks:       i.cfg.MaxTagChunks,
		SourceChunkSize:    i.cfg.SourceChunkSize,
		SourceChunkOverlap: i.cfg.SourceChunkOverlap,
		IndexedAt:          i.now(),
	})
	if err != nil {
		result.Status = "failed"
		result.Message = err.Error()
		logx.Printf("运行日志", "runtime log", "rag product_detail ingest_failed product_key=%s stage=format duration_ms=%d err=%v", logShort(result.ProductKey), time.Since(startedAt).Milliseconds(), err)
		return result, err
	}
	logx.Printf("运行日志", "runtime log", "rag product_detail chunks_built product_key=%s namespace=%s chunks=%d", logShort(result.ProductKey), doc.Namespace, len(doc.Chunks))
	texts := make([]string, len(doc.Chunks))
	for idx, chunk := range doc.Chunks {
		texts[idx] = chunk.Content
	}
	embedStartedAt := time.Now()
	embeddings, err := i.embedder.EmbedTexts(ctx, texts)
	if err != nil {
		result.Status = "failed"
		result.Message = err.Error()
		logx.Printf("运行日志", "runtime log", "rag product_detail ingest_failed product_key=%s stage=embedding chunks=%d duration_ms=%d err=%v", logShort(result.ProductKey), len(texts), time.Since(embedStartedAt).Milliseconds(), err)
		return result, err
	}
	logx.Printf("运行日志", "runtime log", "rag product_detail embedding_success product_key=%s chunks=%d duration_ms=%d", logShort(result.ProductKey), len(embeddings), time.Since(embedStartedAt).Milliseconds())
	if len(embeddings) != len(doc.Chunks) {
		err := fmt.Errorf("embedding response count mismatch: got=%d want=%d", len(embeddings), len(doc.Chunks))
		result.Status = "failed"
		result.Message = err.Error()
		logx.Printf("运行日志", "runtime log", "rag product_detail ingest_failed product_key=%s stage=embedding_count duration_ms=%d err=%v", logShort(result.ProductKey), time.Since(startedAt).Milliseconds(), err)
		return result, err
	}
	chunkRecords := make([]store.ChunkRecord, len(doc.Chunks))
	for idx, chunk := range doc.Chunks {
		chunkRecords[idx] = store.ChunkRecord{
			ChunkIndex: chunk.ChunkIndex,
			Content:    chunk.Content,
			Embedding:  embeddings[idx],
			Metadata:   chunk.Metadata,
		}
	}
	documentID, err := i.store.UpsertDocumentWithChunks(ctx, store.DocumentInput{
		Namespace:   doc.Namespace,
		SourceType:  doc.SourceType,
		SourceURL:   doc.SourceURL,
		Title:       doc.Title,
		CleanedText: doc.CleanedText,
		Metadata:    doc.Metadata,
		Chunks:      chunkRecords,
	})
	if err != nil {
		result.Status = "failed"
		result.Message = err.Error()
		logx.Printf("运行日志", "runtime log", "rag product_detail ingest_failed product_key=%s stage=upsert duration_ms=%d err=%v", logShort(result.ProductKey), time.Since(startedAt).Milliseconds(), err)
		return result, err
	}
	result.Status = "success"
	result.DocumentID = documentID
	result.ChunkCount = len(chunkRecords)
	result.Message = fmt.Sprintf("ingested %d chunks", len(chunkRecords))
	logx.Printf("运行日志", "runtime log", "rag product_detail ingest_success product_key=%s document_id=%d chunks=%d duration_ms=%d", logShort(result.ProductKey), documentID, len(chunkRecords), time.Since(startedAt).Milliseconds())
	return result, nil
}

func (i *Ingestor) ready() error {
	if i == nil {
		return errors.New("product detail ingestor is nil")
	}
	if i.embedder == nil {
		return errors.New("product detail ingestor embedder is nil")
	}
	if i.store == nil {
		return errors.New("product detail ingestor store is nil")
	}
	if strings.TrimSpace(i.cfg.Namespace) == "" {
		return errors.New("product detail ingestor namespace is empty")
	}
	if strings.TrimSpace(i.cfg.SourceType) == "" {
		return errors.New("product detail ingestor source type is empty")
	}
	if i.now == nil {
		i.now = func() time.Time { return time.Now().UTC() }
	}
	return nil
}

func logShort(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
