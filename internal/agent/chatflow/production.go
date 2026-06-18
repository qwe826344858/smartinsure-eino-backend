package chatflow

import (
	"context"
	"database/sql"
	"errors"
	"smartinsure-eino-backend/internal/logx"
	"strconv"
	"strings"
	"sync"
	"time"

	"smartinsure-eino-backend/internal/compliance"
	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/platform"
	ragembedding "smartinsure-eino-backend/internal/rag/embedding"
	ragproduct "smartinsure-eino-backend/internal/rag/productdetail"
	ragsearch "smartinsure-eino-backend/internal/rag/search"
	ragstore "smartinsure-eino-backend/internal/rag/store"
	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/search/fallback"
	"smartinsure-eino-backend/internal/service/answer"
	"smartinsure-eino-backend/internal/service/followup"
	"smartinsure-eino-backend/internal/service/intent"
	"smartinsure-eino-backend/internal/service/productsearch"
	"smartinsure-eino-backend/internal/skill/productdetail"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultProviderConfigPath = "configs/llm_providers.yaml"

const (
	OrchestratorLite      = "lite"
	OrchestratorEinoGraph = "eino_graph"
)

func NewProductionRunner() Runner {
	settings := config.Load()
	flow := NewProduction()
	switch strings.ToLower(strings.TrimSpace(settings.Orchestrator)) {
	case "eino", "graph", OrchestratorEinoGraph:
		opts := []GraphOption{}
		if models, err := graphChatModelsForProduction(settings); err == nil {
			opts = append(opts, WithGraphChatModels(models))
		} else {
			logx.Printf("运行日志", "runtime log", "failed to initialize eino chat models, using graph fallback adapters: %v", err)
		}
		runner, err := NewGraphFlow(flow, opts...)
		if err != nil {
			logx.Printf("运行日志", "runtime log", "failed to compile eino graph orchestrator, falling back to lite: %v", err)
			return flow
		}
		return runner
	default:
		return flow
	}
}

func graphChatModelsForProduction(settings config.Settings) (GraphChatModels, error) {
	registry, err := llm.LoadRegistry(defaultProviderConfigPath, settings)
	if err != nil {
		return GraphChatModels{}, err
	}
	ctx := context.Background()
	intentModel, _, err := registry.EinoChatModelForStage(ctx, "intent")
	if err != nil {
		return GraphChatModels{}, err
	}
	followupModel, _, err := registry.EinoChatModelForStage(ctx, "followup")
	if err != nil {
		return GraphChatModels{}, err
	}
	answerModel, _, err := registry.EinoChatModelForStage(ctx, "answer")
	if err != nil {
		return GraphChatModels{}, err
	}
	return GraphChatModels{
		Intent:   intentModel,
		Followup: followupModel,
		Answer:   answerModel,
	}, nil
}

func NewProduction() *Flow {
	flow := New()
	settings := config.Load()
	detailOpts := productionDetailOptions(settings)

	flow.Search = productSearchAdapter{service: productsearch.New()}
	fallbackSvc := fallback.NewService(nil)
	flow.Fallback = fallbackAdapter{service: fallbackSvc}
	if ragFallback := productionRAGFallbackSearcher(settings, fallbackSvc); ragFallback != nil {
		flow.Fallback = ragFallback
	}
	flow.Detail = detailAdapter{service: productdetail.NewService(detailOpts...)}
	if priceLookup := productionProductPriceLookup(settings); priceLookup != nil {
		flow.Prices = priceLookup
	}

	registry, err := llm.LoadRegistry(defaultProviderConfigPath, settings)
	if err != nil {
		return flow
	}

	if model := modelForStage(registry, settings, "intent"); model != nil {
		flow.Intent = intentAdapter{service: intent.NewService(model)}
	}
	if model := modelForStage(registry, settings, "answer"); model != nil {
		flow.Answer = answerAdapter{service: answer.NewService(model)}
	}
	if model := modelForStage(registry, settings, "followup"); model != nil {
		flow.Followup = followupAdapter{service: followup.NewService(model)}
	}
	if model := modelForStage(registry, settings, "detail"); model != nil {
		opts := append([]productdetail.Option{}, detailOpts...)
		opts = append(opts, productdetail.WithModel(model))
		flow.Detail = detailAdapter{service: productdetail.NewService(opts...)}
	}
	return flow
}

func productionProductPriceLookup(settings config.Settings) ProductPriceLookup {
	if strings.TrimSpace(settings.MySQLDSN) == "" {
		return nil
	}
	deps := sharedProductionDetailDeps(settings)
	if deps.repository == nil {
		return nil
	}
	return productDetailPriceLookupAdapter{repository: deps.repository}
}

type productionDetailDeps struct {
	repository productdetail.DetailRepository
	hotCache   productdetail.DetailHotCache
}

type productDetailPriceLookupAdapter struct {
	repository productdetail.DetailRepository
}

func (a productDetailPriceLookupAdapter) LookupProductPrice(ctx context.Context, productURL string) (ProductPrice, bool, error) {
	if a.repository == nil || strings.TrimSpace(productURL) == "" {
		return ProductPrice{}, false, nil
	}
	record, err := a.repository.GetByURL(ctx, productURL)
	if err != nil {
		if errors.Is(err, productdetail.ErrProductDetailNotFound) {
			return ProductPrice{}, false, nil
		}
		return ProductPrice{}, false, err
	}
	price := strings.TrimSpace(record.Price)
	priceLabel := strings.TrimSpace(record.PriceLabel)
	if price == "" {
		price = strings.TrimSpace(record.Detail.Price)
	}
	if priceLabel == "" {
		priceLabel = strings.TrimSpace(record.Detail.PriceLabel)
	}
	if priceLabel == "" && price != "" {
		priceLabel = price
	}
	if price == "" && priceLabel == "" {
		return ProductPrice{}, false, nil
	}
	return ProductPrice{Price: price, PriceLabel: priceLabel}, true, nil
}

type productionRAGDeps struct {
	searcher              schemaKnowledgeSearcher
	productDetailIngestor productdetail.ProductDetailRAGIngestor
	db                    *sql.DB
}

var (
	productionDetailDepsMu    sync.Mutex
	productionDetailDepsByKey = map[string]productionDetailDeps{}
	productionRAGDepsMu       sync.Mutex
	productionRAGDepsByKey    = map[string]productionRAGDeps{}
)

func productionDetailOptions(settings config.Settings) []productdetail.Option {
	opts := []productdetail.Option{
		productdetail.WithProductKeyer(productdetail.NewProductKeyer()),
		productdetail.WithPromptVersion(settings.ProductDetailPromptVersion),
		productdetail.WithSharedCacheTTL(
			time.Duration(settings.ProductDetailRedisTTL)*time.Second,
			time.Duration(settings.ProductDetailAliasRedisTTL)*time.Second,
			time.Duration(settings.ProductDetailDBTTL)*time.Second,
			time.Duration(settings.ProductDetailLockTTL)*time.Second,
		),
	}
	if !settings.ProductDetailSharedCacheEnabled {
		return opts
	}
	deps := sharedProductionDetailDeps(settings)
	if deps.repository != nil {
		opts = append(opts, productdetail.WithRepository(deps.repository))
	}
	if deps.hotCache != nil {
		opts = append(opts, productdetail.WithHotCache(deps.hotCache))
	}
	if ragIngestor := productionProductDetailRAGIngestor(settings, deps.repository); ragIngestor != nil {
		opts = append(opts, productdetail.WithRAGIngestor(ragIngestor))
	}
	return opts
}

func sharedProductionDetailDeps(settings config.Settings) productionDetailDeps {
	key := strings.Join([]string{
		strings.TrimSpace(settings.MySQLDSN),
		strings.TrimSpace(settings.RedisURL),
		settings.ProductDetailPromptVersion,
	}, "\x00")
	productionDetailDepsMu.Lock()
	if deps, ok := productionDetailDepsByKey[key]; ok {
		productionDetailDepsMu.Unlock()
		return deps
	}
	productionDetailDepsMu.Unlock()

	deps := productionDetailDeps{}
	if strings.TrimSpace(settings.MySQLDSN) != "" {
		repo, err := productdetail.OpenMySQLDetailRepository(settings.MySQLDSN)
		if err != nil {
			logx.Printf("运行日志", "runtime log", "product detail mysql repository disabled: %v", err)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := repo.EnsureSchema(ctx); err != nil {
				logx.Printf("运行日志", "runtime log", "product detail mysql repository disabled: %v", err)
				_ = repo.Close()
			} else {
				deps.repository = repo
			}
			cancel()
		}
	}
	if strings.TrimSpace(settings.RedisURL) != "" {
		cache, err := productdetail.NewRedisDetailHotCacheFromURL(settings.RedisURL)
		if err != nil {
			logx.Printf("运行日志", "runtime log", "product detail redis hot cache disabled: %v", err)
		} else {
			deps.hotCache = cache
		}
	}

	productionDetailDepsMu.Lock()
	if existing, ok := productionDetailDepsByKey[key]; ok {
		productionDetailDepsMu.Unlock()
		closeUnusedProductionDetailDeps(deps)
		return existing
	}
	productionDetailDepsByKey[key] = deps
	productionDetailDepsMu.Unlock()
	return deps
}

func closeUnusedProductionDetailDeps(deps productionDetailDeps) {
	if repo, ok := deps.repository.(*productdetail.MySQLDetailRepository); ok {
		_ = repo.Close()
	}
	if cache, ok := deps.hotCache.(*productdetail.RedisDetailHotCache); ok {
		_ = cache.Close()
	}
}

func productionRAGFallbackSearcher(settings config.Settings, fallbackSvc *fallback.Service) FallbackSearcher {
	if !settings.RAGSearchEnabled {
		return nil
	}
	deps := sharedProductionRAGDeps(settings, nil)
	if deps.searcher == nil {
		return nil
	}
	return ragFallbackAdapter{primary: deps.searcher, fallback: fallbackSvc}
}

// NewProductionRAGOnlySearcher 返回只查 pgvector RAG 的知识检索器。
// 专用 RAG Agent 使用该检索器，避免在产品匹配场景中混入本地静态 fallback 知识。
func NewProductionRAGOnlySearcher() FallbackSearcher {
	settings := config.Load()
	if !settings.RAGSearchEnabled {
		logx.Printf("运行日志", "runtime log", "rag only search disabled: RAG_SEARCH_ENABLED=false")
		return nil
	}
	deps := sharedProductionRAGDeps(settings, nil)
	if deps.searcher == nil {
		logx.Printf("运行日志", "runtime log", "rag only search disabled: searcher_unavailable")
		return nil
	}
	return ragOnlyAdapter{primary: deps.searcher}
}

type productDetailRAGIngestAdapter struct {
	ingestor  *ragproduct.Ingestor
	namespace string
}

func (a productDetailRAGIngestAdapter) IngestProductDetail(ctx context.Context, record productdetail.StoredProductDetailWithSource) error {
	if a.ingestor == nil {
		return nil
	}
	startedAt := time.Now()
	logx.Printf("运行日志", "runtime log", "product_detail rag_adapter_ingest_start namespace=%s product_key=%s duties=%d", a.namespace, logShort(record.Detail.ProductKey), len(record.Detail.Detail.Duties))
	result, err := a.ingestor.Ingest(ctx, productDetailRAGInputFromStored(record, a.namespace))
	if err != nil {
		logx.Printf("运行日志", "runtime log", "product_detail rag_adapter_ingest_failed namespace=%s product_key=%s duration_ms=%d err=%v", a.namespace, logShort(record.Detail.ProductKey), time.Since(startedAt).Milliseconds(), err)
		return err
	}
	if result.Status == "failed" {
		logx.Printf("运行日志", "runtime log", "product_detail rag_adapter_ingest_failed namespace=%s product_key=%s duration_ms=%d message=%s", a.namespace, logShort(record.Detail.ProductKey), time.Since(startedAt).Milliseconds(), result.Message)
		return errors.New(result.Message)
	}
	logx.Printf("运行日志", "runtime log", "product_detail rag_adapter_ingest_done namespace=%s product_key=%s status=%s document_id=%d chunks=%d duration_ms=%d", a.namespace, logShort(record.Detail.ProductKey), result.Status, result.DocumentID, result.ChunkCount, time.Since(startedAt).Milliseconds())
	return nil
}

func productionProductDetailRAGIngestor(settings config.Settings, repository productdetail.DetailRepository) productdetail.ProductDetailRAGIngestor {
	if !settings.ProductDetailRAGEnabled {
		return nil
	}
	deps := sharedProductionRAGDeps(settings, repository)
	return deps.productDetailIngestor
}

func sharedProductionRAGDeps(settings config.Settings, repository productdetail.DetailRepository) productionRAGDeps {
	if !settings.RAGSearchEnabled && !settings.ProductDetailRAGEnabled {
		return productionRAGDeps{}
	}
	key := productionRAGDepsKey(settings, repository != nil)
	productionRAGDepsMu.Lock()
	if deps, ok := productionRAGDepsByKey[key]; ok {
		productionRAGDepsMu.Unlock()
		return deps
	}
	productionRAGDepsMu.Unlock()

	deps := buildProductionRAGDeps(settings, repository)
	productionRAGDepsMu.Lock()
	if existing, ok := productionRAGDepsByKey[key]; ok {
		productionRAGDepsMu.Unlock()
		closeUnusedProductionRAGDeps(deps)
		return existing
	}
	productionRAGDepsByKey[key] = deps
	productionRAGDepsMu.Unlock()
	return deps
}

func buildProductionRAGDeps(settings config.Settings, repository productdetail.DetailRepository) productionRAGDeps {
	deps := productionRAGDeps{}
	if strings.TrimSpace(settings.DatabaseURL) == "" {
		logx.Printf("运行日志", "runtime log", "rag disabled: DATABASE_URL is empty")
		return deps
	}
	logx.Printf("运行日志", "runtime log", "rag init_start search_enabled=%t ingest_enabled=%t search_namespace=%s ingest_namespace=%s embedding_provider=%s embedding_model=%s top_k=%d min_score=%.3f", settings.RAGSearchEnabled, settings.ProductDetailRAGEnabled, settings.RAGSearchNamespace, settings.ProductDetailRAGNamespace, settings.EmbeddingProvider, settings.EmbeddingModel, settings.RAGSearchTopK, settings.RAGSearchMinScore)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	embedder, err := productionRAGEmbedder(ctx, settings)
	if err != nil {
		logx.Printf("运行日志", "runtime log", "rag disabled: %v", err)
		return deps
	}
	db, err := sql.Open("pgx", settings.DatabaseURL)
	if err != nil {
		logx.Printf("运行日志", "runtime log", "rag disabled: %v", err)
		return deps
	}
	deps.db = db
	pgStore := ragstore.NewPgVectorStore(db)
	if settings.ProductDetailRAGEnabled {
		if err := pgStore.EnsureSchema(ctx); err != nil {
			logx.Printf("运行日志", "runtime log", "product detail rag ingest disabled: %v", err)
		} else {
			deps.productDetailIngestor = buildProductionProductDetailRAGIngestor(settings, embedder, pgStore, repository)
			if deps.productDetailIngestor != nil {
				logx.Printf("运行日志", "runtime log", "product_detail rag_ingest_enabled namespace=%s async_workers=%d queue_size=%d timeout_seconds=%d", settings.ProductDetailRAGNamespace, settings.ProductDetailRAGAsyncWorkers, settings.ProductDetailRAGQueueSize, settings.ProductDetailRAGAsyncTimeout)
			}
		}
	}
	if settings.RAGSearchEnabled {
		service, err := ragsearch.NewService(embedder, pgStore, ragsearch.Config{
			Namespace: strings.TrimSpace(settings.RAGSearchNamespace),
			TopK:      settings.RAGSearchTopK,
			MinScore:  settings.RAGSearchMinScore,
			Timeout:   time.Duration(settings.RAGSearchTimeout) * time.Second,
		})
		if err != nil {
			logx.Printf("运行日志", "runtime log", "rag fallback search disabled: %v", err)
		} else {
			deps.searcher = service
			logx.Printf("运行日志", "runtime log", "rag search_enabled namespace=%s top_k=%d min_score=%.3f timeout_seconds=%d", settings.RAGSearchNamespace, settings.RAGSearchTopK, settings.RAGSearchMinScore, settings.RAGSearchTimeout)
		}
	}
	if deps.searcher == nil && deps.productDetailIngestor == nil {
		closeUnusedProductionRAGDeps(deps)
		return productionRAGDeps{}
	}
	logx.Printf("运行日志", "runtime log", "rag init_success search_enabled=%t ingest_enabled=%t", deps.searcher != nil, deps.productDetailIngestor != nil)
	return deps
}

func buildProductionProductDetailRAGIngestor(settings config.Settings, embedder ragembedding.Embedder, pgStore *ragstore.PgVectorStore, repository productdetail.DetailRepository) productdetail.ProductDetailRAGIngestor {
	namespace := strings.TrimSpace(settings.ProductDetailRAGNamespace)
	if namespace == "" {
		namespace = ragproduct.DefaultNamespace
	}
	ingestor, err := ragproduct.NewIngestor(embedder, pgStore, ragproduct.IngestConfig{
		Namespace:          namespace,
		SourceType:         ragproduct.DefaultSourceType,
		MinMatchRate:       settings.ProductDetailRAGMinMatchRate,
		MaxTagChunks:       6,
		SourceChunkSize:    settings.IngestChunkSize,
		SourceChunkOverlap: settings.IngestChunkOverlap,
	})
	if err != nil {
		logx.Printf("运行日志", "runtime log", "product detail rag ingest disabled: %v", err)
		return nil
	}
	var inner productdetail.ProductDetailRAGIngestor = productDetailRAGIngestAdapter{ingestor: ingestor, namespace: namespace}
	if repository != nil {
		inner = productdetail.NewRAGStatusTrackingIngestor(inner, repository)
	}
	async, err := productdetail.NewAsyncProductDetailRAGIngestor(inner, productdetail.AsyncRAGIngestorConfig{
		Workers:   settings.ProductDetailRAGAsyncWorkers,
		QueueSize: settings.ProductDetailRAGQueueSize,
		Timeout:   time.Duration(settings.ProductDetailRAGAsyncTimeout) * time.Second,
	})
	if err != nil {
		logx.Printf("运行日志", "runtime log", "product detail rag ingest disabled: %v", err)
		return nil
	}
	return async
}

func productionRAGDepsKey(settings config.Settings, trackProductDetailStatus bool) string {
	return strings.Join([]string{
		strings.TrimSpace(settings.DatabaseURL),
		strings.TrimSpace(settings.MySQLDSN),
		boolKey(trackProductDetailStatus),
		strings.TrimSpace(settings.EmbeddingProvider),
		strings.TrimSpace(settings.EmbeddingModel),
		strings.TrimSpace(settings.EmbeddingAPIBase),
		strings.TrimSpace(settings.EmbeddingRegion),
		strings.TrimSpace(settings.EmbeddingAPIType),
		strings.TrimSpace(settings.RAGSearchNamespace),
		strings.TrimSpace(settings.ProductDetailRAGNamespace),
		boolKey(settings.RAGSearchEnabled),
		boolKey(settings.ProductDetailRAGEnabled),
		intKey(settings.EmbeddingTimeout),
		intKey(settings.EmbeddingBatchSize),
		intKey(settings.EmbeddingDimensions),
		intKey(settings.EmbeddingRetryTimes),
		intKey(settings.RAGSearchTopK),
		floatKey(settings.RAGSearchMinScore),
		intKey(settings.RAGSearchTimeout),
		floatKey(settings.ProductDetailRAGMinMatchRate),
		intKey(settings.ProductDetailRAGAsyncTimeout),
		intKey(settings.ProductDetailRAGAsyncWorkers),
		intKey(settings.ProductDetailRAGQueueSize),
	}, "\x00")
}

func closeUnusedProductionRAGDeps(deps productionRAGDeps) {
	if closer, ok := deps.productDetailIngestor.(interface{ Close() }); ok {
		closer.Close()
	}
	if deps.db != nil {
		_ = deps.db.Close()
	}
}

func productDetailRAGInputFromStored(record productdetail.StoredProductDetailWithSource, namespace string) ragproduct.DetailInput {
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

func productionRAGEmbedder(ctx context.Context, settings config.Settings) (ragembedding.Embedder, error) {
	apiBase := settings.EmbeddingAPIBase
	apiKey := settings.EmbeddingAPIKey
	provider := ragembedding.Provider(settings.EmbeddingProvider)
	if ragProviderUsesLLMFallback(provider) && (apiBase == "" || apiKey == "") {
		registry, err := llm.LoadRegistry(defaultProviderConfigPath, settings)
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
	if ragProviderUsesLLMFallback(provider) && apiBase == "" {
		apiBase = settings.LLMAPIBase
	}
	if ragProviderUsesLLMFallback(provider) && apiKey == "" {
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

func ragProviderUsesLLMFallback(provider ragembedding.Provider) bool {
	value := strings.TrimSpace(strings.ToLower(string(provider)))
	return value == "" || value == string(ragembedding.ProviderOpenAICompatible) || value == "openai-compatible" || value == "openai"
}

func boolKey(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func intKey(value int) string {
	return strconv.Itoa(value)
}

func floatKey(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func modelForStage(registry *llm.Registry, settings config.Settings, stage string) llm.ChatModel {
	if registry == nil {
		return nil
	}
	provider := registry.ForStage(stage)
	if provider.Key == "" || provider.Base == "" || provider.Model == "" {
		return nil
	}
	return llm.NewClient(
		provider,
		time.Duration(settings.LLMTimeout)*time.Second,
		settings.LLMMaxRetries,
	)
}

type intentAdapter struct {
	service *intent.Service
}

func (a intentAdapter) Classify(ctx context.Context, message string) (IntentResult, error) {
	result, err := a.service.Classify(ctx, message)
	return toChatflowIntentResult(result, err)
}

func (a intentAdapter) ClassifyWithHistory(ctx context.Context, message string, history []ChatMessage) (IntentResult, error) {
	result, err := a.service.ClassifyWithHistory(ctx, message, toIntentHistory(history))
	return toChatflowIntentResult(result, err)
}

func toChatflowIntentResult(result schema.IntentResult, err error) (IntentResult, error) {
	if err != nil {
		return IntentResult{}, err
	}
	return IntentResult{
		Intent:        result.Intent,
		NeedsFollowup: result.NeedsFollowup,
		MissingSlots:  result.MissingSlots,
		Reason:        result.Reason,
	}, nil
}

type answerAdapter struct {
	service *answer.Service
}

func (a answerAdapter) Stream(ctx context.Context, input AnswerInput) (<-chan string, <-chan error) {
	stream, err := a.service.GenerateStreamWithHistory(ctx, input.Message, input.Intent, toSchemaSearchResults(input.Results), toAnswerHistory(input.History))
	if err != nil {
		text := make(chan string)
		errs := make(chan error, 1)
		close(text)
		errs <- err
		close(errs)
		return text, errs
	}

	text := make(chan string)
	errs := make(chan error, 1)
	go func() {
		defer close(text)
		defer close(errs)
		for chunk := range stream {
			if chunk.Err != nil {
				errs <- chunk.Err
				return
			}
			if chunk.Text != "" {
				text <- compliance.Sanitize(chunk.Text)
			}
		}
	}()
	return text, errs
}

func toAnswerHistory(history []ChatMessage) []answer.HistoryMessage {
	out := make([]answer.HistoryMessage, 0, len(history))
	for _, item := range history {
		out = append(out, answer.HistoryMessage{
			ID:        item.ID,
			Role:      item.Role,
			Content:   item.Content,
			Metadata:  item.Metadata,
			CreatedAt: item.CreatedAt,
		})
	}
	return out
}

func toIntentHistory(history []ChatMessage) []intent.HistoryMessage {
	out := make([]intent.HistoryMessage, 0, len(history))
	for _, item := range history {
		out = append(out, intent.HistoryMessage{
			ID:        item.ID,
			Role:      item.Role,
			Content:   item.Content,
			Metadata:  item.Metadata,
			CreatedAt: item.CreatedAt,
		})
	}
	return out
}

type followupAdapter struct {
	service *followup.Service
}

func (a followupAdapter) Generate(ctx context.Context, missingSlots []string) (string, error) {
	text, err := a.service.Generate(ctx, missingSlots)
	return compliance.Sanitize(text), err
}

type fallbackAdapter struct {
	service *fallback.Service
}

func (a fallbackAdapter) Search(_ context.Context, query string) ([]SearchResultItem, error) {
	return fromSchemaSearchResults(a.service.Search(query)), nil
}

type schemaKnowledgeSearcher interface {
	Search(ctx context.Context, query string) ([]schema.SearchResultItem, error)
}

type ragFallbackAdapter struct {
	primary  schemaKnowledgeSearcher
	fallback *fallback.Service
}

type ragOnlyAdapter struct {
	primary schemaKnowledgeSearcher
}

func (a ragOnlyAdapter) Search(ctx context.Context, query string) ([]SearchResultItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	startedAt := time.Now()
	if a.primary == nil {
		logx.Printf("运行日志", "runtime log", "rag only search skipped reason=primary_unavailable query_chars=%d", len([]rune(strings.TrimSpace(query))))
		return nil, nil
	}
	results, err := a.primary.Search(ctx, query)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		logx.Printf("运行日志", "runtime log", "rag only search failed query_chars=%d duration_ms=%d err=%v", len([]rune(strings.TrimSpace(query))), time.Since(startedAt).Milliseconds(), err)
		return nil, err
	}
	logx.Printf("运行日志", "runtime log", "rag only search done query_chars=%d results=%d duration_ms=%d", len([]rune(strings.TrimSpace(query))), len(results), time.Since(startedAt).Milliseconds())
	return fromSchemaSearchResults(results), nil
}

func (a ragFallbackAdapter) Search(ctx context.Context, query string) ([]SearchResultItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	startedAt := time.Now()
	if a.primary != nil {
		results, err := a.primary.Search(ctx, query)
		if err == nil && len(results) > 0 {
			logx.Printf("运行日志", "runtime log", "rag fallback primary_hit query_chars=%d results=%d duration_ms=%d", len([]rune(strings.TrimSpace(query))), len(results), time.Since(startedAt).Milliseconds())
			return fromSchemaSearchResults(results), nil
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			logx.Printf("运行日志", "runtime log", "rag fallback search failed: %v", err)
		} else {
			logx.Printf("运行日志", "runtime log", "rag fallback primary_empty query_chars=%d duration_ms=%d", len([]rune(strings.TrimSpace(query))), time.Since(startedAt).Milliseconds())
		}
	}
	if a.fallback == nil {
		return nil, nil
	}
	results := fromSchemaSearchResults(a.fallback.Search(query))
	logx.Printf("运行日志", "runtime log", "rag fallback local_results query_chars=%d results=%d duration_ms=%d", len([]rune(strings.TrimSpace(query))), len(results), time.Since(startedAt).Milliseconds())
	return results, nil
}

type productSearchAdapter struct {
	service *productsearch.Service
}

func (a productSearchAdapter) Search(ctx context.Context, message string) ([]ProductCard, error) {
	products, err := a.service.Search(ctx, message, productsearch.Options{MaxPerPlatform: 10, MaxTotal: 10})
	if err != nil {
		return nil, err
	}
	return fromPlatformProducts(products), nil
}

func fromPlatformProducts(products []platform.ProductCard) []ProductCard {
	out := make([]ProductCard, 0, len(products))
	for _, product := range products {
		out = append(out, ProductCard{
			ID:         product.ID,
			Name:       product.Name,
			Company:    product.Company,
			Price:      product.Price,
			PriceLabel: product.PriceLabel,
			Tags:       append([]string(nil), product.Tags...),
			URL:        product.URL,
			Platform:   product.Platform,
			Brief:      product.Brief,
		})
	}
	return out
}

func toSchemaSearchResults(results []SearchResultItem) []schema.SearchResultItem {
	out := make([]schema.SearchResultItem, 0, len(results))
	for _, result := range results {
		out = append(out, schema.SearchResultItem{
			Title:       result.Title,
			URL:         result.URL,
			Site:        result.Site,
			Snippet:     result.Snippet,
			ProductURL:  result.ProductURL,
			ProductName: result.ProductName,
			Tags:        append([]string(nil), result.Tags...),
		})
	}
	return out
}

func fromSchemaSearchResults(results []schema.SearchResultItem) []SearchResultItem {
	out := make([]SearchResultItem, 0, len(results))
	for _, result := range results {
		out = append(out, SearchResultItem{
			Title:       result.Title,
			URL:         result.URL,
			Site:        result.Site,
			Snippet:     result.Snippet,
			ProductURL:  result.ProductURL,
			ProductName: result.ProductName,
			Tags:        append([]string(nil), result.Tags...),
		})
	}
	return out
}

func logShort(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

type detailAdapter struct {
	service *productdetail.Service
}

func (a detailAdapter) Run(ctx context.Context, req DetailRequest) <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		if a.service == nil {
			return
		}
		events := a.service.Run(ctx, productdetail.Request{
			Action:       req.Action,
			ProductURL:   req.ProductURL,
			ProductName:  req.ProductName,
			UserQuestion: req.UserQuestion,
			RequestID:    req.RequestID,
		})
		for event := range events {
			out <- Event{Name: event.Name, Data: sanitizeDeltaData(event.Name, event.Data)}
		}
	}()
	return out
}

func sanitizeDeltaData(name string, data any) any {
	if name != EventDelta {
		return data
	}
	switch payload := data.(type) {
	case schema.SSEDeltaPayload:
		payload.Text = compliance.Sanitize(payload.Text)
		return payload
	case map[string]string:
		copied := make(map[string]string, len(payload))
		for key, value := range payload {
			if key == "text" {
				value = compliance.Sanitize(value)
			}
			copied[key] = value
		}
		return copied
	case map[string]any:
		copied := make(map[string]any, len(payload))
		for key, value := range payload {
			if key == "text" {
				if text, ok := value.(string); ok {
					value = compliance.Sanitize(text)
				}
			}
			copied[key] = value
		}
		return copied
	default:
		return data
	}
}
