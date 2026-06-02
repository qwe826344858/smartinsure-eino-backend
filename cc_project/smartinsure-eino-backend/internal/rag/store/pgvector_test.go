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
