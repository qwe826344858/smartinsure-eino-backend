package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStripThinkAndFence(t *testing.T) {
	if got := StripThink("a<think>secret</think>b"); got != "ab" {
		t.Fatalf("StripThink()=%q", got)
	}
	if got := StripMarkdownFence("```json\n{\"a\":1}\n```"); got != `{"a":1}` {
		t.Fatalf("StripMarkdownFence()=%q", got)
	}
}

func TestClientCallTextOpenAICompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing auth header")
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"<think>x</think>回答"}}]}`)
	}))
	defer server.Close()

	client := NewClient(ProviderConfig{
		Name:  "test",
		Model: "openai/model-a",
		Key:   "test-key",
		Base:  server.URL + "/v1",
	}, time.Second, 0)
	got, err := client.CallText(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if got != "回答" {
		t.Fatalf("unexpected text: %q", got)
	}
}
