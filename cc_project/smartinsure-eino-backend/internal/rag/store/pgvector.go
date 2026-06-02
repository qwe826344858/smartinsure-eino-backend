package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const deleteChunksSQL = "DELETE FROM rag_chunks WHERE document_id = $1;"

type SQLStatement struct {
	Query string
	Args  []any
}

type PgVectorStore struct {
	DB *sql.DB
}

func NewPgVectorStore(db *sql.DB) *PgVectorStore {
	return &PgVectorStore{DB: db}
}

func SchemaStatements() []string {
	return []string{
		"CREATE EXTENSION IF NOT EXISTS vector;",
		`CREATE TABLE IF NOT EXISTS rag_documents (
			id BIGSERIAL PRIMARY KEY,
			namespace TEXT NOT NULL,
			source_type TEXT NOT NULL,
			source_url TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			raw_html TEXT NOT NULL DEFAULT '',
			cleaned_text TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
			fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(namespace, source_url)
		);`,
		`CREATE TABLE IF NOT EXISTS rag_chunks (
			id BIGSERIAL PRIMARY KEY,
			document_id BIGINT NOT NULL REFERENCES rag_documents(id) ON DELETE CASCADE,
			chunk_index INTEGER NOT NULL,
			content TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			embedding vector NOT NULL,
			metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(document_id, chunk_index)
		);`,
		"CREATE INDEX IF NOT EXISTS idx_rag_documents_namespace ON rag_documents(namespace);",
		"CREATE INDEX IF NOT EXISTS idx_rag_chunks_document_id ON rag_chunks(document_id);",
	}
}

func (s *PgVectorStore) EnsureSchema(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return errors.New("pgvector store database is nil")
	}
	for _, statement := range SchemaStatements() {
		if _, err := s.DB.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *PgVectorStore) UpsertDocumentWithChunks(ctx context.Context, input DocumentInput) (docID int64, err error) {
	if s == nil || s.DB == nil {
		return 0, errors.New("pgvector store database is nil")
	}
	if err := ValidateDocumentInput(input); err != nil {
		return 0, err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	docStatement, err := BuildDocumentUpsert(input)
	if err != nil {
		return 0, err
	}
	if err = tx.QueryRowContext(ctx, docStatement.Query, docStatement.Args...).Scan(&docID); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, deleteChunksSQL, docID); err != nil {
		return 0, err
	}

	for _, chunk := range input.Chunks {
		chunkStatement, buildErr := BuildChunkInsert(docID, chunk)
		if buildErr != nil {
			return 0, buildErr
		}
		if _, err = tx.ExecContext(ctx, chunkStatement.Query, chunkStatement.Args...); err != nil {
			return 0, err
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return docID, nil
}

func ValidateDocumentInput(input DocumentInput) error {
	if strings.TrimSpace(input.Namespace) == "" {
		return errors.New("namespace is empty")
	}
	if strings.TrimSpace(input.SourceType) == "" {
		return errors.New("source_type is empty")
	}
	if strings.TrimSpace(input.SourceURL) == "" {
		return errors.New("source_url is empty")
	}
	if strings.TrimSpace(input.CleanedText) == "" {
		return errors.New("cleaned_text is empty")
	}
	if len(input.Chunks) == 0 {
		return errors.New("chunks must not be empty")
	}
	for i, chunk := range input.Chunks {
		if strings.TrimSpace(chunk.Content) == "" {
			return fmt.Errorf("chunk %d content is empty", i)
		}
		if len(chunk.Embedding) == 0 {
			return fmt.Errorf("chunk %d embedding is empty", i)
		}
	}
	return nil
}

func BuildDocumentUpsert(input DocumentInput) (SQLStatement, error) {
	metadata, err := metadataJSON(input.Metadata)
	if err != nil {
		return SQLStatement{}, err
	}
	query := `
INSERT INTO rag_documents (
	namespace, source_type, source_url, title,
	raw_html, cleaned_text, content_hash, metadata
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
ON CONFLICT (namespace, source_url)
DO UPDATE SET
	title = EXCLUDED.title,
	raw_html = EXCLUDED.raw_html,
	cleaned_text = EXCLUDED.cleaned_text,
	content_hash = EXCLUDED.content_hash,
	metadata = EXCLUDED.metadata,
	updated_at = NOW()
RETURNING id;`
	return SQLStatement{
		Query: query,
		Args: []any{
			input.Namespace,
			input.SourceType,
			input.SourceURL,
			input.Title,
			input.RawHTML,
			input.CleanedText,
			SHA256Hex(input.CleanedText),
			metadata,
		},
	}, nil
}

func BuildChunkInsert(documentID int64, chunk ChunkRecord) (SQLStatement, error) {
	if documentID <= 0 {
		return SQLStatement{}, errors.New("document_id must be greater than 0")
	}
	if strings.TrimSpace(chunk.Content) == "" {
		return SQLStatement{}, errors.New("chunk content is empty")
	}
	vector, err := VectorLiteral(chunk.Embedding)
	if err != nil {
		return SQLStatement{}, err
	}
	metadata, err := metadataJSON(chunk.Metadata)
	if err != nil {
		return SQLStatement{}, err
	}
	query := `
INSERT INTO rag_chunks (
	document_id, chunk_index, content, content_hash, embedding, metadata
)
VALUES ($1, $2, $3, $4, $5::vector, $6::jsonb);`
	return SQLStatement{
		Query: query,
		Args: []any{
			documentID,
			chunk.ChunkIndex,
			chunk.Content,
			SHA256Hex(chunk.Content),
			vector,
			metadata,
		},
	}, nil
}

func VectorLiteral(values []float64) (string, error) {
	if len(values) == 0 {
		return "", errors.New("vector must not be empty")
	}
	parts := make([]string, len(values))
	for i, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", fmt.Errorf("vector contains non-finite value at index %d", i)
		}
		parts[i] = strconv.FormatFloat(value, 'g', 12, 64)
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}

func SHA256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func metadataJSON(metadata map[string]any) (string, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(metadata); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
