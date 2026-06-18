package productdetail

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/schema"
)

func TestProductDetailSchemaStatements(t *testing.T) {
	statements := ProductDetailSchemaStatements()
	if len(statements) != 3 {
		t.Fatalf("len(statements) = %d, want 3", len(statements))
	}
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS product_details",
		"price VARCHAR(64) NOT NULL DEFAULT ''",
		"price_label VARCHAR(64) NOT NULL DEFAULT ''",
		"detail_json JSON NOT NULL",
		"rag_ingest_status VARCHAR(20) NOT NULL DEFAULT 'pending'",
		"rag_ingest_source_hash VARCHAR(64) NOT NULL DEFAULT ''",
		"UNIQUE KEY uk_product_details_key (product_key)",
	} {
		if !strings.Contains(statements[0], want) {
			t.Fatalf("product_details schema missing %q:\n%s", want, statements[0])
		}
	}
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS product_detail_aliases",
		"UNIQUE KEY uk_product_detail_aliases_url_hash (normalized_url_hash)",
		"FOREIGN KEY (product_key) REFERENCES product_details(product_key)",
	} {
		if !strings.Contains(statements[1], want) {
			t.Fatalf("product_detail_aliases schema missing %q:\n%s", want, statements[1])
		}
	}
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS product_detail_sources",
		"raw_payload MEDIUMTEXT NULL",
		"UNIQUE KEY uk_product_detail_sources_key_hash (product_key, content_hash)",
		"FOREIGN KEY (product_key) REFERENCES product_details(product_key)",
	} {
		if !strings.Contains(statements[2], want) {
			t.Fatalf("product_detail_sources schema missing %q:\n%s", want, statements[2])
		}
	}
}

func TestBuildProductDetailUpsertStatementMarshalsDetailJSON(t *testing.T) {
	expiresAt := time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 9, 8, 30, 0, 0, time.UTC)
	input := UpsertProductDetailInput{
		Detail: schema.ProductDetail{
			ProductName: "测试百万医疗险",
			ProductURL:  "HTTPS://Example.COM/Product/1/?utm_source=ad&id=9#frag",
			Price:       "323元/年起",
			PriceLabel:  "323元/年起",
			Duties: []schema.DutyItem{{
				Name:        "一般医疗保险金",
				Coverage:    "300万",
				Description: "保障住院医疗费用",
			}},
			CNCharCount: 1234,
			MatchRate:   0.92,
		},
		SourceHash:    "source-hash",
		PromptVersion: "detail-v1",
		ModelName:     "extract-model",
		ExpiresAt:     &expiresAt,
	}

	record, err := normalizeProductDetailUpsert(input, NewProductKeyer())
	if err != nil {
		t.Fatalf("normalizeProductDetailUpsert error = %v", err)
	}
	wantURL := "https://example.com/Product/1?id=9"
	wantHash := ProductURLHash(wantURL)
	if record.ProductKey != "unknown:url:"+wantHash {
		t.Fatalf("ProductKey = %q", record.ProductKey)
	}
	if record.Platform != ProductPlatformUnknown {
		t.Fatalf("Platform = %q", record.Platform)
	}
	if record.CanonicalURL != wantURL || record.URLHash != wantHash {
		t.Fatalf("canonical/hash = %q/%q, want %q/%q", record.CanonicalURL, record.URLHash, wantURL, wantHash)
	}

	stmt, err := buildProductDetailUpsertStatement(record, now)
	if err != nil {
		t.Fatalf("buildProductDetailUpsertStatement error = %v", err)
	}
	if !strings.Contains(stmt.Query, "ON DUPLICATE KEY UPDATE") {
		t.Fatalf("upsert SQL missing duplicate update:\n%s", stmt.Query)
	}
	if len(stmt.Args) != 20 {
		t.Fatalf("len(args) = %d, want 20", len(stmt.Args))
	}
	if stmt.Args[0] != record.ProductKey || stmt.Args[3] != wantURL || stmt.Args[18] != now || stmt.Args[19] != now {
		t.Fatalf("unexpected upsert args: %#v", stmt.Args)
	}
	if stmt.Args[4] != "323元/年起" || stmt.Args[5] != "323元/年起" {
		t.Fatalf("unexpected price args: %#v", stmt.Args[4:6])
	}
	if stmt.Args[13] != RAGIngestStatusPending || stmt.Args[14] != "" {
		t.Fatalf("unexpected rag ingest args: %#v", stmt.Args[13:17])
	}
	if stmt.Args[17] != expiresAt {
		t.Fatalf("expires arg = %#v, want %v", stmt.Args[17], expiresAt)
	}

	var detail schema.ProductDetail
	rawJSON, ok := stmt.Args[6].(string)
	if !ok {
		t.Fatalf("detail_json arg type = %T, want string", stmt.Args[6])
	}
	if err := json.Unmarshal([]byte(rawJSON), &detail); err != nil {
		t.Fatalf("detail_json unmarshal error = %v", err)
	}
	if detail.ProductURL != wantURL {
		t.Fatalf("detail.ProductURL = %q, want canonical URL", detail.ProductURL)
	}
	if detail.Platform != ProductPlatformUnknown {
		t.Fatalf("detail.Platform = %q, want unknown", detail.Platform)
	}
	if detail.Price != "323元/年起" || detail.PriceLabel != "323元/年起" {
		t.Fatalf("detail price = %q/%q", detail.Price, detail.PriceLabel)
	}
	if len(detail.Duties) != 1 || detail.Duties[0].Name != "一般医疗保险金" {
		t.Fatalf("detail.Duties = %#v", detail.Duties)
	}
}

