package productdetail

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/htmlcleaner"
	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/prompt"
	"smartinsure-eino-backend/internal/schema"
)

const (
	defaultMinCNChars      = 50
	defaultMaxExtractRetry = 3
	asyncPersistTimeout    = 10 * time.Second
)

type Request struct {
	Action       string
	ProductURL   string
	ProductName  string
	Message      string
	UserQuestion string
	RequestID    string
}

type Event struct {
	Name string
	Data any
}

type PreparedDetailSource struct {
	SourceURL           string
	SourceType          string
	SourceFormat        string
	RawPayload          string
	CleanedText         string
	CNCharCount         int
	ResolvedProductName string
	FetchedAt           time.Time
}

type Service struct {
	model           llm.ChatModel
	fetcher         Fetcher
	cache           *ProductDetailCache
	repository      DetailRepository
	hotCache        DetailHotCache
	ragIngestor     ProductDetailRAGIngestor
	keyer           ProductKeyer
	minCNChars      int
	maxExtractRetry int
	redisTTL        time.Duration
	aliasRedisTTL   time.Duration
	dbTTL           time.Duration
	lockTTL         time.Duration
	promptVersion   string
	now             func() time.Time
}

type detailPersistLockContextKey struct{}

type Option func(*Service)

func WithModel(model llm.ChatModel) Option {
	return func(s *Service) {
		s.model = model
	}
}

func WithFetcher(fetcher Fetcher) Option {
	return func(s *Service) {
		s.fetcher = fetcher
	}
}

func WithCache(cache *ProductDetailCache) Option {
	return func(s *Service) {
		s.cache = cache
	}
}

func WithRepository(repository DetailRepository) Option {
	return func(s *Service) {
		s.repository = repository
	}
}

func WithHotCache(cache DetailHotCache) Option {
	return func(s *Service) {
		s.hotCache = cache
	}
}

func WithRAGIngestor(ingestor ProductDetailRAGIngestor) Option {
	return func(s *Service) {
		s.ragIngestor = ingestor
	}
}

func WithProductKeyer(keyer ProductKeyer) Option {
	return func(s *Service) {
		s.keyer = keyer
	}
}

func WithSharedCacheTTL(redisTTL, aliasRedisTTL, dbTTL, lockTTL time.Duration) Option {
	return func(s *Service) {
		s.redisTTL = redisTTL
		s.aliasRedisTTL = aliasRedisTTL
		s.dbTTL = dbTTL
		s.lockTTL = lockTTL
	}
}

func WithPromptVersion(version string) Option {
	return func(s *Service) {
		s.promptVersion = strings.TrimSpace(version)
	}
}

func WithMinCNChars(min int) Option {
	return func(s *Service) {
		s.minCNChars = min
	}
}

