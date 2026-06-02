package productlegacy

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/search/parsers"
)

type fakeSearcher struct {
	results []schema.SearchResultItem
}

func (s fakeSearcher) Search(context.Context, string) ([]schema.SearchResultItem, error) {
	return append([]schema.SearchResultItem(nil), s.results...), nil
}

func TestCardsFromSearchResultsFiltersProductPages(t *testing.T) {
	results := []schema.SearchResultItem{
		{
			Title:   "【蓝医保百万医疗险】_小雨伞保险",
			URL:     "https://www.xiaoyusan.com/insurance/detail?id=177318",
			Snippet: "保证续保20年，医疗保障",
		},
		{
			Title:   "文章页",
			URL:     "https://www.xiaoyusan.com/article/123",
			Snippet: "非商品页",
		},
	}

	got := CardsFromSearchResults(results, nil)
	if len(got) != 1 {
		t.Fatalf("len(got)=%d, want 1: %#v", len(got), got)
	}
	card := got[0]
	if card.Name != "蓝医保百万医疗险" {
		t.Fatalf("name=%q", card.Name)
	}
	if card.Platform != "小雨伞" || card.PriceLabel != "加载中" {
		t.Fatalf("card=%#v", card)
	}
	if len(card.Tags) == 0 || card.Tags[0] != "百万医疗" {
		t.Fatalf("tags=%#v", card.Tags)
	}
}

func TestSearchProductsBuildsPlatformQueriesAndDedupes(t *testing.T) {
	svc := NewService(Options{
		Searcher: fakeSearcher{results: []schema.SearchResultItem{
			{Title: "蓝医保百万医疗险_小雨伞保险", URL: "https://www.xiaoyusan.com/insurance/detail?id=1", Snippet: "保证续保"},
			{Title: "蓝医保重复", URL: "https://www.xiaoyusan.com/insurance/detail?id=1/", Snippet: "重复"},
		}},
		MaxProducts: 5,
		Now: func() time.Time {
			return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		},
	})

	got, err := svc.SearchProducts(context.Background(), "想买医疗险")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got)=%d, want 1: %#v", len(got), got)
	}
}

func TestEnrichProductParsesPriceAndDetail(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `<html><head><title>蓝医保百万医疗险 - 慧择保险网</title><meta name="description" content="保证续保20年"></head><body>太平洋健康 238元/年</body></html>`
		return &http.Response{
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	svc := NewService(Options{HTTPClient: client})

	card := schema.ProductCard{
		ID:         "1",
		Name:       "蓝医保百万医疗险",
		URL:        "https://www.huize.com/apps/cps/index/product/detail?prodId=1",
		Platform:   "慧择",
		PriceLabel: "加载中",
	}
	got := svc.EnrichProduct(context.Background(), card)
	if got.Price != "238元/年" || got.PriceLabel != "238元/年" {
		t.Fatalf("price got=%#v", got)
	}
	if got.Brief != "保证续保20年" {
		t.Fatalf("brief=%q", got.Brief)
	}
	if parsers.ParsePriceFromText("首月 0 元") != "首月0元" {
		t.Fatalf("price parser not exposed as expected")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
