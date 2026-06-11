package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestParseSearchResponse(t *testing.T) {
	raw := `{
		"jsonrpc":"2.0",
		"id":1,
		"result":{
			"content":[{"type":"text","text":"{\"results\":[{\"title\":\"医疗险测评\",\"url\":\"https://example.com/a\",\"description\":\"摘要\"},{\"title\":\"重复\",\"url\":\"https://example.com/a/\",\"description\":\"重复\"},{\"title\":\"短视频\",\"url\":\"https://douyin.com/a\",\"description\":\"跳过\"}]}"}]
		}
	}`
	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatal(err)
	}
	got := ParseSearchResponse(resp)
	if len(got) != 1 {
		t.Fatalf("len(got)=%d, want 1: %#v", len(got), got)
	}
	if got[0].Title != "医疗险测评" || got[0].Site != "example.com" {
		t.Fatalf("unexpected result: %#v", got[0])
	}
}

func TestClientSearchProtocol(t *testing.T) {
	var (
		mu      sync.Mutex
		methods []string
		ready   = make(chan struct{})
		once    sync.Once
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("response writer does not flush")
			}
			_, _ = fmt.Fprint(w, "data: /messages?session_id=test\n\n")
			flusher.Flush()
			<-ready
			_, _ = fmt.Fprint(w, `data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"results\":[{\"title\":\"A\",\"url\":\"https://a.example/p\",\"description\":\"aaa\"}]}"}]}}`+"\n\n")
			flusher.Flush()
		case "/messages":
			var payload struct {
				Method string `json:"method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			methods = append(methods, payload.Method)
			mu.Unlock()
			if payload.Method == "tools/call" {
				once.Do(func() { close(ready) })
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, WithHTTPClient(server.Client()), WithTimeouts(time.Second, time.Second))
	got, err := client.Search(context.Background(), "医疗险", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].URL != "https://a.example/p" {
		t.Fatalf("got %#v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"initialize", "notifications/initialized", "tools/call"}
	if len(methods) != len(want) {
		t.Fatalf("methods=%#v", methods)
	}
	for i := range want {
		if methods[i] != want[i] {
			t.Fatalf("methods=%#v, want %#v", methods, want)
		}
	}
}
