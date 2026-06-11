package main

import (
	"testing"
	"time"

	"smartinsure-eino-backend/internal/schema"
	skillproduct "smartinsure-eino-backend/internal/skill/productdetail"
)

func TestParseArgs(t *testing.T) {
	opts, err := parseArgs([]string{
		"--limit", "20",
		"--namespace", "product_details",
		"--platform", "huize",
		"--prompt-version", "detail-v1",
		"--min-match-rate", "0.7",
		"--max-tag-chunks", "4",
		"--source-chunk-size", "800",
		"--source-chunk-overlap", "120",
		"--require-source",
		"--ensure-schema=false",
	})
	if err != nil {
		t.Fatalf("parseArgs error = %v", err)
	}
	if opts.Limit != 20 || opts.Namespace != "product_details" || opts.Platform != "huize" {
		t.Fatalf("unexpected opts: %#v", opts)
	}
	if opts.MinMatchRate != 0.7 || opts.MaxTagChunks != 4 || opts.SourceChunkSize != 800 || opts.SourceChunkOverlap != 120 {
		t.Fatalf("unexpected numeric opts: %#v", opts)
	}
	if !opts.RequireSource || opts.EnsureSchema {
		t.Fatalf("unexpected bool opts: %#v", opts)
	}
}

func TestDetailInputFromStoredMapsSource(t *testing.T) {
	expiresAt := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	fetchedAt := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	record := skillproduct.StoredProductDetailWithSource{
		Detail: skillproduct.StoredProductDetail{
			ProductKey:    "huize:104006:108504",
			Platform:      "huize",
			ProductName:   "测试医疗险",
			CanonicalURL:  "https://example.com/product",
			URLHash:       "url-hash",
			SourceHash:    "source-hash",
			PromptVersion: "detail-v1",
			ModelName:     "extract-model",
			Status:        skillproduct.DetailStatusActive,
			ExpiresAt:     &expiresAt,
			Detail: schema.ProductDetail{
				ProductName: "测试医疗险",
				ProductURL:  "https://example.com/product",
				Platform:    "huize",
				Duties: []schema.DutyItem{{
					Name:        "一般住院医疗保险金",
					Coverage:    "300万",
					Description: "住院医疗费用",
				}},
				CNCharCount: 1000,
				MatchRate:   0.9,
			},
		},
		Source: &skillproduct.ProductDetailSource{
			SourceURL:         "https://example.com/product",
			SourceType:        "web_page",
			SourceFormat:      "html",
			CleanedText:       "产品原文",
			ContentHash:       "source-hash",
			CNCharCount:       1000,
			FetchedAt:         fetchedAt,
			NormalizedURLHash: "source-url-hash",
		},
	}

	input := detailInputFromStored(record, "product_details")
	if input.ProductKey != record.Detail.ProductKey || input.NormalizedURLHash != "url-hash" {
		t.Fatalf("unexpected identity fields: %#v", input)
	}
	if len(input.Sources) != 1 {
		t.Fatalf("len(Sources) = %d, want 1", len(input.Sources))
	}
	if input.Sources[0].OriginSourceType != "web_page" || input.Sources[0].ContentHash != "source-hash" {
		t.Fatalf("unexpected source: %#v", input.Sources[0])
	}
	if input.ExpiresAt == nil || !input.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %#v, want %v", input.ExpiresAt, expiresAt)
	}
}
