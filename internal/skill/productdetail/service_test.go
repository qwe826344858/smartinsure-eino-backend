package productdetail

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/schema"
)

type fakeFetcher struct {
	html  string
	err   error
	calls int
}

func (f *fakeFetcher) Fetch(context.Context, string) (string, error) {
	f.calls++
	return f.html, f.err
}

type routeFetcher struct {
	responses map[string]string
	calls     []string
}

func (f *routeFetcher) Fetch(_ context.Context, rawURL string) (string, error) {
	f.calls = append(f.calls, rawURL)
	for key, response := range f.responses {
		if strings.Contains(rawURL, key) {
			return response, nil
		}
	}
	return "", errors.New("unexpected fetch: " + rawURL)
}

type fakeModel struct {
	text          string
	textErr       error
	stream        []string
	streamErr     error
	lastMessages  []llm.Message
	callTextCalls int
}

func (f *fakeModel) CallText(_ context.Context, messages []llm.Message, _ float64) (string, error) {
	f.callTextCalls++
	f.lastMessages = append([]llm.Message(nil), messages...)
	return f.text, f.textErr
}

func (f *fakeModel) CallJSON(context.Context, []llm.Message, float64, any) error {
	return nil
}

func (f *fakeModel) StreamText(context.Context, []llm.Message, float64) (<-chan llm.StreamChunk, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	ch := make(chan llm.StreamChunk, len(f.stream))
	for _, text := range f.stream {
		ch <- llm.StreamChunk{Text: text}
	}
	close(ch)
	return ch, nil
}

type fakeDetailRepository struct {
	byKey          map[string]*StoredProductDetail
	byURL          map[string]*StoredProductDetail
	getErr         error
	upsertErr      error
	upsertStarted  chan struct{}
	upsertContinue chan struct{}
	upsertDone     chan struct{}
	getKeyCalls    int
	getURLCalls    int
	upsertCalls    int
	ragStateCalls  int
	touchCalls     int
	lastUpsert     UpsertProductDetailInput
	lastRAGState   UpdateRAGIngestStateInput
}

func (r *fakeDetailRepository) EnsureSchema(context.Context) error {
	return nil
}

func (r *fakeDetailRepository) GetByProductKey(_ context.Context, productKey string) (*StoredProductDetail, error) {
	r.getKeyCalls++
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.byKey != nil {
		if record := r.byKey[productKey]; record != nil {
			return record, nil
		}
	}
	return nil, ErrProductDetailNotFound
}

func (r *fakeDetailRepository) GetByURL(_ context.Context, productURL string) (*StoredProductDetail, error) {
	r.getURLCalls++
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.byURL != nil {
		if record := r.byURL[productURL]; record != nil {
			return record, nil
		}
		if normalized, err := NormalizeProductURL(productURL); err == nil {
			if record := r.byURL[normalized]; record != nil {
				return record, nil
			}
		}
	}
	return nil, ErrProductDetailNotFound
}

func (r *fakeDetailRepository) Upsert(ctx context.Context, input UpsertProductDetailInput) error {
	signalTestChan(r.upsertStarted)
	if r.upsertContinue != nil {
		select {
		case <-ctx.Done():
			signalTestChan(r.upsertDone)
			return ctx.Err()
		case <-r.upsertContinue:
		}
	}
	r.upsertCalls++
	r.lastUpsert = input
	signalTestChan(r.upsertDone)
	return r.upsertErr
}

func (r *fakeDetailRepository) UpdateRAGIngestState(_ context.Context, input UpdateRAGIngestStateInput) error {
	r.ragStateCalls++
	r.lastRAGState = input
	if r.byKey != nil {
		if record := r.byKey[input.ProductKey]; record != nil {
			status := NormalizeRAGIngestStatus(input.Status)
			record.RAGIngestStatus = status
			record.RAGIngestSourceHash = strings.TrimSpace(input.SourceHash)
			record.RAGIngestError = strings.TrimSpace(input.Error)
			updatedAt := input.UpdatedAt
			record.RAGIngestUpdatedAt = &updatedAt
		}
	}
	return nil
}

func (r *fakeDetailRepository) TouchHit(context.Context, string) error {
	r.touchCalls++
	return nil
}

type fakeDetailHotCache struct {
	byKey         map[string]DetailRecord
	aliases       map[string]string
	getErr        error
	setErr        error
	aliasErr      error
	lockErr       error
	lockGranted   bool
	lockStarted   chan struct{}
	getCalls      int
	setCalls      int
	aliasGetCalls int
	aliasSetCalls int
	tryLockCalls  int
	setRecords    []DetailRecord
}

type fakeRAGIngestor struct {
	calls   int
	records chan StoredProductDetailWithSource
	err     error
}

func (i *fakeRAGIngestor) IngestProductDetail(_ context.Context, record StoredProductDetailWithSource) error {
	i.calls++
	if i.records != nil {
		i.records <- record
	}
	return i.err
}

func (c *fakeDetailHotCache) Get(_ context.Context, productKey string) (DetailRecord, bool, error) {
	c.getCalls++
	if c.getErr != nil {
		return DetailRecord{}, false, c.getErr
	}
	if c.byKey == nil {
		return DetailRecord{}, false, nil
	}
	record, ok := c.byKey[productKey]
	return record, ok, nil
}

