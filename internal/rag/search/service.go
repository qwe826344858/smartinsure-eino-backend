package search

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"
	"unicode/utf8"

	"smartinsure-eino-backend/internal/rag/embedding"
	"smartinsure-eino-backend/internal/rag/productdetail"
	ragstore "smartinsure-eino-backend/internal/rag/store"
	"smartinsure-eino-backend/internal/schema"
)

var httpURLPattern = regexp.MustCompile(`https?://[^\s"'<>，。；、]+`)

type Config struct {
	Namespace string
	TopK      int
	MinScore  float64
	Timeout   time.Duration
}

type Options struct {
	Namespace string
	TopK      int
	MinScore  float64
	Filters   map[string]any
}

type Service struct {
	embedder embedding.Embedder
	store    ragstore.SearchStore
	cfg      Config
}

func NewService(embedder embedding.Embedder, store ragstore.SearchStore, cfg Config) (*Service, error) {
	if embedder == nil {
		return nil, errors.New("rag search embedder is nil")
	}
	if store == nil {
		return nil, errors.New("rag search store is nil")
	}
	if strings.TrimSpace(cfg.Namespace) == "" {
		cfg.Namespace = productdetail.DefaultNamespace
	}
	if cfg.TopK <= 0 {
		cfg.TopK = 5
	}
	return &Service{embedder: embedder, store: store, cfg: cfg}, nil
}

func (s *Service) Search(ctx context.Context, query string) ([]schema.SearchResultItem, error) {
	return s.SearchWithOptions(ctx, query, Options{})
}

func (s *Service) SearchWithOptions(ctx context.Context, query string, opts Options) ([]schema.SearchResultItem, error) {
	startedAt := time.Now()
	if s == nil {
		return nil, errors.New("rag search service is nil")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		logx.Printf("运行日志", "runtime log", "rag search skipped reason=empty_query")
		return nil, nil
	}
	timeout := s.cfg.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	namespace := firstNonEmpty(opts.Namespace, s.cfg.Namespace)
	topK := opts.TopK
	if topK <= 0 {
		topK = s.cfg.TopK
	}
	minScore := opts.MinScore
	if minScore == 0 {
		minScore = s.cfg.MinScore
	}
	logx.Printf("运行日志", "runtime log", "rag search start namespace=%s query_chars=%d top_k=%d min_score=%.3f filters=%d", namespace, utf8.RuneCountInString(query), topK, minScore, len(opts.Filters))
	embedStartedAt := time.Now()
	logRAGSearchEmbeddingInput(namespace, query)
	vectors, err := s.embedder.EmbedTexts(ctx, []string{query})
	if err != nil {
		logx.Printf("运行日志", "runtime log", "rag search failed stage=embedding namespace=%s duration_ms=%d err=%v", namespace, time.Since(embedStartedAt).Milliseconds(), err)
		return nil, err
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		err := fmt.Errorf("query embedding response invalid: got=%d", len(vectors))
		logx.Printf("运行日志", "runtime log", "rag search failed stage=embedding_response namespace=%s duration_ms=%d err=%v", namespace, time.Since(embedStartedAt).Milliseconds(), err)
		return nil, err
	}
	logx.Printf("运行日志", "runtime log", "rag search embedding_success namespace=%s vector_dims=%d duration_ms=%d", namespace, len(vectors[0]), time.Since(embedStartedAt).Milliseconds())
	results, err := s.store.SearchSimilarChunks(ctx, ragstore.SearchQuery{
		Namespace: namespace,
		Vector:    vectors[0],
		TopK:      topK,
		Filters:   cloneFilters(opts.Filters),
	})
	if err != nil {
		logx.Printf("运行日志", "runtime log", "rag search failed stage=store namespace=%s duration_ms=%d err=%v", namespace, time.Since(startedAt).Milliseconds(), err)
		return nil, err
	}
	logRAGSearchRecallResults(namespace, results)

	items := make([]schema.SearchResultItem, 0, len(results))
	for _, result := range results {
		if minScore > 0 && result.Score < minScore {
			continue
		}
		items = append(items, mapSearchResult(result))
	}
	logx.Printf("运行日志", "runtime log", "rag search success namespace=%s raw_results=%d returned=%d duration_ms=%d", namespace, len(results), len(items), time.Since(startedAt).Milliseconds())
	return items, nil
}

