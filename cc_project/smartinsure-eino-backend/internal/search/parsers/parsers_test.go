package parsers

import "testing"

func TestParsePriceFromText(t *testing.T) {
	cases := map[string]string{
		"年交 258元/年 起": "258元/年",
		"保费 ¥99.5":    "¥99.5",
		"最低 66元起":     "66元起",
		"首月 0 元":      "首月0元",
	}
	for input, want := range cases {
		if got := ParsePriceFromText(input); got != want {
			t.Fatalf("ParsePriceFromText(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestParseXiaoyusanStaticData(t *testing.T) {
	html := `<script>
	window.staticData = {
		"insuranceConfig":{"title":"蓝医保百万医疗险","productname":"备用"},
		"companyConfig":{"companyname":"太平洋健康"},
		"versions":{"list":[{"duty":"一般医疗","coverage":"200万"},{"duty":"重疾医疗","coverage":"400万"}]}
	};
	</script>`

	got := ParseXiaoyusan(html)
	if got.Name != "蓝医保百万医疗险" {
		t.Fatalf("name=%q", got.Name)
	}
	if got.Company != "太平洋健康" {
		t.Fatalf("company=%q", got.Company)
	}
	if got.Brief != "一般医疗200万，重疾医疗400万" {
		t.Fatalf("brief=%q", got.Brief)
	}
	if len(got.Tags) == 0 || got.Tags[0] != "百万医疗" {
		t.Fatalf("tags=%#v", got.Tags)
	}
}

func TestGetParserFallbackMeta(t *testing.T) {
	parser := GetParser("https://unknown.example/p")
	got := parser(`<html><head><title>产品A</title><meta name="description" content="保证续保，首年99元起"></head></html>`)
	if got.Name != "产品A" || got.Price != "99元起" {
		t.Fatalf("got=%#v", got)
	}
}