func (c *fakeDetailHotCache) Set(_ context.Context, record DetailRecord, _ time.Duration) error {
	c.setCalls++
	if c.setErr != nil {
		return c.setErr
	}
	if c.byKey == nil {
		c.byKey = map[string]DetailRecord{}
	}
	c.byKey[record.ProductKey] = record
	c.setRecords = append(c.setRecords, record)
	return nil
}

func (c *fakeDetailHotCache) GetAlias(_ context.Context, normalizedURLHash string) (string, bool, error) {
	c.aliasGetCalls++
	if c.aliasErr != nil {
		return "", false, c.aliasErr
	}
	if c.aliases == nil {
		return "", false, nil
	}
	productKey, ok := c.aliases[normalizedURLHash]
	return productKey, ok, nil
}

func (c *fakeDetailHotCache) SetAlias(_ context.Context, normalizedURLHash, productKey string, _ time.Duration) error {
	c.aliasSetCalls++
	if c.aliasErr != nil {
		return c.aliasErr
	}
	if c.aliases == nil {
		c.aliases = map[string]string{}
	}
	c.aliases[normalizedURLHash] = productKey
	return nil
}

func (c *fakeDetailHotCache) TryLock(context.Context, string, time.Duration) (DetailLock, bool, error) {
	signalTestChan(c.lockStarted)
	c.tryLockCalls++
	if c.lockErr != nil || !c.lockGranted {
		return nil, c.lockGranted, c.lockErr
	}
	return fakeDetailLock{}, true, nil
}

type fakeDetailLock struct{}

func (fakeDetailLock) Release(context.Context) error {
	return nil
}

func signalTestChan(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func TestValidateExtraction(t *testing.T) {
	duties := []schema.DutyItem{
		{Name: "一般医疗保险金", Coverage: "300万"},
		{Name: "重大疾病保险金", Coverage: "300万"},
		{Name: "不存在的保障", Coverage: "100万"},
	}
	text := "本产品包含一般医疗保险金和重大疾病保险金。"

	passed, rate, reason := ValidateExtraction(duties, text)
	if passed {
		t.Fatalf("passed = true, want false below threshold; %s", reason)
	}
	if rate >= 0.7 {
		t.Fatalf("rate = %f, want below 0.7", rate)
	}

	passed, rate, _ = ValidateExtraction(duties[:2], text)
	if !passed || rate != 1 {
		t.Fatalf("ValidateExtraction = (%v, %f), want true and 1", passed, rate)
	}
}

func TestParseExtractionJSONSupportsMarkdownWrapper(t *testing.T) {
	raw := "```json\n{\"product_name\":\"测试产品\",\"duties\":[{\"name\":\"一般医疗\",\"coverage\":\"300万\",\"description\":\"说明\",\"is_optional\":false},{\"name\":\"\",\"coverage\":\"100万\"}]}\n```"

	payload, ok := ParseExtractionJSON(raw)
	if !ok {
		t.Fatal("expected parse success")
	}
	if payload.ProductName != "测试产品" {
		t.Fatalf("ProductName = %q", payload.ProductName)
	}
	if len(payload.Duties) != 1 || payload.Duties[0].Name != "一般医疗" {
		t.Fatalf("duties = %#v", payload.Duties)
	}
}

func TestPinganProductInfoURLExtractsHashProductCode(t *testing.T) {
	endpoint, ok := buildPinganProductInfoURL("https://baoxian.pingan.com/pa18shopnst/quote/pc/index.html#/ZP021636")
	if !ok {
		t.Fatal("expected pingan product info endpoint")
	}
	if !strings.Contains(endpoint, "/pa18shopnst/do/era/core/base/productInfo") {
		t.Fatalf("endpoint path = %s", endpoint)
	}
	if !strings.Contains(endpoint, "productCode=ZP021636") {
		t.Fatalf("endpoint query = %s", endpoint)
	}
}

func TestParsePinganProductInfoTextFormatsModelInput(t *testing.T) {
	text, productName, ok := parsePinganProductInfoText(pinganProductInfoFixture, "")
	if !ok {
		t.Fatal("expected pingan product info text")
	}
	if productName != "平安医无忧·中高端医疗险(互联网版)" {
		t.Fatalf("productName = %q", productName)
	}
	for _, want := range []string{
		"商品渠道：平安保险商城",
		"产品编码：ZP021636",
		"保障责任：",
		"一般医疗费用保险责任(共享600万保额)：保额/限额600万",
		"特定疾病医疗费用保险责任(共享600万保额)：保额/限额600万",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("formatted text missing %q:\n%s", want, text)
		}
	}
}