func TestBuildProductDetailAliasUpsertStatement(t *testing.T) {
	now := time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC)
	record := StoredProductDetail{
		ProductKey:   "pingan:ZP021636",
		Platform:     ProductPlatformPingan,
		CanonicalURL: "https://baoxian.pingan.com/product",
		URLHash:      ProductURLHash("https://baoxian.pingan.com/product"),
	}

	stmt := buildProductDetailAliasUpsertStatement(record, now)
	if !strings.Contains(stmt.Query, "INSERT INTO product_detail_aliases") ||
		!strings.Contains(stmt.Query, "ON DUPLICATE KEY UPDATE") {
		t.Fatalf("unexpected alias SQL:\n%s", stmt.Query)
	}
	wantArgs := []any{record.URLHash, record.CanonicalURL, record.ProductKey, record.Platform, now, now}
	if len(stmt.Args) != len(wantArgs) {
		t.Fatalf("len(args) = %d, want %d", len(stmt.Args), len(wantArgs))
	}
	for i, want := range wantArgs {
		if stmt.Args[i] != want {
			t.Fatalf("arg[%d] = %#v, want %#v", i, stmt.Args[i], want)
		}
	}
}

func TestBuildProductDetailSourceUpsertStatement(t *testing.T) {
	now := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	raw := "<html>一般医疗保险金</html>"
	record := StoredProductDetail{
		ProductKey:   "unknown:url:abc",
		CanonicalURL: "https://example.com/product?id=1",
		URLHash:      "url-hash",
		CNCharCount:  99,
	}

	stmt, ok, err := buildProductDetailSourceUpsertStatement(record, &UpsertProductDetailSourceInput{
		SourceType:   "WEB_PAGE",
		SourceFormat: "HTML",
		RawPayload:   &raw,
		CleanedText:  "产品名称：测试\n保障责任：一般医疗保险金",
		FetchedAt:    now.Add(-time.Minute),
	}, now)
	if err != nil {
		t.Fatalf("buildProductDetailSourceUpsertStatement error = %v", err)
	}
	if !ok {
		t.Fatal("source statement not built")
	}
	if !strings.Contains(stmt.Query, "INSERT INTO product_detail_sources") ||
		!strings.Contains(stmt.Query, "ON DUPLICATE KEY UPDATE") {
		t.Fatalf("unexpected source SQL:\n%s", stmt.Query)
	}
	if len(stmt.Args) != 12 {
		t.Fatalf("len(args) = %d, want 12", len(stmt.Args))
	}
	if stmt.Args[0] != record.ProductKey || stmt.Args[1] != record.URLHash || stmt.Args[2] != record.CanonicalURL {
		t.Fatalf("unexpected source identity args: %#v", stmt.Args[:3])
	}
	if stmt.Args[3] != "web_page" || stmt.Args[4] != "html" {
		t.Fatalf("unexpected source type/format: %#v", stmt.Args[3:5])
	}
	if stmt.Args[7] != SHA256Hex("产品名称：测试\n保障责任：一般医疗保险金") {
		t.Fatalf("unexpected source hash: %v", stmt.Args[7])
	}
}