func mapSearchResult(result ragstore.SearchResult) schema.SearchResultItem {
	metadata := result.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	documentMetadata := result.DocumentMetadata
	if documentMetadata == nil {
		documentMetadata = map[string]any{}
	}
	productName := firstNonEmpty(stringMetadata(metadata, "product_name"), stringMetadata(documentMetadata, "product_name"), result.Title)
	title := productName
	if dutyName := stringMetadata(metadata, "duty_name"); dutyName != "" {
		title = firstNonEmpty(productName, result.Title) + " - " + dutyName
	}
	if tagName := stringMetadata(metadata, "tag_name"); tagName != "" && title == productName {
		title = firstNonEmpty(productName, result.Title) + " - " + tagName
	}
	if title == "" {
		title = result.Title
	}
	productURL := publicProductURL(result, metadata, documentMetadata)
	return schema.SearchResultItem{
		Title:       title,
		URL:         productURL,
		Site:        firstNonEmpty(stringMetadata(metadata, "platform"), stringMetadata(documentMetadata, "platform"), "SmartInsure RAG"),
		Snippet:     truncateRunes(strings.TrimSpace(result.Content), 500),
		ProductURL:  productURL,
		ProductName: productName,
		Tags:        resultTags(metadata, documentMetadata),
	}
}

func logRAGSearchEmbeddingInput(namespace, query string) {
	logx.Printf(
		"运行日志",
		"runtime log",
		"rag search embedding_input namespace=%s query_chars=%d query=%q",
		namespace,
		utf8.RuneCountInString(query),
		truncateRunes(query, 1000),
	)
}

func logRAGSearchRecallResults(namespace string, results []ragstore.SearchResult) {
	for index, result := range results {
		metadata := result.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		documentMetadata := result.DocumentMetadata
		if documentMetadata == nil {
			documentMetadata = map[string]any{}
		}
		chunkType := stringMetadata(metadata, "chunk_type")
		dutyName := stringMetadata(metadata, "duty_name")
		tagName := stringMetadata(metadata, "tag_name")
		productName := firstNonEmpty(stringMetadata(metadata, "product_name"), stringMetadata(documentMetadata, "product_name"), result.Title)
		sourceURL := publicProductURL(result, metadata, documentMetadata)
		content := strings.TrimSpace(result.Content)
		logx.Printf(
			"召回内容",
			"retrieval content",
			"rag search recall_result namespace=%s rank=%d score=%.6f distance=%.6f chunk_index=%d chunk_type=%s product_name=%q duty_name=%q tag_name=%q source_url=%s content_chars=%d content=%q",
			namespace,
			index+1,
			result.Score,
			result.Distance,
			result.ChunkIndex,
			chunkType,
			productName,
			dutyName,
			tagName,
			sourceURL,
			utf8.RuneCountInString(content),
			truncateRunes(content, 1200),
		)
	}
}

func publicProductURL(result ragstore.SearchResult, metadata, documentMetadata map[string]any) string {
	return firstHTTPURL(
		stringMetadata(metadata, "product_url"),
		stringMetadata(metadata, "canonical_url"),
		stringMetadata(metadata, "source_url"),
		stringMetadata(documentMetadata, "product_url"),
		stringMetadata(documentMetadata, "canonical_url"),
		stringMetadata(documentMetadata, "source_url"),
		firstHTTPURLFromText(result.Content),
		result.SourceURL,
	)
}

func firstHTTPURL(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			return value
		}
	}
	return ""
}

func firstHTTPURLFromText(text string) string {
	match := httpURLPattern.FindString(strings.TrimSpace(text))
	return strings.TrimRight(match, "）)]}")
}

func resultTags(metadata, documentMetadata map[string]any) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 6)
	add := func(values ...string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	add(stringSliceMetadata(metadata, "tags")...)
	add(stringSliceMetadata(documentMetadata, "tags")...)
	add(stringMetadata(metadata, "tag_name"))
	add(stringSliceMetadata(metadata, "duty_tags")...)
	if len(out) > 5 {
		return out[:5]
	}
	return out
}

func stringSliceMetadata(metadata map[string]any, key string) []string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		text := strings.TrimSpace(fmt.Sprint(typed))
		if text == "" {
			return nil
		}
		return []string{text}
	}
}

func stringMetadata(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func cloneFilters(filters map[string]any) map[string]any {
	if filters == nil {
		return nil
	}
	out := make(map[string]any, len(filters))
	for key, value := range filters {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncateRunes(text string, max int) string {
	if max <= 0 || utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	return string(runes[:max]) + "..."
}