func TestServiceFeedsPinganPlatformTextToExtractionModel(t *testing.T) {
	fetcher := &routeFetcher{responses: map[string]string{
		"/pa18shopnst/do/era/core/base/productInfo": pinganProductInfoFixture,
	}}
	model := &fakeModel{
		text:   `{"product_name":"平安医无忧·中高端医疗险(互联网版)","duties":[{"name":"一般医疗费用保险责任(共享600万保额)","coverage":"600万","description":"医院范围：公立普通部+269家私立/民营医院","is_optional":false},{"name":"特定疾病医疗费用保险责任(共享600万保额)","coverage":"600万","description":"医院范围：公立普通部+特需部、国际部、VIP部，269家私立/民营医院","is_optional":false}]}`,
		stream: []string{"这是平安渠道解读"},
	}
	svc := NewService(WithFetcher(fetcher), WithCache(NewCache(time.Hour)), WithModel(model))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: "https://baoxian.pingan.com/pa18shopnst/quote/pc/index.html#/ZP021636",
	}))

	if len(fetcher.calls) != 1 || !strings.Contains(fetcher.calls[0], "productCode=ZP021636") {
		t.Fatalf("fetch calls = %#v", fetcher.calls)
	}
	if model.callTextCalls == 0 {
		t.Fatal("expected LLM extraction call")
	}
	modelInput := joinedMessages(model.lastMessages)
	for _, want := range []string{"商品渠道：平安保险商城", "保障责任：", "一般医疗费用保险责任(共享600万保额)"} {
		if !strings.Contains(modelInput, want) {
			t.Fatalf("model input missing %q:\n%s", want, modelInput)
		}
	}
	payload, ok := findDetailItems(events)
	if !ok {
		t.Fatal("missing detail_items event")
	}
	if payload.ProductName != "平安医无忧·中高端医疗险(互联网版)" || len(payload.Duties) != 2 {
		t.Fatalf("unexpected detail_items: %#v", payload)
	}
	if !strings.Contains(deltaText(events), "这是平安渠道解读") {
		t.Fatalf("delta text = %s", deltaText(events))
	}
}

func TestServiceFetchesCleansFallbackExtractsAndCaches(t *testing.T) {
	html := `<html><body>
<h1>测试百万医疗险</h1>
<p>一般医疗保险金 300万 保障住院医疗费用和特殊门诊费用。</p>
<p>重大疾病医疗保险金 300万 保障重大疾病治疗费用。</p>
<p>可选外购药保险金 100万 可附加报销院外特定药品费用。</p>
</body></html>`
	fetcher := &fakeFetcher{html: html}
	cache := NewCache(time.Hour)
	svc := NewService(WithFetcher(fetcher), WithCache(cache))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:      "product_detail",
		ProductURL:  "https://example.com/product/1",
		ProductName: "测试百万医疗险",
	}))

	if fetcher.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetcher.calls)
	}
	payload, ok := findDetailItems(events)
	if !ok {
		t.Fatalf("missing detail_items event: %#v", events)
	}
	if payload.ProductName != "测试百万医疗险" {
		t.Fatalf("ProductName = %q", payload.ProductName)
	}
	if len(payload.Duties) < 3 {
		t.Fatalf("duties = %#v, want at least 3", payload.Duties)
	}
	if _, ok := cache.Get("https://example.com/product/1"); !ok {
		t.Fatal("expected cached product detail")
	}
	if !strings.Contains(deltaText(events), "主要保障") {
		t.Fatalf("fallback answer missing summary: %s", deltaText(events))
	}
}

func TestServiceUsesLLMExtractionWhenValidated(t *testing.T) {
	html := `<p>LLM百万医疗险</p><p>一般医疗保险金 300万 保障住院医疗费用、特殊门诊费用和住院前后门急诊费用。</p><p>重大疾病保险金 300万 保障重疾治疗费用、重大疾病住院医疗费用和相关检查费用。</p>`
	model := &fakeModel{
		text:   `{"product_name":"LLM百万医疗险","duties":[{"name":"一般医疗保险金","coverage":"300万","description":"住院医疗费用","is_optional":false},{"name":"重大疾病保险金","coverage":"300万","description":"重疾治疗费用","is_optional":false}]}`,
		stream: []string{"这是LLM解读"},
	}
	svc := NewService(WithFetcher(&fakeFetcher{html: html}), WithCache(NewCache(time.Hour)), WithModel(model))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: "https://example.com/product/llm",
	}))

	if model.callTextCalls == 0 {
		t.Fatal("expected LLM extraction call")
	}
	payload, ok := findDetailItems(events)
	if !ok {
		t.Fatal("missing detail_items event")
	}
	if payload.ProductName != "LLM百万医疗险" {
		t.Fatalf("ProductName = %q", payload.ProductName)
	}
	if !strings.Contains(deltaText(events), "这是LLM解读") {
		t.Fatalf("delta text = %s", deltaText(events))
	}
}

func TestServiceCacheHitSkipsFetchAndEmitsDetailItems(t *testing.T) {
	cache := NewCache(time.Hour)
	url := "https://example.com/product/cached"
	cache.Set(url, schema.ProductDetail{
		ProductName: "缓存医疗险",
		ProductURL:  url,
		Duties: []schema.DutyItem{{
			Name:        "一般医疗保险金",
			Coverage:    "300万",
			Description: "保障住院医疗费用",
		}},
	})
	fetcher := &fakeFetcher{err: errors.New("should not fetch")}
	svc := NewService(WithFetcher(fetcher), WithCache(cache))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: url,
	}))

	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", fetcher.calls)
	}
	payload, ok := findDetailItems(events)
	if !ok {
		t.Fatal("cache hit should emit cached detail_items")
	}
	if payload.ProductName != "缓存医疗险" || len(payload.Duties) != 1 {
		t.Fatalf("unexpected cached detail_items: %#v", payload)
	}
	if !strings.Contains(deltaText(events), "一般医疗保险金") {
		t.Fatalf("delta text = %s", deltaText(events))
	}
}

