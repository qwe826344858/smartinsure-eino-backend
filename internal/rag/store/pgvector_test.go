package store

import (
	"math"
	"strings"
	"testing"
)

func TestVectorLiteral(t *testing.T) {
	got, err := VectorLiteral([]float64{1, 0.125, -3.5, 123456789.123456})
	if err != nil {
		t.Fatal(err)
	}
	if got != "[1,0.125,-3.5,123456789.123]" {
		t.Fatalf("VectorLiteral()=%q", got)
	}

	if _, err := VectorLiteral([]float64{math.NaN()}); err == nil {
		t.Fatal("expected non-finite vector error")
	}
}

func TestBuildDocumentUpsert(t *testing.T) {
	stmt, err := BuildDocumentUpsert(DocumentInput{
		Namespace:   "ns",
		SourceType:  "web_page",
		SourceURL:   "https://example.com/a",
		Title:       "标题",
		RawHTML:     "<html></html>",
		CleanedText: "清洗文本",
		Metadata:    map[string]any{"cn_count": 4, "title": "标题"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stmt.Query, "ON CONFLICT (namespace, source_url)") {
		t.Fatalf("missing conflict clause: %s", stmt.Query)
	}
	if len(stmt.Args) != 8 {
		t.Fatalf("len(args)=%d", len(stmt.Args))
	}
	if stmt.Args[6] != SHA256Hex("清洗文本") {
		t.Fatalf("unexpected content hash: %v", stmt.Args[6])
	}
	if !strings.Contains(stmt.Args[7].(string), `"cn_count":4`) {
		t.Fatalf("metadata not encoded: %v", stmt.Args[7])
	}
}

func TestBuildChunkInsert(t *testing.T) {
	stmt, err := BuildChunkInsert(42, ChunkRecord{
		ChunkIndex: 3,
		Content:    "chunk 内容",
		Embedding:  []float64{0.1, 0.2},
		Metadata:   map[string]any{"source_url": "https://example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stmt.Query, "$5::vector") {
		t.Fatalf("missing vector cast: %s", stmt.Query)
	}
	if len(stmt.Args) != 6 {
		t.Fatalf("len(args)=%d", len(stmt.Args))
	}
	if stmt.Args[0] != int64(42) || stmt.Args[1] != 3 || stmt.Args[4] != "[0.1,0.2]" {
		t.Fatalf("unexpected args: %+v", stmt.Args)
	}
}

func TestValidateDocumentInput(t *testing.T) {
	err := ValidateDocumentInput(DocumentInput{
		Namespace:   "ns",
		SourceType:  "web_page",
		SourceURL:   "https://example.com",
		CleanedText: "正文",
		Chunks: []ChunkRecord{{
			ChunkIndex: 0,
			Content:    "正文",
			Embedding:  []float64{1},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := ValidateDocumentInput(DocumentInput{Namespace: "ns"}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestBuildSearchSimilarChunks(t *testing.T) {
	stmt, err := BuildSearchSimilarChunks(SearchQuery{
		Namespace: "product_details",
		Vector:    []float64{0.1, 0.2, 0.3},
		TopK:      8,
		Filters: map[string]any{
			"chunk_type":  "product_duty",
			"product_key": "huize:104006:108504",
		},
	})
	if err != nil {
		t.Fatalf("BuildSearchSimilarChunks error = %v", err)
	}
	for _, want := range []string{
		"c.embedding <=> $2::vector AS distance",
		"1 - (c.embedding <=> $2::vector) AS score",
		"WHERE d.namespace = $1",
		"c.metadata @> $4::jsonb",
		"ORDER BY c.embedding <=> $2::vector",
	} {
		if !strings.Contains(stmt.Query, want) {
			t.Fatalf("search SQL missing %q:\n%s", want, stmt.Query)
		}
	}
	if len(stmt.Args) != 4 {
		t.Fatalf("len(args) = %d, want 4", len(stmt.Args))
	}
	if stmt.Args[0] != "product_details" || stmt.Args[1] != "[0.1,0.2,0.3]" || stmt.Args[2] != 8 {
		t.Fatalf("unexpected args: %#v", stmt.Args)
	}
	filterJSON, ok := stmt.Args[3].(string)
	if !ok || !strings.Contains(filterJSON, `"chunk_type":"product_duty"`) || !strings.Contains(filterJSON, `"product_key":"huize:104006:108504"`) {
		t.Fatalf("filters not encoded as JSONB arg: %#v", stmt.Args[3])
	}
}

func TestBuildSearchSimilarChunksDefaultsAndValidation(t *testing.T) {
	stmt, err := BuildSearchSimilarChunks(SearchQuery{
		Namespace: "product_details",
		Vector:    []float64{1},
	})
	if err != nil {
		t.Fatalf("BuildSearchSimilarChunks default error = %v", err)
	}
	if stmt.Args[2] != 5 || stmt.Args[3] != "{}" {
		t.Fatalf("default topK/filter args = %#v", stmt.Args[2:])
	}
	if _, err := BuildSearchSimilarChunks(SearchQuery{Namespace: "product_details"}); err == nil {
		t.Fatal("expected empty vector error")
	}
	if _, err := BuildSearchSimilarChunks(SearchQuery{Namespace: "", Vector: []float64{1}}); err == nil {
		t.Fatal("expected empty namespace error")
	}
	if _, err := BuildSearchSimilarChunks(SearchQuery{Namespace: "product_details", Vector: []float64{1}, TopK: -1}); err == nil {
		t.Fatal("expected negative topK error")
	}
}
