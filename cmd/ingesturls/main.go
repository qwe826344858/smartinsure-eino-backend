package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/rag/embedding"
	"smartinsure-eino-backend/internal/rag/ingest"
	"smartinsure-eino-backend/internal/rag/store"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const providerConfigPath = "configs/llm_providers.yaml"

type cliOptions struct {
	URLs         multiFlag
	InputFile    string
	Namespace    string
	SourceType   string
	MetadataJSON string
}

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		*m = append(*m, value)
	}
	return nil
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}
	urls, err := loadURLs(opts)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		return fmt.Errorf("未提供任何 URL，请使用 --url 或 --input-file")
	}
	metadata, err := parseMetadata(opts.MetadataJSON)
	if err != nil {
		return err
	}

	settings := config.Load()
	if strings.TrimSpace(settings.DatabaseURL) == "" {
		return fmt.Errorf("DATABASE_URL 未配置")
	}

	db, err := sql.Open("pgx", settings.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	embedder, err := newEmbedder(ctx, settings)
	if err != nil {
		return err
	}
	service, err := ingest.NewService(nil, embedder, store.NewPgVectorStore(db), ingest.Config{
		Namespace:    firstNonEmpty(opts.Namespace, settings.IngestNamespace),
		SourceType:   firstNonEmpty(opts.SourceType, "web_page"),
		MinCNChars:   settings.IngestMinCNChars,
		ChunkSize:    settings.IngestChunkSize,
		ChunkOverlap: settings.IngestChunkOverlap,
	})
	if err != nil {
		return err
	}

	results, err := service.IngestURLs(ctx, urls, ingest.Options{
		Namespace:  opts.Namespace,
		SourceType: opts.SourceType,
		Metadata:   metadata,
	})
	if err != nil {
		return err
	}

	success := 0
	for _, item := range results {
		if item.Status == "success" {
			success++
		}
		fmt.Printf("[%s] url=%s document_id=%d chunks=%d cn=%d message=%s\n",
			item.Status,
			item.URL,
			item.DocumentID,
			item.ChunkCount,
			item.CNCharCount,
			item.Message,
		)
	}
	fmt.Printf("done: success=%d/%d\n", success, len(results))
	return nil
}

func parseArgs(args []string) (cliOptions, error) {
	var opts cliOptions
	fs := flag.NewFlagSet("ingesturls", flag.ContinueOnError)
	fs.Var(&opts.URLs, "url", "单个待抓取 URL，可重复传入")
	fs.StringVar(&opts.InputFile, "input-file", "", "包含 URL 列表的文本文件，每行一个 URL")
	fs.StringVar(&opts.Namespace, "namespace", "", "入库命名空间，默认使用 INGEST_NAMESPACE")
	fs.StringVar(&opts.SourceType, "source-type", "web_page", "数据来源类型标记")
	fs.StringVar(&opts.MetadataJSON, "metadata-json", "{}", "额外 metadata，JSON 字符串")
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	return opts, nil
}

func loadURLs(opts cliOptions) ([]string, error) {
	urls := append([]string(nil), opts.URLs...)
	if opts.InputFile != "" {
		data, err := os.ReadFile(opts.InputFile)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			urls = append(urls, line)
		}
	}
	return ingest.DeduplicateURLs(urls), nil
}

func parseMetadata(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, fmt.Errorf("metadata-json 不是合法 JSON: %w", err)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	return metadata, nil
}

func newEmbedder(ctx context.Context, settings config.Settings) (embedding.Embedder, error) {
	apiBase := settings.EmbeddingAPIBase
	apiKey := settings.EmbeddingAPIKey
	provider := embedding.Provider(settings.EmbeddingProvider)
	if providerUsesLLMFallback(provider) && (apiBase == "" || apiKey == "") {
		registry, err := llm.LoadRegistry(providerConfigPath, settings)
		if err == nil {
			provider := registry.ForStage("answer")
			if apiBase == "" {
				apiBase = provider.Base
			}
			if apiKey == "" {
				apiKey = provider.Key
			}
		}
	}
	if providerUsesLLMFallback(provider) && apiBase == "" {
		apiBase = settings.LLMAPIBase
	}
	if providerUsesLLMFallback(provider) && apiKey == "" {
		apiKey = settings.LLMAPIKey
	}
	return embedding.NewEmbedder(ctx, embedding.Config{
		Provider:   provider,
		APIBase:    apiBase,
		APIKey:     apiKey,
		Model:      settings.EmbeddingModel,
		Region:     settings.EmbeddingRegion,
		APIType:    settings.EmbeddingAPIType,
		Timeout:    time.Duration(settings.EmbeddingTimeout) * time.Second,
		BatchSize:  settings.EmbeddingBatchSize,
		Dimensions: settings.EmbeddingDimensions,
		RetryTimes: settings.EmbeddingRetryTimes,
	})
}

func providerUsesLLMFallback(provider embedding.Provider) bool {
	value := strings.TrimSpace(strings.ToLower(string(provider)))
	return value == "" || value == string(embedding.ProviderOpenAICompatible) || value == "openai-compatible" || value == "openai"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