func TestServiceRedisHitSkipsFetchAndBackfillsLocalCache(t *testing.T) {
	url := "https://example.com/product/redis?utm_source=ad&id=1"
	identity, err := NewProductKeyer().Key(url)
	if err != nil {
		t.Fatal(err)
	}
	detail := schema.ProductDetail{
		ProductName: "Redis医疗险",
		ProductURL:  identity.NormalizedURL,
		Platform:    identity.Platform,
		Duties: []schema.DutyItem{{
			Name:        "一般医疗保险金",
			Coverage:    "300万",
			Description: "保障住院医疗费用",
		}},
		CNCharCount: 100,
		MatchRate:   1,
	}
	hotCache := &fakeDetailHotCache{
		byKey: map[string]DetailRecord{
			identity.ProductKey: {
				ProductKey:    identity.ProductKey,
				Platform:      identity.Platform,
				CanonicalURL:  identity.NormalizedURL,
				Detail:        detail,
				Status:        DetailStatusActive,
				PromptVersion: "detail_extract_v1",
				ExpiresAt:     time.Now().Add(time.Hour),
			},
		},
		aliases: map[string]string{identity.URLHash: identity.ProductKey},
	}
	fetcher := &fakeFetcher{err: errors.New("should not fetch")}
	cache := NewCache(time.Hour)
	svc := NewService(WithFetcher(fetcher), WithCache(cache), WithHotCache(hotCache))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: url,
	}))

	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", fetcher.calls)
	}
	payload, ok := findDetailItems(events)
	if !ok || payload.ProductName != "Redis医疗险" {
		t.Fatalf("unexpected detail_items: %#v ok=%v", payload, ok)
	}
	if _, ok := cache.Get(identity.ProductKey); !ok {
		t.Fatal("redis hit should backfill local cache by product_key")
	}
}

func TestServiceMySQLHitBackfillsRedisAndLocalCache(t *testing.T) {
	url := "https://example.com/product/mysql?from=share&id=2"
	identity, err := NewProductKeyer().Key(url)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(time.Hour)
	detail := schema.ProductDetail{
		ProductName: "MySQL医疗险",
		ProductURL:  identity.NormalizedURL,
		Platform:    identity.Platform,
		Duties: []schema.DutyItem{{
			Name:        "重大疾病医疗保险金",
			Coverage:    "300万",
			Description: "保障重疾治疗费用",
		}},
		CNCharCount: 100,
		MatchRate:   1,
	}
	repo := &fakeDetailRepository{byURL: map[string]*StoredProductDetail{
		identity.NormalizedURL: {
			ProductKey:    identity.ProductKey,
			Platform:      identity.Platform,
			ProductName:   detail.ProductName,
			CanonicalURL:  identity.NormalizedURL,
			URLHash:       identity.URLHash,
			Detail:        detail,
			Status:        DetailStatusActive,
			PromptVersion: "detail_extract_v1",
			ExpiresAt:     &expiresAt,
		},
	}}
	hotCache := &fakeDetailHotCache{}
	cache := NewCache(time.Hour)
	fetcher := &fakeFetcher{err: errors.New("should not fetch")}
	svc := NewService(WithFetcher(fetcher), WithCache(cache), WithRepository(repo), WithHotCache(hotCache))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: url,
	}))

	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", fetcher.calls)
	}
	if repo.getURLCalls == 0 || repo.touchCalls == 0 {
		t.Fatalf("repo calls getURL=%d touch=%d", repo.getURLCalls, repo.touchCalls)
	}
	if hotCache.setCalls == 0 || hotCache.aliasSetCalls == 0 {
		t.Fatalf("mysql hit should backfill redis: set=%d aliasSet=%d", hotCache.setCalls, hotCache.aliasSetCalls)
	}
	if _, ok := cache.Get(identity.ProductKey); !ok {
		t.Fatal("mysql hit should backfill local cache by product_key")
	}
	payload, ok := findDetailItems(events)
	if !ok || payload.ProductName != "MySQL医疗险" {
		t.Fatalf("unexpected detail_items: %#v ok=%v", payload, ok)
	}
}

func TestServiceStoredStatusDoesNotBlockVisibleParse(t *testing.T) {
	url := "https://example.com/product/disabled?id=4"
	identity, err := NewProductKeyer().Key(url)
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeDetailRepository{byKey: map[string]*StoredProductDetail{
		identity.ProductKey: {
			ProductKey:    identity.ProductKey,
			Platform:      identity.Platform,
			CanonicalURL:  identity.NormalizedURL,
			Status:        DetailStatusDisabled,
			PromptVersion: "detail_extract_v1",
		},
	}}
	fetcher := &fakeFetcher{html: `<html><body>
<h1>前台展示医疗险</h1>
<p>一般医疗保险金 300万 保障住院医疗费用、特殊门诊费用和住院前后门急诊费用。</p>
<p>重大疾病医疗保险金 300万 保障重大疾病治疗费用和相关检查费用。</p>
</body></html>`}
	svc := NewService(WithFetcher(fetcher), WithCache(NewCache(time.Hour)), WithRepository(repo))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: url,
	}))

	if fetcher.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetcher.calls)
	}
	if _, ok := findDetailItems(events); !ok {
		t.Fatalf("stored disabled status should not block visible detail_items: %#v", events)
	}
	if strings.Contains(deltaText(events), "暂不重复解析") {
		t.Fatalf("delta text = %s", deltaText(events))
	}
}

