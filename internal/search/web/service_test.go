package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/search/fallback"
)

type fakeBackend struct {
	items []schema.SearchResultItem
	err   error
}

func (b fakeBackend) Search(context.Context, string) ([]schema.SearchResultItem, error) {
	return append([]schema.SearchResultItem(nil), b.items...), b.err
}

func TestSearchDeduplicatesFiltersAndFallsBack(t *testing.T) {
	svc := NewService(Options{
		Backends: []Backend{fakeBackend{items: []schema.SearchResultItem{
			{Title: "等待期说明", URL: "HTTPS://Example.com/a/", Site: "example.com", Snippet: "等待期内容"},
			{Title: "等待期说明重复", URL: "https://example.com/a", Site: "example.com", Snippet: "重复"},
			{Title: "广告", URL: "https://example.com/ad", Site: "example.com", Snippet: "立即购买 限时优惠"},
			{Title: "缺摘要", URL: "https://example.com/empty", Site: "example.com"},
		}}},
	})

	got, err := svc.Search(context.Background(), []string{"等待期"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got)=%d, want 1: %#v", len(got), got)
	}
	if got[0].URL != "HTTPS://Example.com/a/" {
		t.Fatalf("unexpected item: %#v", got[0])
	}

	fallbackSvc := NewService(Options{
		Backends: []Backend{fakeBackend{items: []schema.SearchResultItem{
			{Title: "广告", URL: "https://example.com/ad", Snippet: "免费领"},
		}}},
		Fallback: fallback.NewService([]fallback.KnowledgeItem{
			{Title: "等待期", URL: "https://kb.example/wait", Site: "kb.example", Snippet: "等待期", Keywords: []string{"等待期"}},
		}),
	})
	got, err = fallbackSvc.Search(context.Background(), []string{"等待期"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].URL != "https://kb.example/wait" {
		t.Fatalf("fallback got %#v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPBackendsUseInjectedClient(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "search.local" {
			t.Fatalf("unexpected real network target: %s", req.URL.String())
		}
		if req.Method == http.MethodPost {
			if req.Header.Get("Authorization") != "Bearer mini-key" {
				t.Fatalf("missing minimax auth")
			}
			return jsonResponse(`{"base_resp":{"status_code":0},"organic":[{"title":"A","link":"https://a.example/p","snippet":"aaa"}]}`), nil
		}
		if req.Method == http.MethodGet {
			if got := req.URL.Query().Get("q"); got != "医疗险" {
				t.Fatalf("query=%q", got)
			}
			if req.Header.Get("Authorization") != "Bearer ext-key" {
				t.Fatalf("missing external auth")
			}
			return jsonResponse(`{"data":{"results":[{"title":"B","url":"https://b.example/p","description":"bbb"}]}}`), nil
		}
		t.Fatalf("unexpected method: %s", req.Method)
		return nil, nil
	})}

	mini := NewMiniMaxBackend(MiniMaxOptions{
		Endpoint:   "https://search.local/minimax",
		APIKey:     "mini-key",
		HTTPClient: client,
	})
	miniGot, err := mini.Search(context.Background(), "医疗险")
	if err != nil {
		t.Fatal(err)
	}
	if len(miniGot) != 1 || miniGot[0].Site != "a.example" {
		t.Fatalf("miniGot=%#v", miniGot)
	}

	external := NewExternalBackend(ExternalOptions{
		Endpoint:   "https://search.local/external",
		APIKey:     "ext-key",
		HTTPClient: client,
	})
	extGot, err := external.Search(context.Background(), "医疗险")
	if err != nil {
		t.Fatal(err)
	}
	if len(extGot) != 1 || extGot[0].Snippet != "bbb" {
		t.Fatalf("extGot=%#v", extGot)
	}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
