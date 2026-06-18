package productdetail

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

const (
	productDetailsSelectColumns = `
	product_key, platform, product_name, canonical_url, price, price_label, detail_json,
	source_hash, prompt_version, model_name, cn_char_count, match_rate,
	status, rag_ingest_status, rag_ingest_source_hash, rag_ingest_error,
	rag_ingest_updated_at, expires_at, created_at, updated_at, last_hit_at`

	productDetailsSelectColumnsWithDetailAlias = `
	d.product_key, d.platform, d.product_name, d.canonical_url, d.price, d.price_label, d.detail_json,
	d.source_hash, d.prompt_version, d.model_name, d.cn_char_count, d.match_rate,
	d.status, d.rag_ingest_status, d.rag_ingest_source_hash, d.rag_ingest_error,
	d.rag_ingest_updated_at, d.expires_at, d.created_at, d.updated_at, d.last_hit_at`

	productDetailSourcesSelectColumnsWithAlias = `
	s.id, s.product_key, s.normalized_url_hash, s.source_url, s.source_type,
	s.source_format, s.raw_payload, s.cleaned_text, s.content_hash, s.cn_char_count,
	s.fetched_at, s.created_at, s.updated_at`

	productDetailsSelectByKeySQL = `
SELECT` + productDetailsSelectColumns + `
FROM product_details
WHERE product_key = ?
LIMIT 1`

	productDetailsSelectByURLHashSQL = `
SELECT` + productDetailsSelectColumnsWithDetailAlias + `
FROM product_detail_aliases a
JOIN product_details d ON d.product_key = a.product_key
WHERE a.normalized_url_hash = ?
LIMIT 1`
)

type MySQLDetailRepository struct {
	db    *sql.DB
	keyer ProductKeyer
	now   func() time.Time
}

type detailSQLStatement struct {
	Query string
	Args  []any
}

type detailRowScanner interface {
	Scan(dest ...any) error
}

func OpenMySQLDetailRepository(dsn string) (*MySQLDetailRepository, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("productdetail: mysql dsn is empty")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	return NewMySQLDetailRepository(db), nil
}