func TestServicePersistLockBusyDoesNotBlockVisibleParse(t *testing.T) {
	url := "https://example.com/product/lock-busy?id=5"
	repo := &fakeDetailRepository{}
	hotCache := &fakeDetailHotCache{lockGranted: false, lockStarted: make(chan struct{}, 1)}
	fetcher := &fakeFetcher{html: `<html><body>
<h1>锁忙展示医疗险</h1>
<p>一般医疗保险金 300万 保障住院医疗费用、特殊门诊费用和住院前后门急诊费用。</p>
<p>重大疾病医疗保险金 300万 保障重大疾病治疗费用和相关检查费用。</p>
</body></html>`}
	model := &fakeModel{
		text:   `{"product_name":"锁忙展示医疗险","duties":[{"name":"一般医疗保险金","coverage":"300万","description":"保障住院医疗费用","is_optional":false},{"name":"重大疾病医疗保险金","coverage":"300万","description":"保障重疾治疗费用","is_optional":false}]}`,
		stream: []string{"这是前台展示解读"},
	}
	svc := NewService(
		WithFetcher(fetcher),
		WithCache(NewCache(time.Hour)),
		WithRepository(repo),
		WithHotCache(hotCache),
		WithModel(model),
		WithSharedCacheTTL(time.Hour, time.Hour, time.Hour, 20*time.Millisecond),
	)

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_detail",
		ProductURL: url,
	}))

	waitSignal(t, hotCache.lockStarted, "persist lock started")
	if hotCache.tryLockCalls != 1 {
		t.Fatalf("tryLock calls = %d, want 1", hotCache.tryLockCalls)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetcher.calls)
	}
	if model.callTextCalls == 0 {
		t.Fatal("expected visible parse to call model")
	}
	if _, ok := findDetailItems(events); !ok {
		t.Fatalf("lock-busy persist should not block visible detail_items: %#v", events)
	}
	if strings.Contains(deltaText(events), "正在被其他用户解析") {
		t.Fatalf("delta text = %s", deltaText(events))
	}
	if repo.upsertCalls != 0 {
		t.Fatalf("upsert calls = %d, want 0 when persist lock busy", repo.upsertCalls)
	}
}

func TestServiceFollowupUsesCacheAndMissingCacheIsClear(t *testing.T) {
	cache := NewCache(time.Hour)
	url := "https://example.com/product/followup"
	cache.Set(url, schema.ProductDetail{
		ProductName: "追问医疗险",
		ProductURL:  url,
		Duties: []schema.DutyItem{{
			Name:        "外购药保险金",
			Coverage:    "100万",
			Description: "报销院外特定药品费用",
		}},
	})
	fetcher := &fakeFetcher{err: errors.New("should not fetch")}
	svc := NewService(WithFetcher(fetcher), WithCache(cache))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_followup",
		ProductURL: url,
		Message:    "外购药能报吗？",
	}))

	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", fetcher.calls)
	}
	if !strings.Contains(deltaText(events), "外购药") {
		t.Fatalf("delta text = %s", deltaText(events))
	}

	miss := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_followup",
		ProductURL: "https://example.com/product/miss",
		Message:    "能报吗？",
	}))
	if !strings.Contains(deltaText(miss), "暂未找到该产品的详情缓存") {
		t.Fatalf("missing-cache delta = %s", deltaText(miss))
	}
}

func TestServiceFollowupUsesSharedRepositoryWithoutDetailItems(t *testing.T) {
	url := "https://example.com/product/followup-shared?id=3"
	identity, err := NewProductKeyer().Key(url)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(time.Hour)
	detail := schema.ProductDetail{
		ProductName: "共享追问医疗险",
		ProductURL:  identity.NormalizedURL,
		Platform:    identity.Platform,
		Duties: []schema.DutyItem{{
			Name:        "外购药保险金",
			Coverage:    "100万",
			Description: "报销院外特定药品费用",
		}},
		CNCharCount: 100,
		MatchRate:   1,
	}
	repo := &fakeDetailRepository{byURL: map[string]*StoredProductDetail{
		identity.NormalizedURL: {
			ProductKey:    identity.ProductKey,
			Platform:      identity.Platform,
			ProductName:   detail.ProductName,
			CanonicalURL:  identity.NormalizedURL,
			URLHash:       identity.URLHash,
			Detail:        detail,
			Status:        DetailStatusActive,
			PromptVersion: "detail_extract_v1",
			ExpiresAt:     &expiresAt,
		},
	}}
	fetcher := &fakeFetcher{err: errors.New("should not fetch")}
	svc := NewService(WithFetcher(fetcher), WithCache(NewCache(time.Hour)), WithRepository(repo), WithHotCache(&fakeDetailHotCache{}))

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:     "product_followup",
		ProductURL: url,
		Message:    "外购药能报吗？",
	}))

	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", fetcher.calls)
	}
	if _, ok := findDetailItems(events); ok {
		t.Fatalf("followup should not emit detail_items: %#v", events)
	}
	if !strings.Contains(deltaText(events), "外购药") {
		t.Fatalf("delta text = %s", deltaText(events))
	}
}

