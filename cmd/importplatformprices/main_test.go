package main

import (
	"context"
	"strings"
	"testing"

	skillproduct "smartinsure-eino-backend/internal/skill/productdetail"
)

func TestReadPriceRowsFromCSV(t *testing.T) {
	rows, err := readPriceRows(strings.NewReader("\ufeffplatform_key,platform,keyword,page,id,name,company,price,price_label,tags,url,brief\nhuize,慧择,医疗,1,hz_1,测试医疗险,测试保险,323元/年起,323元/年起,医疗险,https://example.com/p,brief\nhuize,慧择,医疗,1,hz_2,无价格医疗险,测试保险,,,医疗险,https://example.com/skip,brief\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1: %#v", len(rows), rows)
	}
	row := rows[0]
	if row.ProductURL != "https://example.com/p" || row.Price != "323元/年起" || row.PriceLabel != "323元/年起" || row.ProductName != "测试医疗险" {
		t.Fatalf("row = %#v", row)
	}
}

func TestImportRowsUpdatesByURL(t *testing.T) {
	updater := &fakePriceUpdater{}
	stats := importRows(context.Background(), updater, []priceRow{
		{ProductURL: "https://example.com/p", Price: "323元/年起", PriceLabel: "323元/年起"},
		{ProductURL: "https://example.com/miss", Price: "88元/年起", PriceLabel: "88元/年起"},
	}, 0)

	if stats.Total != 2 || stats.Updated != 1 || stats.Missed != 1 || stats.Failed != 0 {
		t.Fatalf("stats = %#v", stats)
	}
	if len(updater.inputs) != 2 {
		t.Fatalf("inputs = %#v", updater.inputs)
	}
	if updater.inputs[0].ProductURL != "https://example.com/p" || updater.inputs[0].Price != "323元/年起" {
		t.Fatalf("first input = %#v", updater.inputs[0])
	}
}

type fakePriceUpdater struct {
	inputs []skillproduct.UpdateProductDetailPriceInput
}

func (u *fakePriceUpdater) UpdatePriceByURL(_ context.Context, input skillproduct.UpdateProductDetailPriceInput) (bool, error) {
	u.inputs = append(u.inputs, input)
	return !strings.Contains(input.ProductURL, "miss"), nil
}
