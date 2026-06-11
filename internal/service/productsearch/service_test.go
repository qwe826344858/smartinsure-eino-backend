package productsearch

import (
	"context"
	"reflect"
	"testing"

	"smartinsure-eino-backend/internal/platform"
)

type fakePlatform struct {
	name    string
	results map[string][]platform.ProductCard
}

func (f fakePlatform) Name() string   { return f.name }
func (f fakePlatform) Domain() string { return f.name + ".test" }
func (f fakePlatform) Search(ctx context.Context, keyword string, page int) ([]platform.ProductCard, error) {
	return f.results[keyword], nil
}

func TestBudgetParsing(t *testing.T) {
	cases := map[string]float64{
		"预算1000元":     1000,
		"一年1万块":       10000,
		"不超过500块":     500,
		"年预算 3000 左右": 3000,
	}
	for input, want := range cases {
		got, ok := ExtractBudget(input)
		if !ok || got != want {
			t.Fatalf("%q got (%v, %v), want %v", input, got, ok, want)
		}
	}
}

func TestFilterByAge(t *testing.T) {
	products := []platform.ProductCard{
		card("1", "少儿医疗险", "200元/年起", []string{"0-17岁"}),
		card("2", "成人百万医疗", "500元/年起", []string{"18-60岁"}),
		card("3", "老年防癌险", "800元/年起", []string{"老年"}),
	}
	got := FilterByAge(products, "32岁上班族想买医疗险")
	if len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("got %v, want only adult product", ids(got))
	}
}

func TestDetectFamilySize(t *testing.T) {
	cases := map[string]int{
		"一家4口买医疗险": 4,
		"四口之家预算1万": 4,
		"家庭两个孩子":   4,
		"全家买保险":    3,
		"自己买保险":    1,
	}
	for input, want := range cases {
		if got := DetectFamilySize(input); got != want {
			t.Fatalf("%q got %d, want %d", input, got, want)
		}
	}
}

func TestServiceSearchInterleavesDedupesAndFilters(t *testing.T) {
	shared := card("a1", "共享百万医疗", "900元/年起", []string{"18-60岁"})
	service := New(
		fakePlatform{name: "p1", results: map[string][]platform.ProductCard{
			"医疗": {shared, card("a2", "境外旅游险", "100元/年起", []string{"旅游险"})},
		}},
		fakePlatform{name: "p2", results: map[string][]platform.ProductCard{
			"医疗": {shared, card("b2", "成人医疗优选", "1200元/年起", []string{"18-60岁"})},
		}},
	)
	got, err := service.Search(context.Background(), "32岁预算1000元买医疗险", Options{MaxPerPlatform: 10, MaxTotal: 10})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a1", "b2"}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("got %v, want %v", ids(got), want)
	}
}

func TestFamilyBudgetSplit(t *testing.T) {
	service := New(fakePlatform{name: "p1", results: map[string][]platform.ProductCard{
		"医疗": {
			card("fit", "家庭医疗", "900元/年起", []string{"医疗"}),
			card("too_high", "高端医疗", "5000元/年起", []string{"医疗"}),
		},
	}})
	got, err := service.Search(context.Background(), "一家三口预算3000元买医疗险", Options{MaxTotal: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "fit" {
		t.Fatalf("got %v, want fit only", ids(got))
	}
}

func card(id, name, price string, tags []string) platform.ProductCard {
	return platform.ProductCard{
		ID:         id,
		Name:       name,
		Price:      &price,
		PriceLabel: price,
		Tags:       tags,
		URL:        "https://example.test/" + id,
		Platform:   "test",
	}
}

func ids(products []platform.ProductCard) []string {
	out := make([]string, 0, len(products))
	for _, p := range products {
		out = append(out, p.ID)
	}
	return out
}
