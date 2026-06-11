package fallback

import "testing"

func TestSearchMatchesKnowledge(t *testing.T) {
	got := NewService(nil).Search("重疾险等待期多久")
	if len(got) < 2 {
		t.Fatalf("len(got) = %d, want at least 2", len(got))
	}
}

func TestSearchAllDeduplicatesNormalizedURL(t *testing.T) {
	svc := NewService([]KnowledgeItem{
		{Title: "A", URL: "HTTPS://Example.com/a/", Site: "example.com", Snippet: "a", Keywords: []string{"x"}},
		{Title: "B", URL: "https://example.com/a", Site: "example.com", Snippet: "b", Keywords: []string{"y"}},
	})
	got := svc.SearchAll([]string{"x", "y"})
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1: %#v", len(got), got)
	}
}

func TestSearchFallsBackToGeneralItem(t *testing.T) {
	got := NewService(nil).Search("完全无关的问题")
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Title != DefaultGeneralItem().Title {
		t.Fatalf("title = %q, want general", got[0].Title)
	}
}
