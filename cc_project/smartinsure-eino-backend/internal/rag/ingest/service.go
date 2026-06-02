package ingest

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/htmlcleaner"
	"smartinsure-eino-backend/internal/rag/chunker"
	"smartinsure-eino-backend/internal/rag/store"
)

const defaultSourceType = "web_page"

type Embedder interface {
	EmbedTexts(ctx context.Context, texts []string) ([][]float64, error)
}

type ChunkSplitter interface {
	Split(text string) []chunker.Chunk
}

type Config struct {
	Namespace    string
	SourceType   string
	MinCNChars   int
	ChunkSize    int
	ChunkOverlap int
	FetchTimeout time.Duration
}

type Options struct {
	Namespace  string
	SourceType string
	Metadata   map[string]any
}

type Result struct {
	URL         string `json:"url"`
	Status      string `json:"status"`
	DocumentID  int64  `json:"document_id,omitempty"`
	ChunkCount  int    `json:"chunk_count"`
	Message     string `json:"message,omitempty"`
	CNCharCount int    `json:"cn_char_count,omitempty"`
}

type Service struct {
	fetcher    Fetcher
	chunker    ChunkSplitter
	embedder   Embedder
	store      store.Store
	namespace  string
	sourceType string
	minCNChars int
}

func DefaultConfig() Config {
	settings := config.Load()
	return Config{
		Namespace:    settings.IngestNamespace,
		SourceType:   defaultSourceType,
		MinCNChars:   settings.IngestMinCNChars,
		ChunkSize:    settings.IngestChunkSize,
		ChunkOverlap: settings.IngestChunkOverlap,
		FetchTimeout: DefaultFetchTimeout,
	}
}

func NewService(fetcher Fetcher, embedder Embedder, st store.Store, cfg Config) (*Service, error) {
	cfg = withDefaults(cfg)
	chunkSplitter, err := chunker.New(cfg.ChunkSize, cfg.ChunkOverlap)
	if err != nil {
		return nil, err
	}
	if fetcher == nil {
		fetcher = NewHTTPFetcher(cfg.FetchTimeout)
	}
	return &Service{
		fetcher:    fetcher,
		chunker:    chunkSplitter,
		embedder:   embedder,
		store:      st,
		namespace:  cfg.Namespace,
		sourceType: cfg.SourceType,
		minCNChars: cfg.MinCNChars,
	}, nil
}

func NewServiceWithChunker(fetcher Fetcher, chunkSplitter ChunkSplitter, embedder Embedder, st store.Store, cfg Config) (*Service, error) {
	cfg = withDefaults(cfg)
	if fetcher == nil {
		fetcher = NewHTTPFetcher(cfg.FetchTimeout)
	}
	if chunkSplitter == nil {
		var err error
		chunkSplitter, err = chunker.New(cfg.ChunkSize, cfg.ChunkOverlap)
		if err != nil {
			return nil, err
		}
	}
	return &Service{
		fetcher:    fetcher,
		chunker:    chunkSplitter,
		embedder:   embedder,
		store:      st,
		namespace:  cfg.Namespace,
		sourceType: cfg.SourceType,
		minCNChars: cfg.MinCNChars,
	}, nil
}

