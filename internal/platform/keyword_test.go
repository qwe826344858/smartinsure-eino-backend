package platform

import (
	"reflect"
	"testing"
)

func TestExtractKeywordsDirectAndAudience(t *testing.T) {
	got := ExtractKeywords("想给3岁孩子买百万医疗险和意外险")
	wantPrefix := []string{"医疗", "意外", "少儿医疗"}
	if len(got) < len(wantPrefix) {
		t.Fatalf("got %v, want prefix %v", got, wantPrefix)
	}
	if !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("got %v, want prefix %v", got, wantPrefix)
	}
}

func TestExtractKeywordsFallbackByBudget(t *testing.T) {
	got := ExtractKeywords("预算每年400元，给自己买保险")
	want := []string{"意外", "医疗"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
