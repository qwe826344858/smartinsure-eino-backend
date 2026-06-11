package main

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"smartinsure-eino-backend/internal/platform"
)

type fakePlatform struct {
	name    string
	results map[string]map[int][]platform.ProductCard
}

func (f fakePlatform) Name() string   { return f.name }
func (f fakePlatform) Domain() string { return f.name + ".test" }
func (f fakePlatform) Search(_ context.Context, keyword string, page int) ([]platform.ProductCard, error) {
	if f.results == nil || f.results[keyword] == nil {
		return nil, nil
	}
	return f.results[keyword][page], nil
}

func TestParseArgsCapsMaxPerPlatform(t *testing.T) {
	opts, err := parseArgs([]string{"--output", "out.csv", "--max-per-platform", "500"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.MaxPerPlatform != 200 {
		t.Fatalf("MaxPerPlatform = %d, want 200", opts.MaxPerPlatform)
	}
}

func TestSelectedPlatformsDedupesAliases(t *testing.T) {
	platforms, err := selectedPlatforms("xys,xiaoyusan,pa,pingan,hz,huize")
	if err != nil {
		t.Fatal(err)
	}
	if len(platforms) != 3 {
		t.Fatalf("len(platforms) = %d, want 3", len(platforms))
	}
	if platforms[0].key != "xiaoyusan" || platforms[1].key != "pingan" || platforms[2].key != "huize" {
		t.Fatalf("unexpected platform order: %#v", platforms)
	}
}

func TestCollectPlatformDedupeAndLimit(t *testing.T) {
	fp := fakePlatform{
		name: "fake",
		results: map[string]map[int][]platform.ProductCard{
			"医疗": {
				1: {
					{ID: "1", Name: "A", URL: "https://example.com/a", Platform: "fake"},
					{ID: "2", Name: "B", URL: "https://example.com/b", Platform: "fake"},
				},
				2: {
					{ID: "1", Name: "A duplicate", URL: "https://example.com/a", Platform: "fake"},
					{ID: "3", Name: "C", URL: "https://example.com/c", Platform: "fake"},
				},
			},
			"重疾": {
				1: {
					{ID: "4", Name: "D", URL: "https://example.com/d", Platform: "fake"},
				},
			},
		},
	}
	rows, err := collectPlatform(context.Background(), "fake", fp, []string{"医疗", "重疾"}, cliOptions{
		MaxPerPlatform: 3,
		PageLimit:      3,
		RequestTimeout: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	if rows[2].Product.ID != "3" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestWriteCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "products.csv")
	err := writeCSV(path, []exportRow{{
		PlatformKey: "fake",
		Keyword:     "医疗",
		Page:        1,
		Product: platform.ProductCard{
			ID:         "1",
			Name:       "测试产品",
			Company:    "测试公司",
			PriceLabel: "查看详情",
			Tags:       []string{"医疗", "少儿"},
			URL:        "https://example.com/a",
			Platform:   "fake",
			Brief:      "简介",
		},
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1][5] != "测试产品" || records[1][9] != "医疗|少儿" {
		t.Fatalf("unexpected csv records: %#v", records)
	}
}