func NewService(opts ...Option) *Service {
	s := &Service{
		fetcher:         NewHTTPFetcher(15 * time.Second),
		cache:           DefaultCache,
		keyer:           NewProductKeyer(),
		minCNChars:      defaultMinCNChars,
		maxExtractRetry: defaultMaxExtractRetry,
		redisTTL:        7 * 24 * time.Hour,
		aliasRedisTTL:   30 * 24 * time.Hour,
		dbTTL:           30 * 24 * time.Hour,
		lockTTL:         60 * time.Second,
		promptVersion:   "detail_extract_v1",
		now:             func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.cache == nil {
		s.cache = NewCache(DefaultCacheTTL)
	}
	if s.fetcher == nil {
		s.fetcher = NewHTTPFetcher(15 * time.Second)
	}
	if s.minCNChars <= 0 {
		s.minCNChars = defaultMinCNChars
	}
	if s.maxExtractRetry <= 0 {
		s.maxExtractRetry = defaultMaxExtractRetry
	}
	if s.keyer == (ProductKeyer{}) {
		s.keyer = NewProductKeyer()
	}
	if s.redisTTL <= 0 {
		s.redisTTL = 7 * 24 * time.Hour
	}
	if s.aliasRedisTTL <= 0 {
		s.aliasRedisTTL = 30 * 24 * time.Hour
	}
	if s.dbTTL <= 0 {
		s.dbTTL = 30 * 24 * time.Hour
	}
	if s.lockTTL <= 0 {
		s.lockTTL = 60 * time.Second
	}
	if s.promptVersion == "" {
		s.promptVersion = "detail_extract_v1"
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	return s
}

type Skill struct {
	service *Service
}

func NewSkill(service *Service) *Skill {
	if service == nil {
		service = NewService()
	}
	return &Skill{service: service}
}

func (s *Skill) Run(ctx context.Context, req Request) <-chan Event {
	return s.service.Run(ctx, req)
}

// Run emits status/detail_items/delta/error events. The caller owns final
// disclaimer and done events so this package can be embedded in chatflow.
func (s *Service) Run(ctx context.Context, req Request) <-chan Event {
	events := make(chan Event)
	go func() {
		defer close(events)
		s.run(ctx, req, events)
	}()
	return events
}

func (s *Service) run(ctx context.Context, req Request, events chan<- Event) {
	if s == nil {
		s = NewService()
	}
	startedAt := time.Now()
	productURL := strings.TrimSpace(req.ProductURL)
	if productURL == "" {
		logx.Printf("运行日志", "runtime log", "product_detail request invalid request_id=%s action=%s reason=empty_product_url", req.RequestID, req.Action)
		emitDelta(ctx, events, "请先提供产品页面链接，才能读取保障详情。")
		return
	}
	identity, hasIdentity := s.productIdentity(productURL)
	if hasIdentity {
		logx.Printf("运行日志", "runtime log", "product_detail request start request_id=%s action=%s %s has_question=%t", req.RequestID, req.Action, logIdentity(identity), strings.TrimSpace(req.UserQuestion) != "" || strings.TrimSpace(req.Message) != "")
	} else {
		logx.Printf("运行日志", "runtime log", "product_detail request start request_id=%s action=%s identity=unavailable has_question=%t", req.RequestID, req.Action, strings.TrimSpace(req.UserQuestion) != "" || strings.TrimSpace(req.Message) != "")
	}
	defer func() {
		logx.Printf("运行日志", "runtime log", "product_detail request end request_id=%s action=%s duration_ms=%d", req.RequestID, req.Action, time.Since(startedAt).Milliseconds())
	}()

	question := strings.TrimSpace(req.UserQuestion)
	if question == "" {
		question = strings.TrimSpace(req.Message)
	}

	if req.Action == "product_followup" {
		_, detail, ok := s.loadSharedDetail(ctx, productURL)
		if !ok {
			logx.Printf("运行日志", "runtime log", "product_detail followup cache_miss request_id=%s %s", req.RequestID, logIdentity(identity))
			emitDelta(ctx, events, "暂未找到该产品的详情缓存，请先查看产品详情后再追问。")
			return
		}
		logx.Printf("运行日志", "runtime log", "product_detail followup cache_hit request_id=%s %s duties=%d", req.RequestID, logIdentity(identity), len(detail.Duties))
		emitStatus(ctx, events, "answering", "正在基于已缓存的保障信息回答追问...")
		s.emitAnswer(ctx, events, detail, question)
		return
	}

	identity, detail, ok := s.loadSharedDetail(ctx, productURL)
	if ok {
		logx.Printf("运行日志", "runtime log", "product_detail shared_cache_hit request_id=%s %s duties=%d cn_chars=%d match_rate=%.3f", req.RequestID, logIdentity(identity), len(detail.Duties), detail.CNCharCount, detail.MatchRate)
		emit(ctx, events, Event{Name: schema.SSEEventDetailItems, Data: DetailItemsPayload(detail)})
		emitStatus(ctx, events, "answering", "正在生成保障解读...")
		s.emitAnswer(ctx, events, detail, question)
		return
	}
	emitStatus(ctx, events, "reading", "正在读取产品页面...")
	preparedSource, ok := s.prepareDetailSource(ctx, productURL, req.ProductName)
	if !ok {
		logx.Printf("运行日志", "runtime log", "product_detail source_read_failed request_id=%s %s", req.RequestID, logIdentity(identity))
		emitDelta(ctx, events, fmt.Sprintf("暂时无法访问该产品页面，建议直接查看：%s", productURL))
		return
	}
	if preparedSource.CNCharCount < s.minCNChars {
		logx.Printf("运行日志", "runtime log", "product_detail source_too_short request_id=%s %s cn_chars=%d min_cn_chars=%d source_type=%s", req.RequestID, logIdentity(identity), preparedSource.CNCharCount, s.minCNChars, preparedSource.SourceType)
		emitDelta(ctx, events, fmt.Sprintf("该页面内容较少，无法提取保障详情，建议直接查看：%s", productURL))
		return
	}

	emitStatus(ctx, events, "analyzing", "正在分析保障项目...")
	detail, extracted := s.extractDetail(ctx, preparedSource.CleanedText, preparedSource.CNCharCount, productURL, preparedSource.ResolvedProductName)
	if !extracted {
		logx.Printf("运行日志", "runtime log", "product_detail extract_failed request_id=%s %s cn_chars=%d source_type=%s", req.RequestID, logIdentity(identity), preparedSource.CNCharCount, preparedSource.SourceType)
		emitDelta(ctx, events, fmt.Sprintf("暂时无法自动解析此产品的保障详情，建议直接查看：%s", productURL))
		return
	}
	logx.Printf("运行日志", "runtime log", "product_detail extract_success request_id=%s %s product_name=%q duties=%d cn_chars=%d match_rate=%.3f", req.RequestID, logIdentity(identity), detail.ProductName, len(detail.Duties), detail.CNCharCount, detail.MatchRate)

	emit(ctx, events, Event{Name: schema.SSEEventDetailItems, Data: DetailItemsPayload(detail)})
	s.persistSharedDetail(ctx, identity, productURL, preparedSource, detail)

	emitStatus(ctx, events, "answering", "正在生成通俗解读...")
	s.emitAnswer(ctx, events, detail, question)
}

func (s *Service) loadSharedDetail(ctx context.Context, productURL string) (ProductIdentity, schema.ProductDetail, bool) {
	identity, hasIdentity := s.productIdentity(productURL)
	if detail, ok := s.localDetail(identity, productURL); ok {
		logx.Printf("运行日志", "runtime log", "product_detail cache_hit source=local %s duties=%d", logIdentity(identity), len(detail.Duties))
		return identity, detail, true
	}
	if !hasIdentity {
		logx.Printf("运行日志", "runtime log", "product_detail identity_failed source=shared_cache_lookup")
		return identity, schema.ProductDetail{}, false
	}

	if detail, ok := s.redisDetail(ctx, identity); ok {
		logx.Printf("运行日志", "runtime log", "product_detail cache_hit source=redis %s duties=%d", logIdentity(identity), len(detail.Duties))
		s.setLocalDetail(identity, productURL, detail)
		return identity, detail, true
	}
	if detail, ok := s.mysqlDetail(ctx, identity); ok {
		logx.Printf("运行日志", "runtime log", "product_detail cache_hit source=mysql %s duties=%d", logIdentity(identity), len(detail.Duties))
		s.setLocalDetail(identity, productURL, detail)
		return identity, detail, true
	}
	logx.Printf("运行日志", "runtime log", "product_detail cache_miss %s", logIdentity(identity))
	return identity, schema.ProductDetail{}, false
}

func (s *Service) productIdentity(productURL string) (ProductIdentity, bool) {
	if s == nil {
		return ProductIdentity{}, false
	}
	keyer := s.keyer
	if keyer == (ProductKeyer{}) {
		keyer = NewProductKeyer()
	}
	identity, err := keyer.Key(productURL)
	if err != nil {
		return ProductIdentity{}, false
	}
	return identity, true
}

func (s *Service) localDetail(identity ProductIdentity, productURL string) (schema.ProductDetail, bool) {
	for _, key := range localDetailKeys(identity, productURL) {
		if detail, ok := s.cache.Get(key); ok {
			return detail, true
		}
	}
	return schema.ProductDetail{}, false
}

func (s *Service) redisDetail(ctx context.Context, identity ProductIdentity) (schema.ProductDetail, bool) {
	if s == nil || s.hotCache == nil || identity.ProductKey == "" {
		return schema.ProductDetail{}, false
	}
	now := s.now()
	if identity.URLHash != "" {
		productKey, ok, err := s.hotCache.GetAlias(ctx, identity.URLHash)
		if err != nil {
			logx.Printf("运行日志", "runtime log", "product detail redis alias read failed: %v", err)
		} else if ok {
			logx.Printf("运行日志", "runtime log", "product_detail redis_alias_hit url_hash=%s product_key=%s", logShort(identity.URLHash), logShort(productKey))
			if detail, ok := s.redisDetailByProductKey(ctx, productKey, now); ok {
				return detail, true
			}
		}
	}
	return s.redisDetailByProductKey(ctx, identity.ProductKey, now)
}

func (s *Service) redisDetailByProductKey(ctx context.Context, productKey string, now time.Time) (schema.ProductDetail, bool) {
	record, ok, err := s.hotCache.Get(ctx, productKey)
	if err != nil {
		logx.Printf("运行日志", "runtime log", "product detail redis read failed: %v", err)
		return schema.ProductDetail{}, false
	}
	if !ok || !record.Usable(now, s.promptVersion) {
		return schema.ProductDetail{}, false
	}
	return record.Detail, true
}

func (s *Service) mysqlDetail(ctx context.Context, identity ProductIdentity) (schema.ProductDetail, bool) {
	if s == nil || s.repository == nil || identity.ProductKey == "" {
		return schema.ProductDetail{}, false
	}
	record, err := s.storedDetail(ctx, identity)
	if errors.Is(err, ErrProductDetailNotFound) {
		return schema.ProductDetail{}, false
	}
	if err != nil {
		logx.Printf("运行日志", "runtime log", "product detail mysql read failed: %v", err)
		return schema.ProductDetail{}, false
	}
	if record == nil || !record.Usable(s.now(), s.promptVersion) {
		if record != nil {
			logx.Printf("运行日志", "runtime log", "product_detail mysql_record_not_usable %s status=%s prompt_version=%s", logIdentity(identity), record.Status, record.PromptVersion)
		}
		return schema.ProductDetail{}, false
	}
	if err := s.repository.TouchHit(ctx, record.ProductKey); err != nil {
		logx.Printf("运行日志", "runtime log", "product detail mysql touch failed: %v", err)
	}
	if s.hotCache != nil {
		detailRecord := record.DetailRecord()
		if err := s.hotCache.Set(ctx, detailRecord, s.redisTTL); err != nil {
			logx.Printf("运行日志", "runtime log", "product detail redis backfill failed: %v", err)
		}
		if identity.URLHash != "" {
			if err := s.hotCache.SetAlias(ctx, identity.URLHash, record.ProductKey, s.aliasRedisTTL); err != nil {
				logx.Printf("运行日志", "runtime log", "product detail redis alias backfill failed: %v", err)
			}
		}
	}
	return record.Detail, true
}

func (s *Service) storedDetail(ctx context.Context, identity ProductIdentity) (*StoredProductDetail, error) {
	if s == nil || s.repository == nil || identity.ProductKey == "" {
		return nil, ErrProductDetailNotFound
	}
	record, err := s.repository.GetByURL(ctx, identity.NormalizedURL)
	if errors.Is(err, ErrProductDetailNotFound) {
		record, err = s.repository.GetByProductKey(ctx, identity.ProductKey)
	}
	return record, err
}

func (s *Service) acquireDetailLock(ctx context.Context, identity ProductIdentity) (DetailLock, bool) {
	if s == nil || s.hotCache == nil || identity.ProductKey == "" {
		return nil, false
	}
	lock, ok, err := s.hotCache.TryLock(ctx, identity.ProductKey, s.lockTTL)
	if err != nil {
		logx.Printf("运行日志", "runtime log", "product detail redis lock failed: %v", err)
		return nil, false
	}
	if !ok {
		logx.Printf("运行日志", "runtime log", "product_detail redis_lock_busy %s", logIdentity(identity))
		return nil, true
	}
	return lock, false
}

func contextWithDetailPersistLock(ctx context.Context) context.Context {
	return context.WithValue(ctx, detailPersistLockContextKey{}, true)
}

func detailPersistLockHeld(ctx context.Context) bool {
	held, _ := ctx.Value(detailPersistLockContextKey{}).(bool)
	return held
}

func (s *Service) persistSharedDetail(_ context.Context, identity ProductIdentity, productURL string, source PreparedDetailSource, detail schema.ProductDetail) {
	s.setLocalDetail(identity, productURL, detail)
	if !s.shouldPersistSharedDetail(detail) {
		logx.Printf("运行日志", "runtime log", "product_detail persist_skipped reason=quality_gate %s duties=%d cn_chars=%d", logIdentity(identity), len(detail.Duties), detail.CNCharCount)
		return
	}
	if identity.ProductKey == "" {
		identity, _ = s.productIdentity(productURL)
	}
	if identity.ProductKey == "" || s.repository == nil {
		logx.Printf("运行日志", "runtime log", "product_detail persist_skipped reason=repository_unavailable %s", logIdentity(identity))
		return
	}

	logx.Printf("运行日志", "runtime log", "product_detail persist_async_start %s duties=%d cn_chars=%d source_type=%s", logIdentity(identity), len(detail.Duties), detail.CNCharCount, source.SourceType)
	go func() {
		persistCtx, cancel := context.WithTimeout(context.Background(), asyncPersistTimeout)
		defer cancel()
		lock, lockBusy := s.acquireDetailLock(persistCtx, identity)
		if lockBusy {
			logx.Printf("运行日志", "runtime log", "product_detail persist_skipped reason=lock_busy %s", logIdentity(identity))
			return
		}
		if lock != nil {
			logx.Printf("运行日志", "runtime log", "product_detail persist_lock_acquired %s", logIdentity(identity))
			defer func() {
				if err := lock.Release(context.Background()); err != nil {
					logx.Printf("运行日志", "runtime log", "product detail persist lock release failed: %v", err)
				} else {
					logx.Printf("运行日志", "runtime log", "product_detail persist_lock_released %s", logIdentity(identity))
				}
			}()
		}
		s.persistSharedDetailStores(contextWithDetailPersistLock(persistCtx), identity, source, detail)
	}()
}

func (s *Service) persistSharedDetailStores(ctx context.Context, identity ProductIdentity, source PreparedDetailSource, detail schema.ProductDetail) {
	if s == nil || s.repository == nil || identity.ProductKey == "" {
		return
	}
	now := s.now()
	sourceHash := SHA256Hex(source.CleanedText)
	existingRecord, existingReason := s.existingDetailBeforePersist(ctx, identity)
	expiresAt := now.Add(s.dbTTL)
	rawPayload := strings.TrimSpace(source.RawPayload)
	var rawPayloadPtr *string
	if rawPayload != "" {
		rawPayloadPtr = &source.RawPayload
	}
	sourceURL := strings.TrimSpace(source.SourceURL)
	if sourceURL == "" {
		sourceURL = identity.NormalizedURL
	}
	fetchedAt := source.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = now
	}
	input := UpsertProductDetailInput{
		ProductKey:        identity.ProductKey,
		Platform:          identity.Platform,
		CanonicalURL:      identity.NormalizedURL,
		NormalizedURLHash: identity.URLHash,
		Detail:            detail,
		SourceHash:        sourceHash,
		PromptVersion:     s.promptVersion,
		Status:            DetailStatusActive,
		ExpiresAt:         &expiresAt,
		Source: &UpsertProductDetailSourceInput{
			ProductKey:        identity.ProductKey,
			NormalizedURLHash: identity.URLHash,
			SourceURL:         sourceURL,
			SourceType:        source.SourceType,
			SourceFormat:      source.SourceFormat,
			RawPayload:        rawPayloadPtr,
			CleanedText:       source.CleanedText,
			ContentHash:       sourceHash,
			CNCharCount:       source.CNCharCount,
			FetchedAt:         fetchedAt,
		},
	}
	if skipPersist, reason := s.shouldSkipSharedDetailPersist(existingRecord, existingReason, sourceHash, now); skipPersist {
		logx.Printf("运行日志", "runtime log", "product_detail persist_skipped reason=%s %s source_hash=%s", reason, logIdentity(identity), logShort(sourceHash))
		s.ingestSharedDetailRAG(ctx, identity, input, source, sourceHash, expiresAt, now, existingRecord)
		return
	}
	if err := s.repository.Upsert(ctx, input); err != nil {
		logx.Printf("运行日志", "runtime log", "product detail mysql upsert failed: %v", err)
		return
	}
	logx.Printf("运行日志", "runtime log", "product_detail mysql_upsert_success %s source_hash=%s duties=%d cn_chars=%d", logIdentity(identity), logShort(sourceHash), len(detail.Duties), source.CNCharCount)
	s.ingestSharedDetailRAG(ctx, identity, input, source, sourceHash, expiresAt, now, existingRecord)
	if s.hotCache == nil {
		logx.Printf("运行日志", "runtime log", "product_detail redis_set_skipped reason=hot_cache_unavailable %s", logIdentity(identity))
		return
	}
	record := DetailRecord{
		ProductKey:    identity.ProductKey,
		Platform:      identity.Platform,
		CanonicalURL:  identity.NormalizedURL,
		Detail:        detail,
		SourceHash:    sourceHash,
		PromptVersion: s.promptVersion,
		Status:        DetailStatusActive,
		ExpiresAt:     expiresAt,
		UpdatedAt:     now,
	}
	if err := s.hotCache.Set(ctx, record, s.redisTTL); err != nil {
		logx.Printf("运行日志", "runtime log", "product detail redis set failed: %v", err)
	} else {
		logx.Printf("运行日志", "runtime log", "product_detail redis_set_success %s ttl_seconds=%d", logIdentity(identity), int(s.redisTTL.Seconds()))
	}
	if identity.URLHash != "" {
		if err := s.hotCache.SetAlias(ctx, identity.URLHash, identity.ProductKey, s.aliasRedisTTL); err != nil {
			logx.Printf("运行日志", "runtime log", "product detail redis alias set failed: %v", err)
		} else {
			logx.Printf("运行日志", "runtime log", "product_detail redis_alias_set_success url_hash=%s product_key=%s ttl_seconds=%d", logShort(identity.URLHash), logShort(identity.ProductKey), int(s.aliasRedisTTL.Seconds()))
		}
	}
}

func (s *Service) existingDetailBeforePersist(ctx context.Context, identity ProductIdentity) (*StoredProductDetail, string) {
	record, err := s.storedDetail(ctx, identity)
	if errors.Is(err, ErrProductDetailNotFound) {
		return nil, "not_found"
	}
	if err != nil {
		logx.Printf("运行日志", "runtime log", "product_detail existing_record_probe_failed %s err=%v", logIdentity(identity), err)
		return nil, "probe_failed"
	}
	if record == nil {
		return nil, "not_found"
	}
	return record, "found"
}

func (s *Service) shouldSkipSharedDetailPersist(record *StoredProductDetail, missingReason string, sourceHash string, now time.Time) (bool, string) {
	if record == nil {
		return false, missingReason
	}
	status := NormalizeDetailStatus(record.Status)
	switch status {
	case DetailStatusDisabled:
		return true, "status_disabled"
	case DetailStatusProcessing:
		if !record.Expired(now) {
			return true, "status_processing"
		}
		return false, "status_processing_expired"
	case DetailStatusActive:
		if record.Expired(now) {
			return false, "active_expired"
		}
		if !detailPromptVersionCompatible(record.PromptVersion, s.promptVersion) {
			return false, "prompt_version_changed"
		}
		if strings.TrimSpace(record.SourceHash) != "" && strings.TrimSpace(record.SourceHash) == strings.TrimSpace(sourceHash) {
			return true, "active_same_source_hash"
		}
		return false, "active_source_changed"
	default:
		return false, "status_" + status
	}
}

func detailPromptVersionCompatible(recordVersion, currentVersion string) bool {
	recordVersion = strings.TrimSpace(recordVersion)
	currentVersion = strings.TrimSpace(currentVersion)
	return recordVersion == "" || currentVersion == "" || recordVersion == currentVersion
}

func (s *Service) ingestSharedDetailRAG(ctx context.Context, identity ProductIdentity, input UpsertProductDetailInput, source PreparedDetailSource, sourceHash string, expiresAt time.Time, updatedAt time.Time, existingRecord *StoredProductDetail) {
	if s == nil || s.ragIngestor == nil {
		logx.Printf("运行日志", "runtime log", "product_detail rag_enqueue_skipped reason=ingestor_unavailable %s", logIdentity(identity))
		return
	}
	if ok, reason := s.validateSharedDetailRAGIngest(input, source, sourceHash, updatedAt, existingRecord); !ok {
		logx.Printf("运行日志", "runtime log", "product_detail rag_enqueue_skipped reason=%s %s source_hash=%s", reason, logIdentity(identity), logShort(sourceHash))
		return
	}
	releaseLock := s.acquireDetailLockBeforeRAGQueue(ctx, identity, sourceHash)
	if releaseLock == nil && s.hotCache != nil && identity.ProductKey != "" && !detailPersistLockHeld(ctx) {
		return
	}
	if releaseLock != nil {
		defer releaseLock()
	}
	s.updateSharedDetailRAGIngestState(ctx, input.ProductKey, RAGIngestStatusIngesting, sourceHash, "", updatedAt)
	sourceURL := strings.TrimSpace(source.SourceURL)
	if sourceURL == "" {
		sourceURL = identity.NormalizedURL
	}
	fetchedAt := source.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = updatedAt
	}
	record := StoredProductDetailWithSource{
		Detail: StoredProductDetail{
			ProductKey:    input.ProductKey,
			Platform:      input.Platform,
			ProductName:   input.Detail.ProductName,
			CanonicalURL:  input.CanonicalURL,
			URLHash:       input.NormalizedURLHash,
			Detail:        input.Detail,
			SourceHash:    sourceHash,
			PromptVersion: input.PromptVersion,
			ModelName:     input.ModelName,
			CNCharCount:   input.Detail.CNCharCount,
			MatchRate:     input.Detail.MatchRate,
			Status:        input.Status,
			ExpiresAt:     &expiresAt,
			UpdatedAt:     updatedAt,
		},
		Source: &ProductDetailSource{
			ProductKey:        input.ProductKey,
			NormalizedURLHash: input.NormalizedURLHash,
			SourceURL:         sourceURL,
			SourceType:        source.SourceType,
			SourceFormat:      source.SourceFormat,
			CleanedText:       source.CleanedText,
			ContentHash:       sourceHash,
			CNCharCount:       source.CNCharCount,
			FetchedAt:         fetchedAt,
			UpdatedAt:         updatedAt,
		},
	}
	if err := s.ragIngestor.IngestProductDetail(ctx, record); err != nil {
		s.updateSharedDetailRAGIngestState(ctx, input.ProductKey, RAGIngestStatusFailed, sourceHash, err.Error(), updatedAt)
		logx.Printf("运行日志", "runtime log", "product detail rag ingest enqueue failed: %v", err)
	} else {
		s.updateSharedDetailRAGIngestState(ctx, input.ProductKey, RAGIngestStatusEnqueued, sourceHash, "", updatedAt)
		logx.Printf("运行日志", "runtime log", "product_detail rag_enqueue_success %s source_hash=%s duties=%d match_rate=%.3f", logIdentity(identity), logShort(sourceHash), len(input.Detail.Duties), input.Detail.MatchRate)
	}
}