func TestBuildProductDetailPriceUpdateStatement(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	stmt := buildProductDetailPriceUpdateStatement("huize:url:abc", "323元/年起", "323元/年起", now)
	for _, want := range []string{
		"UPDATE product_details",
		"price = ?",
		"price_label = ?",
		"JSON_SET(detail_json",
		"WHERE product_key = ?",
	} {
		if !strings.Contains(stmt.Query, want) {
			t.Fatalf("price update SQL missing %q:\n%s", want, stmt.Query)
		}
	}
	wantArgs := []any{"323元/年起", "323元/年起", "323元/年起", "323元/年起", now, "huize:url:abc"}
	if len(stmt.Args) != len(wantArgs) {
		t.Fatalf("len(args) = %d, want %d: %#v", len(stmt.Args), len(wantArgs), stmt.Args)
	}
	for i, want := range wantArgs {
		if stmt.Args[i] != want {
			t.Fatalf("arg[%d] = %#v, want %#v", i, stmt.Args[i], want)
		}
	}
}

func TestBuildProductDetailListActiveStatement(t *testing.T) {
	after := time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC)
	now := after.Add(time.Hour)
	stmt := buildProductDetailListActiveStatement(ListProductDetailParams{
		Limit:           20,
		Platform:        ProductPlatformHuize,
		PromptVersion:   "detail-v1",
		MinMatchRate:    0.6,
		Now:             now,
		AfterUpdatedAt:  &after,
		AfterProductKey: "huize:1",
		RequireSource:   true,
	})
	for _, want := range []string{
		"LEFT JOIN product_detail_sources s",
		"d.match_rate >= ?",
		"d.platform = ?",
		"d.prompt_version = ?",
		"d.updated_at > ?",
		"s.id IS NOT NULL",
		"ORDER BY d.updated_at ASC, d.product_key ASC",
		"LIMIT ?",
	} {
		if !strings.Contains(stmt.Query, want) {
			t.Fatalf("list active SQL missing %q:\n%s", want, stmt.Query)
		}
	}
	if len(stmt.Args) != 9 {
		t.Fatalf("len(args) = %d, want 9: %#v", len(stmt.Args), stmt.Args)
	}
	if stmt.Args[len(stmt.Args)-1] != 20 {
		t.Fatalf("limit arg = %#v, want 20", stmt.Args[len(stmt.Args)-1])
	}
}

func TestScanStoredProductDetailDecodesJSONAndFillsDefaults(t *testing.T) {
	createdAt := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	lastHitAt := updatedAt.Add(time.Minute)
	ragUpdatedAt := updatedAt.Add(30 * time.Second)
	detailJSON := []byte(`{"product_name":"","product_url":"","platform":"","duties":[{"name":"一般医疗保险金","coverage":"300万","description":"保障住院医疗费用","is_optional":false}],"cn_char_count":0,"match_rate":0}`)

	record, err := scanStoredProductDetail(fakeDetailRow{values: []any{
		"unknown:url:abc",
		ProductPlatformUnknown,
		"测试百万医疗险",
		"https://example.com/product",
		"188元/年起",
		"188元/年起",
		detailJSON,
		"source-hash",
		"detail-v1",
		"extract-model",
		1234,
		0.91,
		DetailStatusActive,
		RAGIngestStatusEnqueued,
		"source-hash",
		"",
		ragUpdatedAt,
		nil,
		createdAt,
		updatedAt,
		lastHitAt,
	}})
	if err != nil {
		t.Fatalf("scanStoredProductDetail error = %v", err)
	}
	if record.Detail.ProductName != record.ProductName {
		t.Fatalf("Detail.ProductName = %q, want %q", record.Detail.ProductName, record.ProductName)
	}
	if record.Detail.ProductURL != record.CanonicalURL {
		t.Fatalf("Detail.ProductURL = %q, want %q", record.Detail.ProductURL, record.CanonicalURL)
	}
	if record.Detail.Price != "188元/年起" || record.Detail.PriceLabel != "188元/年起" {
		t.Fatalf("Detail price = %q/%q", record.Detail.Price, record.Detail.PriceLabel)
	}
	if record.Detail.Platform != record.Platform {
		t.Fatalf("Detail.Platform = %q, want %q", record.Detail.Platform, record.Platform)
	}
	if record.Detail.CNCharCount != 1234 || record.Detail.MatchRate != 0.91 {
		t.Fatalf("detail cn/match = %d/%f", record.Detail.CNCharCount, record.Detail.MatchRate)
	}
	if record.LastHitAt == nil || !record.LastHitAt.Equal(lastHitAt) {
		t.Fatalf("LastHitAt = %#v, want %v", record.LastHitAt, lastHitAt)
	}
	if record.RAGIngestStatus != RAGIngestStatusEnqueued || record.RAGIngestSourceHash != "source-hash" || record.RAGIngestUpdatedAt == nil || !record.RAGIngestUpdatedAt.Equal(ragUpdatedAt) {
		t.Fatalf("unexpected rag ingest fields: %#v", record)
	}
}

