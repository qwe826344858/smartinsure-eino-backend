package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEmbedTextsBatchesOpenAICompatibleRequests(t *testing.T) {
	var requests []struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions,omitempty"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing auth header: %s", r.Header.Get("Authorization"))
		}
		var payload struct {
			Model      string   `json:"model"`
			Input      []string `json:"input"`
			Dimensions int      `json:"dimensions,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, payload)

		fmt.Fprint(w, `{"data":[`)
		for i := range payload.Input {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"index":%d,"embedding":[%d,%.1f]}`, i, i+1, float64(len([]rune(payload.Input[i]))))
		}
		fmt.Fprint(w, `]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		APIBase:    server.URL + "/v1/",
		APIKey:     "test-key",
		Model:      "embedding-model",
		BatchSize:  2,
		Dimensions: 1024,
		Timeout:    time.Second,
	})
	vectors, err := client.EmbedTexts(context.Background(), []string{"甲", "乙乙", "丙丙丙"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 3 {
		t.Fatalf("len(vectors)=%d", len(vectors))
	}
	if len(requests) != 2 {
		t.Fatalf("request count=%d", len(requests))
	}
	if requests[0].Model != "embedding-model" || len(requests[0].Input) != 2 || len(requests[1].Input) != 1 {
		t.Fatalf("unexpected requests: %+v", requests)
	}
	if requests[0].Dimensions != 1024 || requests[1].Dimensions != 1024 {
		t.Fatalf("unexpected dimensions in requests: %+v", requests)
	}
	if vectors[2][0] != 1 || vectors[2][1] != 3 {
		t.Fatalf("unexpected vector order: %+v", vectors)
	}
}

func TestEmbedTextsRejectsCountMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1]}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		APIBase:   server.URL,
		APIKey:    "test-key",
		Model:     "embedding-model",
		BatchSize: 10,
		Timeout:   time.Second,
	})
	_, err := client.EmbedTexts(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestEmbedTextsValidatesConfig(t *testing.T) {
	client := NewClient(Config{APIBase: "", APIKey: "k", Model: "m"})
	_, err := client.EmbedTexts(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("expected config error")
	}
}