func (s *Service) acquireDetailLockBeforeRAGQueue(ctx context.Context, identity ProductIdentity, sourceHash string) func() {
	if s == nil || detailPersistLockHeld(ctx) || s.hotCache == nil || identity.ProductKey == "" {
		return func() {}
	}
	lock, lockBusy := s.acquireDetailLock(ctx, identity)
	if lockBusy {
		logx.Printf("运行日志", "runtime log", "product_detail rag_enqueue_skipped reason=lock_busy_before_channel %s source_hash=%s", logIdentity(identity), logShort(sourceHash))
		return nil
	}
	if lock == nil {
		return func() {}
	}
	logx.Printf("运行日志", "runtime log", "product_detail rag_queue_lock_acquired %s source_hash=%s", logIdentity(identity), logShort(sourceHash))
	return func() {
		if err := lock.Release(context.Background()); err != nil {
			logx.Printf("运行日志", "runtime log", "product detail rag queue lock release failed: %v", err)
		} else {
			logx.Printf("运行日志", "runtime log", "product_detail rag_queue_lock_released %s source_hash=%s", logIdentity(identity), logShort(sourceHash))
		}
	}
}

func (s *Service) validateSharedDetailRAGIngest(input UpsertProductDetailInput, source PreparedDetailSource, sourceHash string, now time.Time, existingRecord *StoredProductDetail) (bool, string) {
	if NormalizeDetailStatus(input.Status) != DetailStatusActive {
		return false, "input_status_" + NormalizeDetailStatus(input.Status)
	}
	if len(input.Detail.Duties) == 0 {
		return false, "duties_empty"
	}
	if input.Detail.CNCharCount < s.minCNChars {
		return false, "cn_chars_below_min"
	}
	if strings.TrimSpace(sourceHash) == "" {
		return false, "source_hash_empty"
	}
	if strings.TrimSpace(source.CleanedText) == "" {
		return false, "cleaned_text_empty"
	}
	if existingRecord == nil {
		return true, "not_found"
	}
	status := NormalizeDetailStatus(existingRecord.Status)
	switch status {
	case DetailStatusDisabled:
		return false, "status_disabled"
	case DetailStatusProcessing:
		if !existingRecord.Expired(now) {
			return false, "status_processing"
		}
	case DetailStatusActive:
		if !existingRecord.Expired(now) && detailPromptVersionCompatible(existingRecord.PromptVersion, input.PromptVersion) && existingRecord.RAGIngestRecordedForSource(sourceHash) {
			return false, "active_same_source_hash"
		}
	}
	return true, "eligible"
}

