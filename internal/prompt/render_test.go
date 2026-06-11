package prompt

import (
	"strings"
	"testing"
)

func TestBuildPrompt(t *testing.T) {
	got, err := BuildIntentUserPrompt("24岁想买医疗险")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "24岁想买医疗险") || !strings.Contains(got, "product_recommendation") {
		t.Fatalf("unexpected prompt: %s", got)
	}
}
