package schema

import (
	"encoding/json"
	"testing"
)

func TestChatRequestJSONAliases(t *testing.T) {
	raw := []byte(`{"message":"hi","sessionId":"s1","chat_session_id":"chat_1","anonymous_id":"anon_1","requestId":"r1","action":"product_detail","productUrl":"https://example.com/p","productName":"A"}`)
	var req ChatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if req.SessionID != "s1" || req.ChatSessionID != "chat_1" || req.AnonymousID != "anon_1" || req.RequestID != "r1" || req.ProductURL == "" || req.ProductName != "A" {
		t.Fatalf("aliases not decoded: %+v", req)
	}
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"sessionId"`, `"chat_session_id"`, `"anonymous_id"`, `"requestId"`, `"productUrl"`, `"productName"`} {
		if !contains(string(out), want) {
			t.Fatalf("encoded json missing %s: %s", want, out)
		}
	}
}

func TestProductCardDefaultsAreExplicit(t *testing.T) {
	card := ProductCard{
		ID:         "1",
		Name:       "医疗险",
		PriceLabel: "加载中",
		Tags:       []string{},
		URL:        "https://example.com",
		Platform:   "test",
	}
	out, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"price_label":"加载中"`, `"tags":[]`} {
		if !contains(string(out), want) {
			t.Fatalf("encoded product card missing %s: %s", want, out)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && (s == sub || contains(s[1:], sub) || s[:len(sub)] == sub)
}