func (s *Service) updateSharedDetailRAGIngestState(ctx context.Context, productKey, status, sourceHash, message string, updatedAt time.Time) {
	if s == nil || s.repository == nil || strings.TrimSpace(productKey) == "" {
		return
	}
	if updatedAt.IsZero() {
		updatedAt = s.now()
	}
	if err := s.repository.UpdateRAGIngestState(ctx, UpdateRAGIngestStateInput{
		ProductKey: productKey,
		Status:     status,
		SourceHash: sourceHash,
		Error:      message,
		UpdatedAt:  updatedAt,
	}); err != nil {
		logx.Printf("运行日志", "runtime log", "product_detail rag_state_update_failed product_key=%s status=%s source_hash=%s err=%v", logShort(productKey), status, logShort(sourceHash), err)
	}
}

func (s *Service) shouldPersistSharedDetail(detail schema.ProductDetail) bool {
	return len(detail.Duties) > 0 && detail.CNCharCount >= s.minCNChars
}

func (s *Service) setLocalDetail(identity ProductIdentity, productURL string, detail schema.ProductDetail) {
	for _, key := range localDetailKeys(identity, productURL) {
		s.cache.Set(key, detail)
	}
}

func localDetailKeys(identity ProductIdentity, productURL string) []string {
	candidates := []string{
		identity.ProductKey,
		identity.NormalizedURL,
		strings.TrimSpace(productURL),
	}
	seen := map[string]bool{}
	keys := make([]string, 0, len(candidates))
	for _, key := range candidates {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys
}

func (s *Service) prepareDetailSource(ctx context.Context, productURL, productName string) (PreparedDetailSource, bool) {
	if source, ok := s.fetchPlatformDetailSource(ctx, productURL, productName); ok {
		logx.Printf("运行日志", "runtime log", "product_detail source_read_success source_type=%s source_format=%s cn_chars=%d url_hash=%s", source.SourceType, source.SourceFormat, source.CNCharCount, logURLHash(source.SourceURL))
		return source, true
	}

	fetchStarted := time.Now()
	rawHTML, err := s.fetcher.Fetch(ctx, productURL)
	if err != nil || strings.TrimSpace(rawHTML) == "" {
		if err != nil {
			logx.Printf("运行日志", "runtime log", "product_detail source_fetch_failed source_type=web_page host=%s duration_ms=%d err=%v", logURLHost(productURL), time.Since(fetchStarted).Milliseconds(), err)
		} else {
			logx.Printf("运行日志", "runtime log", "product_detail source_fetch_failed source_type=web_page host=%s duration_ms=%d reason=empty_body", logURLHost(productURL), time.Since(fetchStarted).Milliseconds())
		}
		return PreparedDetailSource{ResolvedProductName: strings.TrimSpace(productName)}, false
	}

	cleanedText, cnCount := htmlcleaner.CleanHTML(rawHTML)
	logx.Printf("运行日志", "runtime log", "product_detail source_read_success source_type=web_page source_format=html host=%s raw_bytes=%d cn_chars=%d duration_ms=%d url_hash=%s", logURLHost(productURL), len(rawHTML), cnCount, time.Since(fetchStarted).Milliseconds(), logURLHash(productURL))
	return PreparedDetailSource{
		SourceURL:           normalizedSourceURL(productURL),
		SourceType:          "web_page",
		SourceFormat:        "html",
		RawPayload:          rawHTML,
		CleanedText:         cleanedText,
		CNCharCount:         cnCount,
		ResolvedProductName: strings.TrimSpace(productName),
		FetchedAt:           s.now(),
	}, true
}

func normalizedSourceURL(productURL string) string {
	normalized, err := NormalizeProductURL(productURL)
	if err != nil || strings.TrimSpace(normalized) == "" {
		return strings.TrimSpace(productURL)
	}
	return normalized
}

func (s *Service) extractDetail(ctx context.Context, cleanedText string, cnCount int, productURL, productName string) (schema.ProductDetail, bool) {
	if s.model != nil {
		for attempt := 1; attempt <= s.maxExtractRetry; attempt++ {
			attemptStarted := time.Now()
			input := cleanedText
			if attempt == s.maxExtractRetry && len([]rune(input)) > 3000 {
				input = string([]rune(input)[:3000])
			}
			raw, err := s.model.CallText(ctx, BuildExtractMessages(input), 0.2)
			if err != nil {
				logx.Printf("运行日志", "runtime log", "product_detail llm_extract_attempt_failed attempt=%d max_attempts=%d cn_chars=%d input_runes=%d duration_ms=%d err=%v", attempt, s.maxExtractRetry, cnCount, len([]rune(input)), time.Since(attemptStarted).Milliseconds(), err)
				continue
			}
			payload, ok := ParseExtractionJSON(raw)
			if !ok || len(payload.Duties) == 0 {
				logx.Printf("运行日志", "runtime log", "product_detail llm_extract_attempt_rejected attempt=%d max_attempts=%d reason=invalid_payload duties=%d duration_ms=%d", attempt, s.maxExtractRetry, len(payload.Duties), time.Since(attemptStarted).Milliseconds())
				continue
			}
			passed, matchRate, _ := ValidateExtraction(payload.Duties, cleanedText)
			if !passed {
				logx.Printf("运行日志", "runtime log", "product_detail llm_extract_attempt_rejected attempt=%d max_attempts=%d reason=validation_failed duties=%d match_rate=%.3f duration_ms=%d", attempt, s.maxExtractRetry, len(payload.Duties), matchRate, time.Since(attemptStarted).Milliseconds())
				continue
			}
			name := strings.TrimSpace(payload.ProductName)
			if name == "" {
				name = strings.TrimSpace(productName)
			}
			logx.Printf("运行日志", "runtime log", "product_detail llm_extract_attempt_success attempt=%d max_attempts=%d duties=%d match_rate=%.3f duration_ms=%d", attempt, s.maxExtractRetry, len(payload.Duties), matchRate, time.Since(attemptStarted).Milliseconds())
			return schema.ProductDetail{
				ProductName: name,
				ProductURL:  productURL,
				Platform:    InferPlatform(productURL),
				Duties:      payload.Duties,
				CNCharCount: cnCount,
				MatchRate:   matchRate,
			}, true
		}
	}

	detail := HeuristicExtract(cleanedText, productURL, productName, cnCount)
	logx.Printf("运行日志", "runtime log", "product_detail heuristic_extract_done duties=%d cn_chars=%d", len(detail.Duties), cnCount)
	return detail, len(detail.Duties) > 0
}

func BuildExtractMessages(cleanedText string) []llm.Message {
	userPrompt, err := prompt.Render(prompt.DetailExtractTemplate, struct {
		CleanedText string
	}{CleanedText: cleanedText})
	if err != nil {
		userPrompt = "请提取以下保险产品页面文本中的保障项目：\n" + cleanedText
	}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: prompt.DetailExtractSystem},
		{Role: llm.RoleUser, Content: userPrompt},
	}
}

