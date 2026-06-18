package productdetail

import "testing"

func TestHeuristicExtractSetsPriceFields(t *testing.T) {
	detail := HeuristicExtract(`
测试百万医疗险
保费
323元/年起
保障责任：一般医疗保险金 300万
`, "https://www.huize.com/product/1", "测试百万医疗险", 80)

	if detail.Price != "323元/年起" || detail.PriceLabel != "323元/年起" {
		t.Fatalf("price = %q/%q, want 323元/年起", detail.Price, detail.PriceLabel)
	}
}
