package productdetail

import (
	"context"
	"errors"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/schema"
)

const (
	DetailStatusActive     = "active"
	DetailStatusProcessing = "processing"
	DetailStatusStale      = "stale"
	DetailStatusFailed     = "failed"
	DetailStatusDisabled   = "disabled"
)

const (
	RAGIngestStatusPending   = "pending"
	RAGIngestStatusIngesting = "ingesting"
	RAGIngestStatusEnqueued  = "enqueued"
	RAGIngestStatusIngested  = "ingested"
	RAGIngestStatusFailed    = "failed"
	RAGIngestStatusSkipped   = "skipped"
)

var (
	ErrProductDetailNotFound = errors.New("productdetail: detail not found")
	ErrInvalidDetailInput    = errors.New("productdetail: invalid detail input")
)

type DetailRepository interface {
	EnsureSchema(ctx context.Context) error
	GetByProductKey(ctx context.Context, productKey string) (*StoredProductDetail, error)
	GetByURL(ctx context.Context, productURL string) (*StoredProductDetail, error)
	Upsert(ctx context.Context, input UpsertProductDetailInput) error
	UpdateRAGIngestState(ctx context.Context, input UpdateRAGIngestStateInput) error
	TouchHit(ctx context.Context, productKey string) error
}

type DetailRecord struct {
	ProductKey    string
	Platform      string
	CanonicalURL  string
	Detail        schema.ProductDetail
	SourceHash    string
	PromptVersion string
	ModelName     string
	Status        string
	ExpiresAt     time.Time
	UpdatedAt     time.Time
}

func (r DetailRecord) Usable(now time.Time, promptVersion string) bool {
	status := NormalizeDetailStatus(r.Status)
	if status != DetailStatusActive {
		return false
	}
	if strings.TrimSpace(promptVersion) != "" && strings.TrimSpace(r.PromptVersion) != "" && r.PromptVersion != promptVersion {
		return false
	}
	return r.ExpiresAt.IsZero() || now.Before(r.ExpiresAt)
}

type DetailHotCache interface {
	Get(ctx context.Context, productKey string) (DetailRecord, bool, error)
	Set(ctx context.Context, record DetailRecord, ttl time.Duration) error
	GetAlias(ctx context.Context, normalizedURLHash string) (string, bool, error)
	SetAlias(ctx context.Context, normalizedURLHash, productKey string, ttl time.Duration) error
	TryLock(ctx context.Context, productKey string, ttl time.Duration) (DetailLock, bool, error)
}

type DetailLock interface {
	Release(ctx context.Context) error
}

type StoredProductDetail struct {
	ProductKey          string
	Platform            string
	ProductName         string
	CanonicalURL        string
	URLHash             string
	Detail              schema.ProductDetail
	SourceHash          string
	PromptVersion       string
	ModelName           string
	CNCharCount         int
	MatchRate           float64
	Status              string
	RAGIngestStatus     string
	RAGIngestSourceHash string
	RAGIngestError      string
	RAGIngestUpdatedAt  *time.Time
	ExpiresAt           *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastHitAt           *time.Time
}

type ProductDetailSource struct {
	ID                int64
	ProductKey        string
	NormalizedURLHash string
	SourceURL         string
	SourceType        string
	SourceFormat      string
	RawPayload        *string
	CleanedText       string
	ContentHash       string
	CNCharCount       int
	FetchedAt         time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type StoredProductDetailWithSource struct {
	Detail StoredProductDetail
	Source *ProductDetailSource
}

type ListProductDetailParams struct {
	Limit           int
	Platform        string
	PromptVersion   string
	MinMatchRate    float64
	Now             time.Time
	AfterUpdatedAt  *time.Time
	AfterProductKey string
	RequireSource   bool
}

type ProductDetailRAGReader interface {
	ListActive(ctx context.Context, params ListProductDetailParams) ([]StoredProductDetailWithSource, error)
}

type ProductDetailRAGIngestor interface {
	IngestProductDetail(ctx context.Context, record StoredProductDetailWithSource) error
}

func (d StoredProductDetail) Expired(now time.Time) bool {
	return d.ExpiresAt != nil && !d.ExpiresAt.After(now)
}

func (d StoredProductDetail) Usable(now time.Time, promptVersion string) bool {
	if NormalizeDetailStatus(d.Status) != DetailStatusActive {
		return false
	}
	if strings.TrimSpace(promptVersion) != "" && strings.TrimSpace(d.PromptVersion) != "" && d.PromptVersion != promptVersion {
		return false
	}
	return !d.Expired(now)
}

func NormalizeDetailStatus(status string) string {
	status = strings.TrimSpace(strings.ToLower(status))
	if status == "" {
		return DetailStatusActive
	}
	return status
}

func NormalizeRAGIngestStatus(status string) string {
	status = strings.TrimSpace(strings.ToLower(status))
	if status == "" {
		return RAGIngestStatusPending
	}
	return status
}

func (d StoredProductDetail) RAGIngestRecordedForSource(sourceHash string) bool {
	if strings.TrimSpace(d.RAGIngestSourceHash) == "" || strings.TrimSpace(d.RAGIngestSourceHash) != strings.TrimSpace(sourceHash) {
		return false
	}
	switch NormalizeRAGIngestStatus(d.RAGIngestStatus) {
	case RAGIngestStatusIngesting, RAGIngestStatusEnqueued, RAGIngestStatusIngested:
		return true
	default:
		return false
	}
}

func (d StoredProductDetail) DetailRecord() DetailRecord {
	expiresAt := time.Time{}
	if d.ExpiresAt != nil {
		expiresAt = *d.ExpiresAt
	}
	return DetailRecord{
		ProductKey:    d.ProductKey,
		Platform:      d.Platform,
		CanonicalURL:  d.CanonicalURL,
		Detail:        d.Detail,
		SourceHash:    d.SourceHash,
		PromptVersion: d.PromptVersion,
		ModelName:     d.ModelName,
		Status:        d.Status,
		ExpiresAt:     expiresAt,
		UpdatedAt:     d.UpdatedAt,
	}
}

type UpsertProductDetailInput struct {
	ProductKey          string
	Platform            string
	CanonicalURL        string
	NormalizedURLHash   string
	Detail              schema.ProductDetail
	SourceHash          string
	PromptVersion       string
	ModelName           string
	Status              string
	RAGIngestStatus     string
	RAGIngestSourceHash string
	RAGIngestError      string
	RAGIngestUpdatedAt  *time.Time
	ExpiresAt           *time.Time
	Source              *UpsertProductDetailSourceInput
}

type UpdateRAGIngestStateInput struct {
	ProductKey string
	Status     string
	SourceHash string
	Error      string
	UpdatedAt  time.Time
}

type UpsertProductDetailSourceInput struct {
	ProductKey        string
	NormalizedURLHash string
	SourceURL         string
	SourceType        string
	SourceFormat      string
	RawPayload        *string
	CleanedText       string
	ContentHash       string
	CNCharCount       int
	FetchedAt         time.Time
}