func TestServicePersistsSharedDetailAsyncWithoutBlockingAnswer(t *testing.T) {
	html := `<html><body>
<h1>异步写库医疗险</h1>
<p>一般医疗保险金 300万 保障住院医疗费用、特殊门诊费用和住院前后门急诊费用。</p>
<p>重大疾病医疗保险金 300万 保障重大疾病治疗费用、重大疾病住院医疗费用和相关检查费用。</p>
</body></html>`
	repo := &fakeDetailRepository{
		upsertStarted:  make(chan struct{}, 1),
		upsertContinue: make(chan struct{}),
		upsertDone:     make(chan struct{}, 1),
	}
	hotCache := &fakeDetailHotCache{lockGranted: true}
	svc := NewService(
		WithFetcher(&fakeFetcher{html: html}),
		WithCache(NewCache(time.Hour)),
		WithRepository(repo),
		WithHotCache(hotCache),
	)

	events := svc.Run(context.Background(), Request{
		Action:      "product_detail",
		ProductURL:  "https://example.com/product/async-persist",
		ProductName: "异步写库医疗险",
	})

	if event := waitEventName(t, events, schema.SSEEventDetailItems); event.Name == "" {
		t.Fatal("missing detail_items")
	}
	waitSignal(t, repo.upsertStarted, "upsert started")
	delta := waitEventName(t, events, schema.SSEEventDelta)
	if !strings.Contains(deltaText([]Event{delta}), "主要保障") {
		t.Fatalf("delta text = %#v", delta.Data)
	}

	close(repo.upsertContinue)
	waitSignal(t, repo.upsertDone, "upsert done")
	if repo.upsertCalls != 1 {
		t.Fatalf("upsert calls = %d, want 1", repo.upsertCalls)
	}
	_ = collectEvents(events)
}

func TestServiceMySQLUpsertFailureDoesNotWriteRedis(t *testing.T) {
	html := `<html><body>
	<h1>写库失败医疗险</h1>
	<p>一般医疗保险金 300万 保障住院医疗费用、特殊门诊费用和住院前后门急诊费用。</p>
	<p>重大疾病医疗保险金 300万 保障重大疾病治疗费用、重大疾病住院医疗费用和相关检查费用。</p>
	</body></html>`
	repo := &fakeDetailRepository{upsertErr: errors.New("db down"), upsertDone: make(chan struct{}, 1)}
	hotCache := &fakeDetailHotCache{lockGranted: true}
	fetcher := &fakeFetcher{html: html}
	svc := NewService(
		WithFetcher(fetcher),
		WithCache(NewCache(time.Hour)),
		WithRepository(repo),
		WithHotCache(hotCache),
	)

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:      "product_detail",
		ProductURL:  "https://example.com/product/upsert-fail",
		ProductName: "写库失败医疗险",
	}))

	if fetcher.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetcher.calls)
	}
	waitSignal(t, repo.upsertDone, "upsert done")
	if repo.upsertCalls != 1 {
		t.Fatalf("upsert calls = %d, want 1", repo.upsertCalls)
	}
	if hotCache.setCalls != 0 || hotCache.aliasSetCalls != 0 {
		t.Fatalf("redis should not be written after mysql failure: set=%d alias=%d", hotCache.setCalls, hotCache.aliasSetCalls)
	}
	if _, ok := findDetailItems(events); !ok {
		t.Fatalf("current user should still receive detail_items: %#v", events)
	}
}

func TestServiceTriggersRAGIngestAfterMySQLUpsert(t *testing.T) {
	html := `<html><body>
	<h1>RAG入库医疗险</h1>
	<p>一般医疗保险金 300万 保障住院医疗费用、特殊门诊费用和住院前后门急诊费用。</p>
	<p>住院期间外购药品费用医疗保险金 100万 保障住院期间外购药费用。</p>
	</body></html>`
	repo := &fakeDetailRepository{upsertDone: make(chan struct{}, 1)}
	rag := &fakeRAGIngestor{records: make(chan StoredProductDetailWithSource, 1)}
	svc := NewService(
		WithFetcher(&fakeFetcher{html: html}),
		WithCache(NewCache(time.Hour)),
		WithRepository(repo),
		WithRAGIngestor(rag),
	)

	events := collectEvents(svc.Run(context.Background(), Request{
		Action:      "product_detail",
		ProductURL:  "https://example.com/product/rag-ingest?utm_source=ad&_rag_check=1&id=9#frag",
		ProductName: "RAG入库医疗险",
	}))

	waitSignal(t, repo.upsertDone, "upsert done")
	record := waitRAGRecord(t, rag.records)
	if repo.ragStateCalls != 2 || repo.lastRAGState.Status != RAGIngestStatusEnqueued || repo.lastRAGState.SourceHash != record.Detail.SourceHash {
		t.Fatalf("unexpected rag state calls=%d last=%#v record_hash=%s", repo.ragStateCalls, repo.lastRAGState, record.Detail.SourceHash)
	}
	if record.Detail.ProductKey == "" || record.Detail.SourceHash == "" {
		t.Fatalf("unexpected rag record detail: %#v", record.Detail)
	}
	if record.Source == nil || record.Source.SourceType != "web_page" || record.Source.SourceFormat != "html" || !strings.Contains(record.Source.CleanedText, "外购药") {
		t.Fatalf("unexpected rag source: %#v", record.Source)
	}
	if record.Source.SourceURL != "https://example.com/product/rag-ingest?id=9" {
		t.Fatalf("SourceURL = %q, want normalized product URL", record.Source.SourceURL)
	}
	if _, ok := findDetailItems(events); !ok {
		t.Fatalf("current user should still receive detail_items: %#v", events)
	}
}