func (s *Service) IngestURLs(ctx context.Context, urls []string, opts Options) ([]Result, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if err := s.store.EnsureSchema(ctx); err != nil {
		return nil, err
	}

	deduped := DeduplicateURLs(urls)
	results := make([]Result, 0, len(deduped))
	for _, rawURL := range deduped {
		result, err := s.IngestURL(ctx, rawURL, opts)
		if err != nil {
			result.Status = "failed"
			if result.Message == "" {
				result.Message = err.Error()
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Service) IngestURL(ctx context.Context, rawURL string, opts Options) (Result, error) {
	rawURL = strings.TrimSpace(rawURL)
	result := Result{URL: rawURL}
	if err := s.ensureReady(); err != nil {
		result.Status = "failed"
		result.Message = err.Error()
		return result, err
	}
	if rawURL == "" {
		result.Status = "skipped"
		result.Message = "url is empty"
		return result, nil
	}

	namespace, sourceType := s.resolveOptions(opts)
	rawHTML, err := s.fetcher.Fetch(ctx, rawURL)
	if err != nil || strings.TrimSpace(rawHTML) == "" {
		result.Status = "failed"
		if err != nil {
			result.Message = err.Error()
		} else {
			result.Message = "empty response body"
		}
		return result, err
	}

	cleanedText, cnCount := htmlcleaner.CleanHTML(rawHTML)
	result.CNCharCount = cnCount
	if cnCount < s.minCNChars {
		result.Status = "skipped"
		result.Message = fmt.Sprintf("cleaned Chinese content too short: %d", cnCount)
		return result, nil
	}

	chunks := s.chunker.Split(cleanedText)
	if len(chunks) == 0 {
		result.Status = "skipped"
		result.Message = "chunk result is empty"
		return result, nil
	}

	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		texts[i] = chunk.Text
	}
	embeddings, err := s.embedder.EmbedTexts(ctx, texts)
	if err != nil {
		result.Status = "failed"
		result.Message = err.Error()
		return result, err
	}
	if len(embeddings) != len(chunks) {
		err := fmt.Errorf("embedding response count mismatch: got=%d want=%d", len(embeddings), len(chunks))
		result.Status = "failed"
		result.Message = err.Error()
		return result, err
	}

	title := ExtractTitle(rawHTML)
	chunkRecords := make([]store.ChunkRecord, len(chunks))
	for i, chunk := range chunks {
		chunkRecords[i] = store.ChunkRecord{
			ChunkIndex: chunk.Index,
			Content:    chunk.Text,
			Embedding:  embeddings[i],
			Metadata: mergeMetadata(map[string]any{
				"namespace":    namespace,
				"source_url":   rawURL,
				"source_type":  sourceType,
				"title":        title,
				"chunk_index":  chunk.Index,
				"start_offset": chunk.Start,
				"end_offset":   chunk.End,
			}, opts.Metadata),
		}
	}

	docID, err := s.store.UpsertDocumentWithChunks(ctx, store.DocumentInput{
		Namespace:   namespace,
		SourceType:  sourceType,
		SourceURL:   rawURL,
		Title:       title,
		RawHTML:     rawHTML,
		CleanedText: cleanedText,
		Metadata: mergeMetadata(map[string]any{
			"namespace":   namespace,
			"source_type": sourceType,
			"cn_count":    cnCount,
			"title":       title,
		}, opts.Metadata),
		Chunks: chunkRecords,
	})
	if err != nil {
		result.Status = "failed"
		result.Message = err.Error()
		return result, err
	}

	result.Status = "success"
	result.DocumentID = docID
	result.ChunkCount = len(chunkRecords)
	result.Message = fmt.Sprintf("ingested %d chunks", len(chunkRecords))
	return result, nil
}

func (s *Service) resolveOptions(opts Options) (string, string) {
	namespace := strings.TrimSpace(opts.Namespace)
	if namespace == "" {
		namespace = s.namespace
	}
	sourceType := strings.TrimSpace(opts.SourceType)
	if sourceType == "" {
		sourceType = s.sourceType
	}
	return namespace, sourceType
}

func (s *Service) ensureReady() error {
	if s == nil {
		return errors.New("ingest service is nil")
	}
	if s.fetcher == nil {
		return errors.New("ingest fetcher is nil")
	}
	if s.chunker == nil {
		return errors.New("ingest chunker is nil")
	}
	if s.embedder == nil {
		return errors.New("ingest embedder is nil")
	}
	if s.store == nil {
		return errors.New("ingest store is nil")
	}
	if strings.TrimSpace(s.namespace) == "" {
		return errors.New("ingest namespace is empty")
	}
	if strings.TrimSpace(s.sourceType) == "" {
		return errors.New("ingest source type is empty")
	}
	return nil
}

func withDefaults(cfg Config) Config {
	defaults := DefaultConfig()
	if strings.TrimSpace(cfg.Namespace) == "" {
		cfg.Namespace = defaults.Namespace
	}
	if strings.TrimSpace(cfg.SourceType) == "" {
		cfg.SourceType = defaults.SourceType
	}
	if cfg.MinCNChars <= 0 {
		cfg.MinCNChars = defaults.MinCNChars
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = defaults.ChunkSize
	}
	if cfg.ChunkOverlap < 0 {
		cfg.ChunkOverlap = defaults.ChunkOverlap
	}
	if cfg.ChunkOverlap == 0 && defaults.ChunkOverlap > 0 {
		cfg.ChunkOverlap = defaults.ChunkOverlap
	}
	if cfg.ChunkOverlap >= cfg.ChunkSize {
		cfg.ChunkOverlap = 0
	}
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = defaults.FetchTimeout
	}
	return cfg
}

func DeduplicateURLs(urls []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(urls))
	for _, rawURL := range urls {
		cleaned := strings.TrimSpace(rawURL)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

var titleRE = regexp.MustCompile(`(?is)<\s*title\b[^>]*>(.*?)<\s*/\s*title\s*>`)
var tagRE = regexp.MustCompile(`(?s)<[^>]+>`)
var spaceRE = regexp.MustCompile(`\s+`)

func ExtractTitle(rawHTML string) string {
	match := titleRE.FindStringSubmatch(rawHTML)
	if len(match) < 2 {
		return ""
	}
	title := tagRE.ReplaceAllString(match[1], " ")
	title = html.UnescapeString(title)
	title = strings.TrimSpace(spaceRE.ReplaceAllString(title, " "))
	return truncateRunes(title, 200)
}

func mergeMetadata(base, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}