func BuildAnswerMessages(detail schema.ProductDetail, userQuestion string) []llm.Message {
	duties := FormatDuties(detail.Duties)
	var userPrompt string
	var err error
	if strings.TrimSpace(userQuestion) != "" {
		userPrompt, err = prompt.Render(prompt.DetailFollowupTemplate, struct {
			ProductName     string
			DutiesFormatted string
			UserQuestion    string
		}{ProductName: detail.ProductName, DutiesFormatted: duties, UserQuestion: userQuestion})
	} else {
		userPrompt, err = prompt.Render(prompt.DetailExplainTemplate, struct {
			ProductName     string
			DutiesFormatted string
		}{ProductName: detail.ProductName, DutiesFormatted: duties})
	}
	if err != nil {
		userPrompt = fmt.Sprintf("产品名称：%s\n保障项目：\n%s\n用户追问：%s", detail.ProductName, duties, userQuestion)
	}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: prompt.DetailExplainSystem},
		{Role: llm.RoleUser, Content: userPrompt},
	}
}

func FormatDuties(duties []schema.DutyItem) string {
	if len(duties) == 0 {
		return "（无可用保障项）"
	}
	parts := make([]string, 0, len(duties))
	for _, duty := range duties {
		optional := ""
		if duty.IsOptional {
			optional = "【可选】"
		}
		parts = append(parts, fmt.Sprintf("- %s（%s）%s: %s", duty.Name, duty.Coverage, optional, duty.Description))
	}
	return strings.Join(parts, "\n")
}