func TestScanStoredProductDetailWithSource(t *testing.T) {
	createdAt := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	sourceCreatedAt := createdAt.Add(2 * time.Minute)
	fetchedAt := createdAt.Add(-time.Minute)
	ragUpdatedAt := updatedAt.Add(30 * time.Second)
	raw := "<html>产品原文</html>"
	detailJSON := []byte(`{"product_name":"测试百万医疗险","product_url":"https://example.com/product","platform":"unknown","duties":[{"name":"一般医疗保险金","coverage":"300万","description":"保障住院医疗费用","is_optional":false}],"cn_char_count":1234,"match_rate":0.91}`)

	record, err := scanStoredProductDetailWithSource(fakeDetailRow{values: []any{
		"unknown:url:abc",
		ProductPlatformUnknown,
		"测试百万医疗险",
		"https://example.com/product",
		"288元/年起",
		"288元/年起",
		detailJSON,
		"source-hash",
		"detail-v1",
		"extract-model",
		1234,
		0.91,
		DetailStatusActive,
		RAGIngestStatusIngested,
		"source-hash",
		"",
		ragUpdatedAt,
		nil,
		createdAt,
		updatedAt,
		nil,
		int64(7),
		"unknown:url:abc",
		"url-hash",
		"https://example.com/product",
		"web_page",
		"html",
		raw,
		"清洗后的产品原文",
		"source-hash",
		int64(1234),
		fetchedAt,
		sourceCreatedAt,
		sourceCreatedAt,
	}})
	if err != nil {
		t.Fatalf("scanStoredProductDetailWithSource error = %v", err)
	}
	if record.Source == nil {
		t.Fatal("Source is nil")
	}
	if record.Source.ID != 7 || record.Source.ContentHash != "source-hash" || record.Source.CleanedText != "清洗后的产品原文" {
		t.Fatalf("unexpected source: %#v", record.Source)
	}
	if record.Source.RawPayload == nil || *record.Source.RawPayload != raw {
		t.Fatalf("RawPayload = %#v, want %q", record.Source.RawPayload, raw)
	}
	if record.Detail.Detail.ProductName != "测试百万医疗险" {
		t.Fatalf("ProductName = %q", record.Detail.Detail.ProductName)
	}
}

func TestScanStoredProductDetailMapsNoRows(t *testing.T) {
	_, err := scanStoredProductDetail(fakeDetailRow{err: sql.ErrNoRows})
	if !errors.Is(err, ErrProductDetailNotFound) {
		t.Fatalf("err = %v, want ErrProductDetailNotFound", err)
	}
}

type fakeDetailRow struct {
	values []any
	err    error
}

func (r fakeDetailRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("fakeDetailRow: destination count mismatch")
	}
	for i, value := range r.values {
		assignFakeDetailValue(dest[i], value)
	}
	return nil
}

func assignFakeDetailValue(dest, value any) {
	switch d := dest.(type) {
	case *string:
		*d = value.(string)
	case *[]byte:
		*d = value.([]byte)
	case *int:
		*d = value.(int)
	case *float64:
		*d = value.(float64)
	case *time.Time:
		*d = value.(time.Time)
	case *sql.NullInt64:
		if value == nil {
			*d = sql.NullInt64{}
			return
		}
		*d = sql.NullInt64{Int64: value.(int64), Valid: true}
	case *sql.NullString:
		if value == nil {
			*d = sql.NullString{}
			return
		}
		*d = sql.NullString{String: value.(string), Valid: true}
	case *sql.NullTime:
		if value == nil {
			*d = sql.NullTime{}
			return
		}
		*d = sql.NullTime{Time: value.(time.Time), Valid: true}
	default:
		panic("unsupported fake destination")
	}
}