func NewMySQLDetailRepository(db *sql.DB) *MySQLDetailRepository {
	return &MySQLDetailRepository{
		db:    db,
		keyer: NewProductKeyer(),
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (r *MySQLDetailRepository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *MySQLDetailRepository) EnsureSchema(ctx context.Context) error {
	if err := r.ready(); err != nil {
		return err
	}
	for _, stmt := range ProductDetailSchemaStatements() {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := r.ensureProductDetailRAGColumns(ctx); err != nil {
		return err
	}
	if err := r.ensureProductDetailPriceColumns(ctx); err != nil {
		return err
	}
	return nil
}

func (r *MySQLDetailRepository) GetByProductKey(ctx context.Context, productKey string) (*StoredProductDetail, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	productKey = strings.TrimSpace(productKey)
	if productKey == "" {
		return nil, fmt.Errorf("%w: product_key is required", ErrInvalidDetailInput)
	}
	row := r.db.QueryRowContext(ctx, productDetailsSelectByKeySQL, productKey)
	return scanStoredProductDetail(row)
}

func (r *MySQLDetailRepository) GetByURL(ctx context.Context, productURL string) (*StoredProductDetail, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	identity, err := r.keyer.Key(productURL)
	if err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, productDetailsSelectByURLHashSQL, identity.URLHash)
	record, err := scanStoredProductDetail(row)
	if err == nil {
		record.URLHash = identity.URLHash
		return record, nil
	}
	if !errors.Is(err, ErrProductDetailNotFound) {
		return nil, err
	}
	return r.GetByProductKey(ctx, identity.ProductKey)
}

func (r *MySQLDetailRepository) Upsert(ctx context.Context, input UpsertProductDetailInput) error {
	if err := r.ready(); err != nil {
		return err
	}
	record, err := normalizeProductDetailUpsert(input, r.keyer)
	if err != nil {
		return err
	}
	now := r.now()
	detailStmt, err := buildProductDetailUpsertStatement(record, now)
	if err != nil {
		return err
	}
	aliasStmt := buildProductDetailAliasUpsertStatement(record, now)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, detailStmt.Query, detailStmt.Args...); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, aliasStmt.Query, aliasStmt.Args...); err != nil {
		return err
	}
	if sourceStmt, ok, err := buildProductDetailSourceUpsertStatement(record, input.Source, now); err != nil {
		return err
	} else if ok {
		if _, err = tx.ExecContext(ctx, sourceStmt.Query, sourceStmt.Args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *MySQLDetailRepository) UpdatePriceByURL(ctx context.Context, input UpdateProductDetailPriceInput) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	productURL := strings.TrimSpace(input.ProductURL)
	if productURL == "" {
		return false, fmt.Errorf("%w: product_url is required", ErrInvalidDetailInput)
	}
	price := strings.TrimSpace(input.Price)
	priceLabel := strings.TrimSpace(input.PriceLabel)
	if priceLabel == "" && price != "" {
		priceLabel = price
	}
	if price == "" && priceLabel == "" {
		return false, nil
	}
	identity, err := r.keyer.Key(productURL)
	if err != nil {
		return false, err
	}
	stmt := buildProductDetailPriceUpdateStatement(identity.ProductKey, price, priceLabel, r.now())
	result, err := r.db.ExecContext(ctx, stmt.Query, stmt.Args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (r *MySQLDetailRepository) UpdateRAGIngestState(ctx context.Context, input UpdateRAGIngestStateInput) error {
	if err := r.ready(); err != nil {
		return err
	}
	productKey := strings.TrimSpace(input.ProductKey)
	if productKey == "" {
		return fmt.Errorf("%w: product_key is required", ErrInvalidDetailInput)
	}
	updatedAt := input.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = r.now()
	}
	status := NormalizeRAGIngestStatus(input.Status)
	result, err := r.db.ExecContext(ctx, `
UPDATE product_details
SET rag_ingest_status = ?,
    rag_ingest_source_hash = ?,
    rag_ingest_error = ?,
    rag_ingest_updated_at = ?,
    updated_at = ?
WHERE product_key = ?`,
		status,
		strings.TrimSpace(input.SourceHash),
		truncateRAGIngestError(input.Error),
		updatedAt,
		updatedAt,
		productKey,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return ErrProductDetailNotFound
	}
	return nil
}

func (r *MySQLDetailRepository) ListActive(ctx context.Context, params ListProductDetailParams) ([]StoredProductDetailWithSource, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	stmt := buildProductDetailListActiveStatement(params)
	rows, err := r.db.QueryContext(ctx, stmt.Query, stmt.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]StoredProductDetailWithSource, 0, normalizeListActiveLimit(params.Limit))
	for rows.Next() {
		record, err := scanStoredProductDetailWithSource(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, *record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (r *MySQLDetailRepository) TouchHit(ctx context.Context, productKey string) error {
	if err := r.ready(); err != nil {
		return err
	}
	productKey = strings.TrimSpace(productKey)
	if productKey == "" {
		return fmt.Errorf("%w: product_key is required", ErrInvalidDetailInput)
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE product_details
SET last_hit_at = ?
WHERE product_key = ?`, r.now(), productKey)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return ErrProductDetailNotFound
	}
	return nil
}

func (r *MySQLDetailRepository) ready() error {
	if r == nil || r.db == nil {
		return errors.New("productdetail: mysql repository database is nil")
	}
	if r.keyer == (ProductKeyer{}) {
		r.keyer = NewProductKeyer()
	}
	if r.now == nil {
		r.now = func() time.Time { return time.Now().UTC() }
	}
	return nil
}

type productDetailColumnMigration struct {
	Name       string
	Definition string
}

func (r *MySQLDetailRepository) ensureProductDetailRAGColumns(ctx context.Context) error {
	for _, migration := range productDetailRAGColumnMigrations() {
		if err := r.ensureColumn(ctx, "product_details", migration.Name, migration.Definition); err != nil {
			return err
		}
	}
	return nil
}

func productDetailRAGColumnMigrations() []productDetailColumnMigration {
	return []productDetailColumnMigration{
		{Name: "rag_ingest_status", Definition: "VARCHAR(20) NOT NULL DEFAULT 'pending'"},
		{Name: "rag_ingest_source_hash", Definition: "VARCHAR(64) NOT NULL DEFAULT ''"},
		{Name: "rag_ingest_error", Definition: "VARCHAR(512) NOT NULL DEFAULT ''"},
		{Name: "rag_ingest_updated_at", Definition: "DATETIME(3) NULL"},
	}
}

func (r *MySQLDetailRepository) ensureProductDetailPriceColumns(ctx context.Context) error {
	for _, migration := range productDetailPriceColumnMigrations() {
		if err := r.ensureColumn(ctx, "product_details", migration.Name, migration.Definition); err != nil {
			return err
		}
	}
	return nil
}

func productDetailPriceColumnMigrations() []productDetailColumnMigration {
	return []productDetailColumnMigration{
		{Name: "price", Definition: "VARCHAR(64) NOT NULL DEFAULT ''"},
		{Name: "price_label", Definition: "VARCHAR(64) NOT NULL DEFAULT ''"},
	}
}

func (r *MySQLDetailRepository) ensureColumn(ctx context.Context, tableName, columnName, definition string) error {
	var count int
	if err := r.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND TABLE_NAME = ?
  AND COLUMN_NAME = ?`, tableName, columnName).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN `%s` %s", tableName, columnName, definition))
	return err
}

func ProductDetailSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS product_details (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  product_key VARCHAR(128) NOT NULL,
  platform VARCHAR(64) NOT NULL,
  product_name VARCHAR(255) NOT NULL DEFAULT '',
  canonical_url TEXT NOT NULL,
  price VARCHAR(64) NOT NULL DEFAULT '',
  price_label VARCHAR(64) NOT NULL DEFAULT '',
  detail_json JSON NOT NULL,
  source_hash VARCHAR(64) NOT NULL DEFAULT '',
  prompt_version VARCHAR(64) NOT NULL DEFAULT '',
  model_name VARCHAR(128) NOT NULL DEFAULT '',
  cn_char_count INT NOT NULL DEFAULT 0,
  match_rate DOUBLE NOT NULL DEFAULT 0,
  status VARCHAR(20) NOT NULL DEFAULT 'active',
  rag_ingest_status VARCHAR(20) NOT NULL DEFAULT 'pending',
  rag_ingest_source_hash VARCHAR(64) NOT NULL DEFAULT '',
  rag_ingest_error VARCHAR(512) NOT NULL DEFAULT '',
  rag_ingest_updated_at DATETIME(3) NULL,
  expires_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  last_hit_at DATETIME(3) NULL,
  UNIQUE KEY uk_product_details_key (product_key),
  KEY idx_product_details_platform_updated (platform, updated_at),
  KEY idx_product_details_status_expires (status, expires_at),
  KEY idx_product_details_rag_ingest (rag_ingest_status, rag_ingest_updated_at),
  KEY idx_product_details_last_hit (last_hit_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS product_detail_aliases (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  normalized_url_hash VARCHAR(64) NOT NULL,
  normalized_url TEXT NOT NULL,
  product_key VARCHAR(128) NOT NULL,
  platform VARCHAR(64) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  UNIQUE KEY uk_product_detail_aliases_url_hash (normalized_url_hash),
  KEY idx_product_detail_aliases_product_key (product_key),
  CONSTRAINT fk_product_detail_aliases_product_key
    FOREIGN KEY (product_key) REFERENCES product_details(product_key)
    ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS product_detail_sources (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  product_key VARCHAR(128) NOT NULL,
  normalized_url_hash VARCHAR(64) NOT NULL DEFAULT '',
  source_url TEXT NOT NULL,
  source_type VARCHAR(32) NOT NULL DEFAULT 'web_page',
  source_format VARCHAR(32) NOT NULL DEFAULT 'text',
  raw_payload MEDIUMTEXT NULL,
  cleaned_text MEDIUMTEXT NOT NULL,
  content_hash VARCHAR(64) NOT NULL,
  cn_char_count INT NOT NULL DEFAULT 0,
  fetched_at DATETIME(3) NOT NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  UNIQUE KEY uk_product_detail_sources_key_hash (product_key, content_hash),
  KEY idx_product_detail_sources_url_hash (normalized_url_hash),
  KEY idx_product_detail_sources_fetched (fetched_at),
  CONSTRAINT fk_product_detail_sources_product_key
    FOREIGN KEY (product_key) REFERENCES product_details(product_key)
    ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	}
}

func normalizeProductDetailUpsert(input UpsertProductDetailInput, keyer ProductKeyer) (StoredProductDetail, error) {
	rawURL := strings.TrimSpace(input.CanonicalURL)
	if rawURL == "" {
		rawURL = strings.TrimSpace(input.Detail.ProductURL)
	}
	identity, err := keyer.Key(rawURL)
	if err != nil {
		return StoredProductDetail{}, err
	}

	productKey := strings.TrimSpace(input.ProductKey)
	if productKey == "" {
		productKey = identity.ProductKey
	}
	platform := strings.TrimSpace(strings.ToLower(input.Platform))
	if platform == "" {
		platform = identity.Platform
	}
	if platform == "" {
		platform = ProductPlatformUnknown
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = DetailStatusActive
	}
	ragStatus := NormalizeRAGIngestStatus(input.RAGIngestStatus)

	detail := input.Detail
	detail.ProductURL = identity.NormalizedURL
	detail.Platform = platform
	detail.Price = strings.TrimSpace(detail.Price)
	detail.PriceLabel = strings.TrimSpace(detail.PriceLabel)
	if detail.PriceLabel == "" && detail.Price != "" {
		detail.PriceLabel = detail.Price
	}
	productName := strings.TrimSpace(detail.ProductName)

	urlHash := strings.TrimSpace(input.NormalizedURLHash)
	if urlHash == "" {
		urlHash = identity.URLHash
	}
	return StoredProductDetail{
		ProductKey:          productKey,
		Platform:            platform,
		ProductName:         productName,
		CanonicalURL:        identity.NormalizedURL,
		URLHash:             urlHash,
		Price:               detail.Price,
		PriceLabel:          detail.PriceLabel,
		Detail:              detail,
		SourceHash:          strings.TrimSpace(input.SourceHash),
		PromptVersion:       strings.TrimSpace(input.PromptVersion),
		ModelName:           strings.TrimSpace(input.ModelName),
		CNCharCount:         detail.CNCharCount,
		MatchRate:           detail.MatchRate,
		Status:              status,
		RAGIngestStatus:     ragStatus,
		RAGIngestSourceHash: strings.TrimSpace(input.RAGIngestSourceHash),
		RAGIngestError:      truncateRAGIngestError(input.RAGIngestError),
		RAGIngestUpdatedAt:  input.RAGIngestUpdatedAt,
		ExpiresAt:           input.ExpiresAt,
	}, nil
}

func buildProductDetailUpsertStatement(record StoredProductDetail, now time.Time) (detailSQLStatement, error) {
	detailJSON, err := json.Marshal(record.Detail)
	if err != nil {
		return detailSQLStatement{}, err
	}
	return detailSQLStatement{
		Query: `
INSERT INTO product_details (
	product_key, platform, product_name, canonical_url, price, price_label, detail_json,
	source_hash, prompt_version, model_name, cn_char_count, match_rate,
	status, rag_ingest_status, rag_ingest_source_hash, rag_ingest_error,
	rag_ingest_updated_at, expires_at, created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
	platform = VALUES(platform),
	product_name = VALUES(product_name),
	canonical_url = VALUES(canonical_url),
	price = VALUES(price),
	price_label = VALUES(price_label),
	detail_json = VALUES(detail_json),
	source_hash = VALUES(source_hash),
	prompt_version = VALUES(prompt_version),
	model_name = VALUES(model_name),
	cn_char_count = VALUES(cn_char_count),
	match_rate = VALUES(match_rate),
	status = VALUES(status),
	rag_ingest_status = VALUES(rag_ingest_status),
	rag_ingest_source_hash = VALUES(rag_ingest_source_hash),
	rag_ingest_error = VALUES(rag_ingest_error),
	rag_ingest_updated_at = VALUES(rag_ingest_updated_at),
	expires_at = VALUES(expires_at),
	updated_at = VALUES(updated_at)`,
		Args: []any{
			record.ProductKey,
			record.Platform,
			record.ProductName,
			record.CanonicalURL,
			record.Price,
			record.PriceLabel,
			string(detailJSON),
			record.SourceHash,
			record.PromptVersion,
			record.ModelName,
			record.CNCharCount,
			record.MatchRate,
			record.Status,
			record.RAGIngestStatus,
			record.RAGIngestSourceHash,
			record.RAGIngestError,
			nullTimePtr(record.RAGIngestUpdatedAt),
			nullTimePtr(record.ExpiresAt),
			now,
			now,
		},
	}, nil
}

func truncateRAGIngestError(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= 512 {
		return value
	}
	return string([]rune(value)[:512])
}

func buildProductDetailAliasUpsertStatement(record StoredProductDetail, now time.Time) detailSQLStatement {
	return detailSQLStatement{
		Query: `
INSERT INTO product_detail_aliases (
	normalized_url_hash, normalized_url, product_key, platform, created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
	normalized_url = VALUES(normalized_url),
	product_key = VALUES(product_key),
	platform = VALUES(platform),
	updated_at = VALUES(updated_at)`,
		Args: []any{
			record.URLHash,
			record.CanonicalURL,
			record.ProductKey,
			record.Platform,
			now,
			now,
		},
	}
}

func buildProductDetailSourceUpsertStatement(record StoredProductDetail, source *UpsertProductDetailSourceInput, now time.Time) (detailSQLStatement, bool, error) {
	if source == nil {
		return detailSQLStatement{}, false, nil
	}
	normalized := normalizeProductDetailSourceUpsert(record, *source, now)
	if strings.TrimSpace(normalized.CleanedText) == "" {
		return detailSQLStatement{}, false, nil
	}
	return detailSQLStatement{
		Query: `
INSERT INTO product_detail_sources (
	product_key, normalized_url_hash, source_url, source_type, source_format,
	raw_payload, cleaned_text, content_hash, cn_char_count, fetched_at, created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
	normalized_url_hash = VALUES(normalized_url_hash),
	source_url = VALUES(source_url),
	source_type = VALUES(source_type),
	source_format = VALUES(source_format),
	raw_payload = VALUES(raw_payload),
	cleaned_text = VALUES(cleaned_text),
	cn_char_count = VALUES(cn_char_count),
	fetched_at = VALUES(fetched_at),
	updated_at = VALUES(updated_at)`,
		Args: []any{
			normalized.ProductKey,
			normalized.NormalizedURLHash,
			normalized.SourceURL,
			normalized.SourceType,
			normalized.SourceFormat,
			nullableStringPtr(normalized.RawPayload),
			normalized.CleanedText,
			normalized.ContentHash,
			normalized.CNCharCount,
			normalized.FetchedAt,
			now,
			now,
		},
	}, true, nil
}

func buildProductDetailPriceUpdateStatement(productKey, price, priceLabel string, now time.Time) detailSQLStatement {
	return detailSQLStatement{
		Query: `
UPDATE product_details
SET price = ?,
    price_label = ?,
    detail_json = JSON_SET(detail_json, '$.price', ?, '$.price_label', ?),
    updated_at = ?
WHERE product_key = ?`,
		Args: []any{
			strings.TrimSpace(price),
			strings.TrimSpace(priceLabel),
			strings.TrimSpace(price),
			strings.TrimSpace(priceLabel),
			now,
			strings.TrimSpace(productKey),
		},
	}
}

func normalizeProductDetailSourceUpsert(record StoredProductDetail, source UpsertProductDetailSourceInput, now time.Time) ProductDetailSource {
	productKey := strings.TrimSpace(source.ProductKey)
	if productKey == "" {
		productKey = record.ProductKey
	}
	urlHash := strings.TrimSpace(source.NormalizedURLHash)
	if urlHash == "" {
		urlHash = record.URLHash
	}
	sourceURL := strings.TrimSpace(source.SourceURL)
	if sourceURL == "" {
		sourceURL = record.CanonicalURL
	}
	sourceType := strings.TrimSpace(strings.ToLower(source.SourceType))
	if sourceType == "" {
		sourceType = "web_page"
	}
	sourceFormat := strings.TrimSpace(strings.ToLower(source.SourceFormat))
	if sourceFormat == "" {
		sourceFormat = "text"
	}
	cleanedText := strings.TrimSpace(source.CleanedText)
	contentHash := strings.TrimSpace(source.ContentHash)
	if contentHash == "" {
		contentHash = SHA256Hex(cleanedText)
	}
	fetchedAt := source.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = now
	}
	cnCount := source.CNCharCount
	if cnCount <= 0 {
		cnCount = record.CNCharCount
	}
	return ProductDetailSource{
		ProductKey:        productKey,
		NormalizedURLHash: urlHash,
		SourceURL:         sourceURL,
		SourceType:        sourceType,
		SourceFormat:      sourceFormat,
		RawPayload:        source.RawPayload,
		CleanedText:       cleanedText,
		ContentHash:       contentHash,
		CNCharCount:       cnCount,
		FetchedAt:         fetchedAt,
	}
}

func buildProductDetailListActiveStatement(params ListProductDetailParams) detailSQLStatement {
	limit := normalizeListActiveLimit(params.Limit)
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	query := `
SELECT` + productDetailsSelectColumnsWithDetailAlias + `,` + productDetailSourcesSelectColumnsWithAlias + `
FROM product_details d
LEFT JOIN product_detail_sources s
  ON s.product_key = d.product_key
 AND s.content_hash = d.source_hash
WHERE d.status = ? AND (d.expires_at IS NULL OR d.expires_at > ?)`
	args := []any{DetailStatusActive, now}

	if params.MinMatchRate > 0 {
		query += "\n  AND d.match_rate >= ?"
		args = append(args, params.MinMatchRate)
	}
	if platform := strings.TrimSpace(strings.ToLower(params.Platform)); platform != "" {
		query += "\n  AND d.platform = ?"
		args = append(args, platform)
	}
	if promptVersion := strings.TrimSpace(params.PromptVersion); promptVersion != "" {
		query += "\n  AND d.prompt_version = ?"
		args = append(args, promptVersion)
	}
	if params.AfterUpdatedAt != nil && !params.AfterUpdatedAt.IsZero() {
		query += "\n  AND (d.updated_at > ? OR (d.updated_at = ? AND d.product_key > ?))"
		args = append(args, *params.AfterUpdatedAt, *params.AfterUpdatedAt, strings.TrimSpace(params.AfterProductKey))
	}
	if params.RequireSource {
		query += "\n  AND s.id IS NOT NULL"
	}
	query += "\nORDER BY d.updated_at ASC, d.product_key ASC\nLIMIT ?"
	args = append(args, limit)
	return detailSQLStatement{Query: query, Args: args}
}

func normalizeListActiveLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func scanStoredProductDetail(row detailRowScanner) (*StoredProductDetail, error) {
	var record StoredProductDetail
	var detailJSON []byte
	var ragIngestUpdatedAt, expiresAt, lastHitAt sql.NullTime
	err := row.Scan(
		&record.ProductKey,
		&record.Platform,
		&record.ProductName,
		&record.CanonicalURL,
		&record.Price,
		&record.PriceLabel,
		&detailJSON,
		&record.SourceHash,
		&record.PromptVersion,
		&record.ModelName,
		&record.CNCharCount,
		&record.MatchRate,
		&record.Status,
		&record.RAGIngestStatus,
		&record.RAGIngestSourceHash,
		&record.RAGIngestError,
		&ragIngestUpdatedAt,
		&expiresAt,
		&record.CreatedAt,
		&record.UpdatedAt,
		&lastHitAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProductDetailNotFound
		}
		return nil, err
	}
	if err := fillStoredProductDetail(&record, detailJSON, ragIngestUpdatedAt, expiresAt, lastHitAt); err != nil {
		return nil, err
	}
	return &record, nil
}

func scanStoredProductDetailWithSource(row detailRowScanner) (*StoredProductDetailWithSource, error) {
	var record StoredProductDetail
	var detailJSON []byte
	var ragIngestUpdatedAt, expiresAt, lastHitAt sql.NullTime
	source := nullableProductDetailSource{}
	err := row.Scan(
		&record.ProductKey,
		&record.Platform,
		&record.ProductName,
		&record.CanonicalURL,
		&record.Price,
		&record.PriceLabel,
		&detailJSON,
		&record.SourceHash,
		&record.PromptVersion,
		&record.ModelName,
		&record.CNCharCount,
		&record.MatchRate,
		&record.Status,
		&record.RAGIngestStatus,
		&record.RAGIngestSourceHash,
		&record.RAGIngestError,
		&ragIngestUpdatedAt,
		&expiresAt,
		&record.CreatedAt,
		&record.UpdatedAt,
		&lastHitAt,
		&source.ID,
		&source.ProductKey,
		&source.NormalizedURLHash,
		&source.SourceURL,
		&source.SourceType,
		&source.SourceFormat,
		&source.RawPayload,
		&source.CleanedText,
		&source.ContentHash,
		&source.CNCharCount,
		&source.FetchedAt,
		&source.CreatedAt,
		&source.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProductDetailNotFound
		}
		return nil, err
	}
	if err := fillStoredProductDetail(&record, detailJSON, ragIngestUpdatedAt, expiresAt, lastHitAt); err != nil {
		return nil, err
	}
	return &StoredProductDetailWithSource{
		Detail: record,
		Source: source.productDetailSource(),
	}, nil
}

type nullableProductDetailSource struct {
	ID                sql.NullInt64
	ProductKey        sql.NullString
	NormalizedURLHash sql.NullString
	SourceURL         sql.NullString
	SourceType        sql.NullString
	SourceFormat      sql.NullString
	RawPayload        sql.NullString
	CleanedText       sql.NullString
	ContentHash       sql.NullString
	CNCharCount       sql.NullInt64
	FetchedAt         sql.NullTime
	CreatedAt         sql.NullTime
	UpdatedAt         sql.NullTime
}

func (s nullableProductDetailSource) productDetailSource() *ProductDetailSource {
	if !s.ID.Valid {
		return nil
	}
	source := &ProductDetailSource{
		ID:                s.ID.Int64,
		ProductKey:        s.ProductKey.String,
		NormalizedURLHash: s.NormalizedURLHash.String,
		SourceURL:         s.SourceURL.String,
		SourceType:        s.SourceType.String,
		SourceFormat:      s.SourceFormat.String,
		CleanedText:       s.CleanedText.String,
		ContentHash:       s.ContentHash.String,
		CNCharCount:       int(s.CNCharCount.Int64),
		FetchedAt:         s.FetchedAt.Time,
		CreatedAt:         s.CreatedAt.Time,
		UpdatedAt:         s.UpdatedAt.Time,
	}
	if s.RawPayload.Valid {
		raw := s.RawPayload.String
		source.RawPayload = &raw
	}
	return source
}

func fillStoredProductDetail(record *StoredProductDetail, detailJSON []byte, ragIngestUpdatedAt, expiresAt, lastHitAt sql.NullTime) error {
	if err := json.Unmarshal(detailJSON, &record.Detail); err != nil {
		return fmt.Errorf("productdetail: decode detail_json: %w", err)
	}
	if record.Detail.ProductName == "" {
		record.Detail.ProductName = record.ProductName
	}
	if record.Detail.ProductURL == "" {
		record.Detail.ProductURL = record.CanonicalURL
	}
	if record.Detail.Platform == "" {
		record.Detail.Platform = record.Platform
	}
	record.Price = strings.TrimSpace(record.Price)
	record.PriceLabel = strings.TrimSpace(record.PriceLabel)
	if record.PriceLabel == "" && record.Price != "" {
		record.PriceLabel = record.Price
	}
	if record.Detail.Price == "" {
		record.Detail.Price = record.Price
	}
	if record.Detail.PriceLabel == "" {
		record.Detail.PriceLabel = record.PriceLabel
	}
	if record.Price == "" {
		record.Price = record.Detail.Price
	}
	if record.PriceLabel == "" {
		record.PriceLabel = record.Detail.PriceLabel
	}
	if record.Detail.CNCharCount == 0 {
		record.Detail.CNCharCount = record.CNCharCount
	}
	if record.Detail.MatchRate == 0 {
		record.Detail.MatchRate = record.MatchRate
	}
	record.RAGIngestStatus = NormalizeRAGIngestStatus(record.RAGIngestStatus)
	if ragIngestUpdatedAt.Valid {
		record.RAGIngestUpdatedAt = &ragIngestUpdatedAt.Time
	}
	if expiresAt.Valid {
		record.ExpiresAt = &expiresAt.Time
	}
	if lastHitAt.Valid {
		record.LastHitAt = &lastHitAt.Time
	}
	return nil
}

func nullTimePtr(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return *value
}

func nullableStringPtr(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