func TestServiceRAGIngestLockBusyReturnsBeforeQueue(t *testing.T) {
	url := "https://example.com/product/rag-lock-busy?id=12"
	identity, err := NewProductKeyer().Key(url)
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeDetailRepository{}
	hotCache := &fakeDetailHotCache{lockGranted: false}
	rag := &fakeRAGIngestor{}
	svc := NewService(
		WithCache(NewCache(time.Hour)),
		WithRepository(repo),
		WithHotCache(hotCache),
		WithRAGIngestor(rag),
	)
	sourceHash := SHA256Hex("产品名称：锁忙\n保障责任：一般医疗保险金 300万")
	expiresAt := time.Now().Add(time.Hour)
	detail := schema.ProductDetail{
		ProductName: "锁忙医疗险",
		ProductURL:  identity.NormalizedURL,
		Platform:    identity.Platform,
		Duties: []schema.DutyItem{{
			Name:        "一般医疗保险金",
			Coverage:    "300万",
			Description: "保障住院医疗费用",
		}},
		CNCharCount: 100,
		MatchRate:   1,
	}

	svc.ingestSharedDetailRAG(context.Background(), identity, UpsertProductDetailInput{
		ProductKey:        identity.ProductKey,
		Platform:          identity.Platform,
		CanonicalURL:      identity.NormalizedURL,
		NormalizedURLHash: identity.URLHash,
		Detail:            detail,
		SourceHash:        sourceHash,
		PromptVersion:     "detail_extract_v1",
		Status:            DetailStatusActive,
		ExpiresAt:         &expiresAt,
	}, PreparedDetailSource{
		SourceURL:    identity.NormalizedURL,
		SourceType:   "web_page",
		SourceFormat: "text",
		CleanedText:  "产品名称：锁忙\n保障责任：一般医疗保险金 300万",
		CNCharCount:  100,
		FetchedAt:    time.Now(),
	}, sourceHash, expiresAt, time.Now(), nil)

	if hotCache.tryLockCalls != 1 {
		t.Fatalf("tryLock calls = %d, want 1", hotCache.tryLockCalls)
	}
	if rag.calls != 0 {
		t.Fatalf("rag calls = %d, want 0", rag.calls)
	}
	if repo.ragStateCalls != 0 {
		t.Fatalf("rag state calls = %d, want 0", repo.ragStateCalls)
	}
}

func TestServiceSkipsRAGIngestForActiveSameSourceHash(t *testing.T) {
	url := "https://example.com/product/rag-same-source?id=10"
	identity, err := NewProductKeyer().Key(url)
	if err != nil {
		t.Fatal(err)
	}
	cleanedText := "产品名称：重复入库医疗险\n保障责任：一般医疗保险金 300万"
	sourceHash := SHA256Hex(cleanedText)
	expiresAt := time.Now().Add(time.Hour)
	repo := &fakeDetailRepository{byKey: map[string]*StoredProductDetail{
		identity.ProductKey: {
			ProductKey:          identity.ProductKey,
			Platform:            identity.Platform,
			CanonicalURL:        identity.NormalizedURL,
			SourceHash:          sourceHash,
			PromptVersion:       "detail_extract_v1",
			Status:              DetailStatusActive,
			RAGIngestStatus:     RAGIngestStatusEnqueued,
			RAGIngestSourceHash: sourceHash,
			ExpiresAt:           &expiresAt,
		},
	}}
	rag := &fakeRAGIngestor{}
	svc := NewService(
		WithCache(NewCache(time.Hour)),
		WithRepository(repo),
		WithRAGIngestor(rag),
	)
	detail := schema.ProductDetail{
		ProductName: "重复入库医疗险",
		ProductURL:  identity.NormalizedURL,
		Platform:    identity.Platform,
		Duties: []schema.DutyItem{{
			Name:        "一般医疗保险金",
			Coverage:    "300万",
			Description: "保障住院医疗费用",
		}},
		CNCharCount: 100,
		MatchRate:   1,
	}

	svc.persistSharedDetailStores(context.Background(), identity, PreparedDetailSource{
		SourceURL:    identity.NormalizedURL,
		SourceType:   "web_page",
		SourceFormat: "text",
		CleanedText:  cleanedText,
		CNCharCount:  100,
		FetchedAt:    time.Now(),
	}, detail)

	if repo.upsertCalls != 0 {
		t.Fatalf("upsert calls = %d, want 0", repo.upsertCalls)
	}
	if rag.calls != 0 {
		t.Fatalf("rag calls = %d, want 0", rag.calls)
	}
	if repo.ragStateCalls != 0 {
		t.Fatalf("rag state calls = %d, want 0", repo.ragStateCalls)
	}
}

