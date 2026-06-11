package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/llm"
	ragembedding "smartinsure-eino-backend/internal/rag/embedding"
	ragproduct "smartinsure-eino-backend/internal/rag/productdetail"
	"smartinsure-eino-backend/internal/rag/store"
	skillproduct "smartinsure-eino-backend/internal/skill/productdetail"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const providerConfigPath = "configs/llm_providers.yaml"

type cliOptions struct {
	Limit              int
	Namespace          string
	Platform           string
	PromptVersion      string
	MinMatchRate       float64
	MaxTagChunks       int
	SourceChunkSize    int
	SourceChunkOverlap int
	RequireSource      bool
	EnsureSchema       bool
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
	settings := config.Load()
	if strings.TrimSpace(settings.MySQLDSN) == "" {
		return fmt.Errorf("MYSQL_DSN 未配置")
	}
	if strings.TrimSpace(settings.DatabaseURL) == "" {
		return fmt.Errorf("DATABASE_URL 未配置")
	}

	mysqlRepo, err := skillproduct.OpenMySQLDetailRepository(settings.MySQLDSN)
	if err != nil {
		return err
	}
	defer mysqlRepo.Close()

	pgDB, err := sql.Open("pgx", settings.DatabaseURL)
	if err != nil {
		return err
	}
	defer pgDB.Close()

	embedder, err := newEmbedder(ctx, settings)
	if err != nil {
		return err
	}
	pgStore := store.NewPgVectorStore(pgDB)
	if opts.EnsureSchema {
		if err := pgStore.EnsureSchema(ctx); err != nil {
			return err
		}
	}
	namespace := firstNonEmpty(opts.Namespace, ragproduct.DefaultNamespace)
	ingestor, err := ragproduct.NewIngestor(embedder, pgStore, ragproduct.IngestConfig{
		Namespace:          namespace,
		SourceType:         ragproduct.DefaultSourceType,
		MinMatchRate:       opts.MinMatchRate,
		MaxTagChunks:       opts.MaxTagChunks,
		SourceChunkSize:    opts.SourceChunkSize,
		SourceChunkOverlap: opts.SourceChunkOverlap,
	})
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	var afterUpdatedAt *time.Time
	afterProductKey := ""
	total := 0
	success := 0
	skipped := 0
	failed := 0
	for {
		records, err := mysqlRepo.ListActive(ctx, skillproduct.ListProductDetailParams{
			Limit:           limit,
			Platform:        opts.Platform,
			PromptVersion:   opts.PromptVersion,
			MinMatchRate:    opts.MinMatchRate,
			Now:             now,
			AfterUpdatedAt:  afterUpdatedAt,
			AfterProductKey: afterProductKey,
			RequireSource:   opts.RequireSource,
		})
		if err != nil {
			return err
		}
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			total++
			input := detailInputFromStored(record, namespace)
			result, err := ingestor.Ingest(ctx, input)
			if err != nil && result.Message == "" {
				result.Message = err.Error()
			}
			switch result.Status {
			case "success":
				success++
			case "skipped":
				skipped++
			default:
				failed++
			}
			fmt.Printf("[%s] product_key=%s document_id=%d chunks=%d message=%s\n",
				result.Status,
				result.ProductKey,
				result.DocumentID,
				result.ChunkCount,
				result.Message,
			)
		}
		last := records[len(records)-1].Detail
		after := last.UpdatedAt
		afterUpdatedAt = &after
		afterProductKey = last.ProductKey
		if len(records) < limit {
			break
		}
	}
	fmt.Printf("done: total=%d success=%d skipped=%d failed=%d\n", total, success, skipped, failed)
	return nil
}

func parseArgs(args []string) (cliOptions, error) {
	var opts cliOptions
	fs := flag.NewFlagSet("ingestproductdetails", flag.ContinueOnError)
	fs.IntVar(&opts.Limit, "limit", 100, "每批读取 product_details 的数量")
	fs.StringVar(&opts.Namespace, "namespace", ragproduct.DefaultNamespace, "RAG namespace")
	fs.StringVar(&opts.Platform, "platform", "", "只同步指定平台，例如 huize 或 pingan")
	fs.StringVar(&opts.PromptVersion, "prompt-version", "", "只同步指定解析 prompt version")
	fs.Float64Var(&opts.MinMatchRate, "min-match-rate", 0.6, "最低 AI 解析匹配率")
	fs.IntVar(&opts.MaxTagChunks, "max-tag-chunks", 6, "单个产品最多生成的 product_tag chunk 数")
	fs.IntVar(&opts.SourceChunkSize, "source-chunk-size", 1200, "原文片段 chunk size")
	fs.IntVar(&opts.SourceChunkOverlap, "source-chunk-overlap", 200, "原文片段 overlap")
	fs.BoolVar(&opts.RequireSource, "require-source", false, "只同步已有 product_detail_sources 的记录")
	fs.BoolVar(&opts.EnsureSchema, "ensure-schema", true, "入库前确保 pgvector schema 存在")
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	return opts, nil
}

func detailInputFromStored(record skillproduct.StoredProductDetailWithSource, namespace string) ragproduct.DetailInput {
	detail := record.Detail
	urlHash := detail.URLHash
	sources := make([]ragproduct.SourceSnapshot, 0, 1)
	if record.Source != nil {
		source := record.Source
		if urlHash == "" {
			urlHash = source.NormalizedURLHash
		}
		sources = append(sources, ragproduct.SourceSnapshot{
			SourceURL:        source.SourceURL,
			OriginSourceType: source.SourceType,
			SourceFormat:     source.SourceFormat,
			CleanedText:      source.CleanedText,
			ContentHash:      source.ContentHash,
			CNCharCount:      source.CNCharCount,
			FetchedAt:        source.FetchedAt,
		})
	}
	return ragproduct.DetailInput{
		Namespace:         namespace,
		ProductKey:        detail.ProductKey,
		ProductName:       detail.ProductName,
		Platform:          detail.Platform,
		CanonicalURL:      detail.CanonicalURL,
		NormalizedURLHash: urlHash,
		SourceHash:        detail.SourceHash,
		PromptVersion:     detail.PromptVersion,
		ModelName:         detail.ModelName,
		Status:            detail.Status,
		ExpiresAt:         detail.ExpiresAt,
		Detail:            detail.Detail,
		Sources:           sources,
	}
}

func newEmbedder(ctx context.Context, settings config.Settings) (ragembedding.Embedder, error) {
	apiBase := settings.EmbeddingAPIBase
	apiKey := settings.EmbeddingAPIKey
	provider := ragembedding.Provider(settings.EmbeddingProvider)
	if providerUsesLLMFallback(provider) && (apiBase == "" || apiKey == "") {
		registry, err := llm.LoadRegistry(providerConfigPath, settings)
		if err == nil {
			providerCfg := registry.ForStage("answer")
			if apiBase == "" {
				apiBase = providerCfg.Base
			}
			if apiKey == "" {
				apiKey = providerCfg.Key
			}
		}
	}
	if providerUsesLLMFallback(provider) && apiBase == "" {
		apiBase = settings.LLMAPIBase
	}
	if providerUsesLLMFallback(provider) && apiKey == "" {
		apiKey = settings.LLMAPIKey
	}
	return ragembedding.NewEmbedder(ctx, ragembedding.Config{
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

func providerUsesLLMFallback(provider ragembedding.Provider) bool {
	value := strings.TrimSpace(strings.ToLower(string(provider)))
	return value == "" || value == string(ragembedding.ProviderOpenAICompatible) || value == "openai-compatible" || value == "openai"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