func logIdentity(identity ProductIdentity) string {
	if identity.ProductKey == "" && identity.Platform == "" && identity.URLHash == "" {
		return "product_key= platform= url_hash="
	}
	return fmt.Sprintf("product_key=%s platform=%s url_hash=%s", logShort(identity.ProductKey), identity.Platform, logShort(identity.URLHash))
}

func logShort(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func logURLHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return ""
	}
	return parsed.Hostname()
}

func logURLHash(rawURL string) string {
	normalized, err := NormalizeProductURL(rawURL)
	if err != nil {
		return ""
	}
	return logShort(ProductURLHash(normalized))
}

func DetailItemsPayload(detail schema.ProductDetail) schema.SSEDetailItemsPayload {
	return schema.SSEDetailItemsPayload{
		ProductName: detail.ProductName,
		Duties:      append([]schema.DutyItem(nil), detail.Duties...),
	}
}

func (s *Service) emitAnswer(ctx context.Context, events chan<- Event, detail schema.ProductDetail, userQuestion string) {
	for chunk := range s.answerStream(ctx, detail, userQuestion) {
		if strings.TrimSpace(chunk) != "" {
			emitDelta(ctx, events, chunk)
		}
	}
}

func (s *Service) answerStream(ctx context.Context, detail schema.ProductDetail, userQuestion string) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		if s.model != nil {
			stream, err := s.model.StreamText(ctx, BuildAnswerMessages(detail, userQuestion), 0.4)
			if err == nil {
				wrote := false
				for chunk := range stream {
					if chunk.Err != nil {
						out <- fallbackAnswer(detail, userQuestion)
						return
					}
					if chunk.Text == "" {
						continue
					}
					wrote = true
					select {
					case <-ctx.Done():
						return
					case out <- chunk.Text:
					}
				}
				if wrote {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
		case out <- fallbackAnswer(detail, userQuestion):
		}
	}()
	return out
}

