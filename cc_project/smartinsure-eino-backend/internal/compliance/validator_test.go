package compliance

import (
	"strings"
	"testing"
)

func TestValidateDetectsProhibitedPhrases(t *testing.T) {
	got := Validate("这款一定最好，100%赔付，限时抢购。")
	if got.Compliant {
		t.Fatal("Validate() reported compliant for prohibited text")
	}
	want := []string{"100%赔付", "这款一定最好", "限时", "抢购"}
	if len(got.Issues) != len(want) {
		t.Fatalf("issues = %#v, want %d items", got.Issues, len(want))
	}
	for i, phrase := range want {
		if got.Issues[i].Phrase != phrase {
			t.Fatalf("issues[%d].Phrase = %q, want %q", i, got.Issues[i].Phrase, phrase)
		}
	}
}

func TestSanitizeReplacesMappedAndRemovesUnmapped(t *testing.T) {
	input := "保证收益，稳赚不赔，必须买，限时抢购。"
	got := Sanitize(input)
	if containsAny(got, ProhibitedPhrases) {
		t.Fatalf("Sanitize() kept prohibited phrase: %q", got)
	}
	for _, want := range []string{
		"预期收益（具体以合同为准）",
		"具有一定保障（具体以合同条款为准）",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Sanitize() = %q, missing %q", got, want)
		}
	}
}

func TestCleanTextIsCompliant(t *testing.T) {
	got := Validate("建议结合预算、健康状况和合同条款综合判断。")
	if !got.Compliant {
		t.Fatalf("Validate() = %#v, want compliant", got)
	}
	if len(got.Issues) != 0 {
		t.Fatalf("issues = %#v, want empty", got.Issues)
	}
}

func TestZeroValueValidatorUsesDefaultPolicy(t *testing.T) {
	var validator Validator
	got := validator.Sanitize("肯定能赔，抢购。")
	if containsAny(got, ProhibitedPhrases) {
		t.Fatalf("Sanitize() kept prohibited phrase: %q", got)
	}
	if !strings.Contains(got, "符合条款约定的情况下可理赔") {
		t.Fatalf("Sanitize() = %q, missing default replacement", got)
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
