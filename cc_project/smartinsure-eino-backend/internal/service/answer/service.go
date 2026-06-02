package answer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/prompt"
	"smartinsure-eino-backend/internal/schema"
)

type Service struct {
	model llm.ChatModel
}

type HistoryMessage struct {
	ID        string
	Role      string
	Content   string
	Metadata  map[string]any
	CreatedAt time.Time
}

func NewService(model llm.ChatModel) *Service {
	return &Service{model: model}
}

func (s *Service) Generate(ctx context.Context, message string, intent string, searchResults []schema.SearchResultItem) (schema.AnswerResult, error) {
	return s.GenerateWithHistory(ctx, message, intent, searchResults, nil)
}

func (s *Service) GenerateWithHistory(ctx context.Context, message string, intent string, searchResults []schema.SearchResultItem, history []HistoryMessage) (schema.AnswerResult, error) {
	if s == nil || s.model == nil {
		return schema.AnswerResult{}, fmt.Errorf("answer llm model is not configured")
	}

	raw, err := s.model.CallText(ctx, BuildAnswerMessagesWithHistory(message, intent, searchResults, history), 0.3)
	if err != nil {
		return schema.AnswerResult{}, fmt.Errorf("generate answer: %w", err)
	}
	return ParseAnswer(raw), nil
}

func (s *Service) GenerateStream(ctx context.Context, message string, intent string, searchResults []schema.SearchResultItem) (<-chan llm.StreamChunk, error) {
	return s.GenerateStreamWithHistory(ctx, message, intent, searchResults, nil)
}

func (s *Service) GenerateStreamWithHistory(ctx context.Context, message string, intent string, searchResults []schema.SearchResultItem, history []HistoryMessage) (<-chan llm.StreamChunk, error) {
	if s == nil || s.model == nil {
		return nil, fmt.Errorf("answer llm model is not configured")
	}
	return s.model.StreamText(ctx, BuildAnswerMessagesWithHistory(message, intent, searchResults, history), 0.3)
}

func BuildAnswerMessages(message string, intent string, searchResults []schema.SearchResultItem) []llm.Message {
	return BuildAnswerMessagesWithHistory(message, intent, searchResults, nil)
}

func BuildAnswerMessagesWithHistory(message string, intent string, searchResults []schema.SearchResultItem, history []HistoryMessage) []llm.Message {
	searchContext := FormatSearchContext(searchResults)
	historyContext := FormatHistoryContext(history)
	userPrompt, err := prompt.Render(prompt.AnswerTemplate, struct {
		Message        string
		Intent         string
		SearchContext  string
		HistoryContext string
	}{Message: message, Intent: intent, SearchContext: searchContext, HistoryContext: historyContext})
	if err != nil {
		userPrompt = fmt.Sprintf("用户问题: %s\n意图: %s\n%s检索结果:\n%s", message, intent, historyContext, searchContext)
	}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: prompt.System},
		{Role: llm.RoleUser, Content: userPrompt},
	}
}

func FormatSearchContext(results []schema.SearchResultItem) string {
	if len(results) == 0 {
		return "（无搜索结果）"
	}

	parts := make([]string, 0, len(results))
	for i, item := range results {
		parts = append(parts, fmt.Sprintf("[%d] %s\n网站: %s\n链接: %s\n摘要: %s", i+1, item.Title, item.Site, item.URL, item.Snippet))
	}
	return strings.Join(parts, "\n\n")
}

func FormatHistoryContext(history []HistoryMessage) string {
	lines := make([]string, 0, len(history))
	for _, item := range history {
		role := strings.TrimSpace(item.Role)
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		if role == "" {
			role = "unknown"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", role, content))
	}
	if len(lines) == 0 {
		return ""
	}
	return "【近期对话上下文】\n" + strings.Join(lines, "\n") + "\n\n"
}

func ParseAnswer(raw string) schema.AnswerResult {
	text := llm.StripMarkdownFence(strings.TrimSpace(raw))
	if text == "" {
		return schema.AnswerResult{}
	}

	var data struct {
		Summary string   `json:"summary"`
		Details []string `json:"details"`
		Caution string   `json:"caution"`
	}
	if err := json.Unmarshal([]byte(text), &data); err == nil {
		return schema.AnswerResult{
			Summary: data.Summary,
			Details: compactStrings(data.Details),
			Caution: strings.TrimSpace(data.Caution),
		}
	}

	return schema.AnswerResult{Summary: raw}
}

func compactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
