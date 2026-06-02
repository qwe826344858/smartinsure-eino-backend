package answer

import (
	"context"
	"strings"
	"testing"

	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/schema"
)

type fakeModel struct {
	text     string
	messages []llm.Message
}

func (f *fakeModel) CallText(_ context.Context, messages []llm.Message, _ float64) (string, error) {
	f.messages = messages
	return f.text, nil
}

func (f *fakeModel) CallJSON(context.Context, []llm.Message, float64, any) error {
	return nil
}

func (f *fakeModel) StreamText(_ context.Context, messages []llm.Message, _ float64) (<-chan llm.StreamChunk, error) {
	f.messages = messages
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

func TestFormatSearchContext(t *testing.T) {
	got := FormatSearchContext([]schema.SearchResultItem{{
		Title:   "等待期",
		URL:     "https://example.com/a",
		Site:    "example.com",
		Snippet: "等待期说明",
	}})

	for _, want := range []string{"[1] 等待期", "网站: example.com", "链接: https://example.com/a", "摘要: 等待期说明"} {
		if !strings.Contains(got, want) {
			t.Fatalf("context missing %q: %s", want, got)
		}
	}
}

func TestParseAnswerSupportsMarkdownJSON(t *testing.T) {
	raw := "```json\n{\"summary\":\"一句话\",\"details\":[\" A \",\"\"],\"caution\":\"注意等待期\"}\n```"
	got := ParseAnswer(raw)

	if got.Summary != "一句话" {
		t.Fatalf("summary = %q", got.Summary)
	}
	if len(got.Details) != 1 || got.Details[0] != "A" {
		t.Fatalf("details = %#v", got.Details)
	}
	if got.Caution != "注意等待期" {
		t.Fatalf("caution = %q", got.Caution)
	}
}

func TestGenerateBuildsMessagesAndParsesAnswer(t *testing.T) {
	got, err := NewService(&fakeModel{text: `{"summary":"可以","details":["基于资料"],"caution":""}`}).
		Generate(context.Background(), "等待期是什么", "knowledge_explain", nil)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if got.Summary != "可以" {
		t.Fatalf("summary = %q", got.Summary)
	}
}

func TestBuildAnswerMessagesWithHistoryInjectsRecentContext(t *testing.T) {
	got := BuildAnswerMessagesWithHistory("继续推荐", "product_recommendation", nil, []HistoryMessage{
		{Role: "user", Content: "我 30 岁，预算 1500"},
		{Role: "assistant", Content: "可以先看百万医疗险"},
	})

	if len(got) != 2 {
		t.Fatalf("messages len = %d, want 2", len(got))
	}
	userPrompt := got[1].Content
	for _, want := range []string{"【近期对话上下文】", "- user: 我 30 岁，预算 1500", "- assistant: 可以先看百万医疗险", "用户问题: 继续推荐"} {
		if !strings.Contains(userPrompt, want) {
			t.Fatalf("prompt missing %q: %s", want, userPrompt)
		}
	}
}

func TestBuildAnswerMessagesWithoutHistoryOmitsRecentContext(t *testing.T) {
	got := BuildAnswerMessages("等待期是什么", "knowledge_explain", nil)
	if strings.Contains(got[1].Content, "【近期对话上下文】") {
		t.Fatalf("empty history should not be rendered: %s", got[1].Content)
	}
}

func TestGenerateStreamWithHistoryPassesPromptToModel(t *testing.T) {
	model := &fakeModel{}
	if _, err := NewService(model).GenerateStreamWithHistory(context.Background(), "继续", "product_query", nil, []HistoryMessage{
		{Role: "user", Content: "上一轮内容"},
	}); err != nil {
		t.Fatalf("GenerateStreamWithHistory returned error: %v", err)
	}
	if len(model.messages) != 2 || !strings.Contains(model.messages[1].Content, "上一轮内容") {
		t.Fatalf("history missing from model messages: %#v", model.messages)
	}
}