func fallbackAnswer(detail schema.ProductDetail, userQuestion string) string {
	if strings.TrimSpace(userQuestion) != "" {
		matches := matchDuties(detail.Duties, userQuestion)
		if len(matches) == 0 {
			return fmt.Sprintf("该产品的公开信息中未涉及此项。当前已识别的保障项包括：%s。具体责任和限制仍以合同条款为准。", dutyNames(detail.Duties))
		}
		return fmt.Sprintf("基于已提取的保障信息，%s。具体赔付条件、免赔额和等待期仍以合同条款为准。", summarizeDuties(matches))
	}
	return fmt.Sprintf("已为你提取 %s 的主要保障：%s。以上为页面可识别信息，具体保障内容以保险合同条款为准。", displayProductName(detail.ProductName), summarizeDuties(detail.Duties))
}

func matchDuties(duties []schema.DutyItem, question string) []schema.DutyItem {
	var matched []schema.DutyItem
	for _, duty := range duties {
		if intersects(question, duty.Name) || intersects(question, duty.Description) {
			matched = append(matched, duty)
		}
	}
	return matched
}

func intersects(a, b string) bool {
	return containsAnyToken(a, b) || containsAnyToken(b, a)
}

func containsAnyToken(haystack, needleSource string) bool {
	for _, seg := range cnSegmentRE.FindAllString(needleSource, -1) {
		runes := []rune(seg)
		for size := 4; size >= 2; size-- {
			if len(runes) < size {
				continue
			}
			for i := 0; i+size <= len(runes); i++ {
				if strings.Contains(haystack, string(runes[i:i+size])) {
					return true
				}
			}
		}
	}
	return false
}