func TestServiceEnqueuesRAGForActiveSameSourceWhenRAGPending(t *testing.T) {
	url := "https://example.com/product/rag-pending?id=11"
	identity, err := NewProductKeyer().Key(url)
	if err != nil {
		t.Fatal(err)
	}
	cleanedText := "产品名称：待入库医疗险\n保障责任：一般医疗保险金 300万"
	sourceHash := SHA256Hex(cleanedText)
	expiresAt := time.Now().Add(time.Hour)
	repo := &fakeDetailRepository{byKey: map[string]*StoredProductDetail{
		identity.ProductKey: {
			ProductKey:      identity.ProductKey,
			Platform:        identity.Platform,
			CanonicalURL:    identity.NormalizedURL,
			SourceHash:      sourceHash,
			PromptVersion:   "detail_extract_v1",
			Status:          DetailStatusActive,
			RAGIngestStatus: RAGIngestStatusPending,
			ExpiresAt:       &expiresAt,
		},
	}}
	rag := &fakeRAGIngestor{}
	svc := NewService(
		WithCache(NewCache(time.Hour)),
		WithRepository(repo),
		WithRAGIngestor(rag),
	)
	detail := schema.ProductDetail{
		ProductName: "待入库医疗险",
		ProductURL:  identity.NormalizedURL,
		Platform:    identity.Platform,
		Duties: []schema.DutyItem{{
			Name:        "一般医疗保险金",
			Coverage:    "300万",
			Description: "保障住院医疗费用",
		}},
		CNCharCount: 100,
		MatchRate:   1,
	}

	svc.persistSharedDetailStores(context.Background(), identity, PreparedDetailSource{
		SourceURL:    identity.NormalizedURL,
		SourceType:   "web_page",
		SourceFormat: "text",
		CleanedText:  cleanedText,
		CNCharCount:  100,
		FetchedAt:    time.Now(),
	}, detail)

	if repo.upsertCalls != 0 {
		t.Fatalf("upsert calls = %d, want 0", repo.upsertCalls)
	}
	if rag.calls != 1 {
		t.Fatalf("rag calls = %d, want 1", rag.calls)
	}
	if repo.ragStateCalls != 2 || repo.lastRAGState.Status != RAGIngestStatusEnqueued || repo.lastRAGState.SourceHash != sourceHash {
		t.Fatalf("unexpected rag state calls=%d last=%#v", repo.ragStateCalls, repo.lastRAGState)
	}
}

func collectEvents(ch <-chan Event) []Event {
	var events []Event
	for event := range ch {
		events = append(events, event)
	}
	return events
}

func waitEventName(t *testing.T, ch <-chan Event, name string) Event {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				t.Fatalf("event stream closed before %s", name)
			}
			if event.Name == name {
				return event
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for event %s", name)
		}
	}
}

func waitRAGRecord(t *testing.T, ch <-chan StoredProductDetailWithSource) StoredProductDetailWithSource {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case record := <-ch:
		return record
	case <-timer.C:
		t.Fatal("timed out waiting for rag ingest record")
	}
	return StoredProductDetailWithSource{}
}

func waitSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", name)
	}
}

func findDetailItems(events []Event) (schema.SSEDetailItemsPayload, bool) {
	for _, event := range events {
		if event.Name != schema.SSEEventDetailItems {
			continue
		}
		payload, ok := event.Data.(schema.SSEDetailItemsPayload)
		return payload, ok
	}
	return schema.SSEDetailItemsPayload{}, false
}

func deltaText(events []Event) string {
	var b strings.Builder
	for _, event := range events {
		if event.Name != schema.SSEEventDelta {
			continue
		}
		switch data := event.Data.(type) {
		case schema.SSEDeltaPayload:
			b.WriteString(data.Text)
		case map[string]string:
			b.WriteString(data["text"])
		}
	}
	return b.String()
}

func joinedMessages(messages []llm.Message) string {
	var b strings.Builder
	for _, message := range messages {
		b.WriteString(message.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

const pinganProductInfoFixture = `{
  "technicProductCode": "TP0300015",
  "productName": "平安医无忧·中高端医疗险(互联网版)",
  "isSupportAutoRenew": "1",
  "leastApplicantAge": 16,
  "renewalGracePeriod": 60,
  "packageList": [{
    "productCode": "ZP021636",
    "planInfoList": [{
      "planName": "平安产险医疗费用补偿保险（2026版基础款）（互联网版）",
      "dutyInfoList": [{
        "isShowDuty": "1",
        "fixAmount": 6000000,
        "dutyDesc": "医院范围：公立普通部+269家私立/民营医院",
        "dutyName": "一般医疗费用保险责任(共享600万保额)",
        "totalInsuredAmount": 6000000,
        "insuredAmount": 6000000,
        "requiredCoverage": "1",
        "forceSelected": "1"
      }, {
        "isShowDuty": "1",
        "fixAmount": 6000000,
        "dutyDesc": "医院范围：公立普通部+特需部、国际部、VIP部，269家私立/民营医院",
        "dutyName": "特定疾病医疗费用保险责任(共享600万保额)",
        "totalInsuredAmount": 6000000,
        "insuredAmount": 6000000,
        "requiredCoverage": "1",
        "forceSelected": "1"
      }]
    }]
  }]
}`
