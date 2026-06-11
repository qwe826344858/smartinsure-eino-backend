package followup

import (
	"context"
	"strings"
	"testing"

	"smartinsure-eino-backend/internal/llm"
)

type fakeModel struct {
	text string
}

func (f fakeModel) CallText(context.Context, []llm.Message, float64) (string, error) {
	return f.text, nil
}

func (f fakeModel) CallJSON(context.Context, []llm.Message, float64, any) error {
	return nil
}

func (f fakeModel) StreamText(context.Context, []llm.Message, float64) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

func TestBuildUserPromptIncludesMissingSlots(t *testing.T) {
	got, err := BuildUserPrompt([]string{"age", "", "budget"})
	if err != nil {
		t.Fatalf("BuildUserPrompt returned error: %v", err)
	}
	if !strings.Contains(got, "age、budget") {
		t.Fatalf("prompt = %q, want slots", got)
	}
}

func TestGenerateTrimsText(t *testing.T) {
	got, err := NewService(fakeModel{text: " 请补充年龄和预算。 \n"}).Generate(context.Background(), []string{"age", "budget"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if got != "请补充年龄和预算。" {
		t.Fatalf("got %q", got)
	}
}
