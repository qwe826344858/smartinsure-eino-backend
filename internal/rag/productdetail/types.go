package productdetail

import (
	"time"

	"smartinsure-eino-backend/internal/schema"
)

const (
	DefaultNamespace  = "product_details"
	DefaultSourceType = "product_detail"
)

type ChunkType string

const (
	ChunkTypeSummary       ChunkType = "product_detail_summary"
	ChunkTypeDuty          ChunkType = "product_duty"
	ChunkTypeTag           ChunkType = "product_tag"
	ChunkTypeSourceExcerpt ChunkType = "product_source_excerpt"
)

type SourceSnapshot struct {
	SourceURL        string
	OriginSourceType string
	SourceFormat     string
	CleanedText      string
	ContentHash      string
	CNCharCount      int
	FetchedAt        time.Time
}

type DetailInput struct {
	Namespace         string
	ProductKey        string
	ProductName       string
	Platform          string
	CanonicalURL      string
	NormalizedURLHash string
	SourceHash        string
	PromptVersion     string
	ModelName         string
	Status            string
	ExpiresAt         *time.Time
	Detail            schema.ProductDetail
	Sources           []SourceSnapshot
}

type Options struct {
	Namespace          string
	SourceType         string
	MaxTagChunks       int
	SourceChunkSize    int
	SourceChunkOverlap int
	IndexedAt          time.Time
}

type FormattedDocument struct {
	Namespace   string
	SourceType  string
	SourceURL   string
	Title       string
	CleanedText string
	Metadata    map[string]any
	Chunks      []FormattedChunk
}

type FormattedChunk struct {
	ChunkIndex int
	ChunkType  ChunkType
	Content    string
	Metadata   map[string]any
}

type TagSet struct {
	Tags          []string
	InsuranceTags []string
	MarketTags    []string
	AudienceTags  []string
	DutyTags      []string
	QualityTags   []string
	RelatedDuties map[string][]string
	categories    map[string]string
}

func (t TagSet) Category(tag string) string {
	if t.categories == nil {
		return ""
	}
	return t.categories[tag]
}