func summarizeDuties(duties []schema.DutyItem) string {
	if len(duties) == 0 {
		return "暂未识别到明确保障项"
	}
	limit := len(duties)
	if limit > 4 {
		limit = 4
	}
	parts := make([]string, 0, limit)
	for _, duty := range duties[:limit] {
		coverage := strings.TrimSpace(duty.Coverage)
		if coverage == "" {
			coverage = "以页面说明为准"
		}
		desc := strings.TrimSpace(duty.Description)
		if desc != "" {
			parts = append(parts, fmt.Sprintf("%s保额/限额为%s，%s", duty.Name, coverage, desc))
		} else {
			parts = append(parts, fmt.Sprintf("%s保额/限额为%s", duty.Name, coverage))
		}
	}
	return strings.Join(parts, "；")
}

func dutyNames(duties []schema.DutyItem) string {
	if len(duties) == 0 {
		return "暂无"
	}
	names := make([]string, 0, len(duties))
	for _, duty := range duties {
		names = append(names, duty.Name)
	}
	return strings.Join(names, "、")
}

func displayProductName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "该产品"
	}
	return name
}

func InferPlatform(productURL string) string {
	parsed, err := url.Parse(productURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	host := strings.ToLower(parsed.Host)
	switch {
	case strings.Contains(host, "xiaoyusan"):
		return "xiaoyusan"
	case strings.Contains(host, "huize"):
		return "huize"
	case strings.Contains(host, "pingan"):
		return "pingan"
	default:
		return host
	}
}

func emitStatus(ctx context.Context, events chan<- Event, stage, message string) {
	emit(ctx, events, Event{Name: schema.SSEEventStatus, Data: schema.SSEStatusPayload{Stage: stage, Message: message}})
}

func emitDelta(ctx context.Context, events chan<- Event, text string) {
	emit(ctx, events, Event{Name: schema.SSEEventDelta, Data: schema.SSEDeltaPayload{Text: text}})
}

func emit(ctx context.Context, events chan<- Event, event Event) {
	select {
	case <-ctx.Done():
	case events <- event:
	}
}
