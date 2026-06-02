package store

import "context"

type ChunkRecord struct {
	ChunkIndex int
	Content    string
	Embedding  []float64
	Metadata   map[string]any
}

type DocumentInput struct {
	Namespace   string
	SourceType  string
	SourceURL   string
	Title       string
	RawHTML     string
	CleanedText string
	Metadata    map[string]any
	Chunks      []ChunkRecord
}

type Store interface {
	EnsureSchema(ctx context.Context) error
	UpsertDocumentWithChunks(ctx context.Context, input DocumentInput) (int64, error)
}
