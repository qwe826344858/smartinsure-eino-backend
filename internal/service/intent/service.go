package intent

import (
	"context"
	"fmt"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/prompt"
	"smartinsure-eino-backend/internal/schema"
)

const (
	IntentKnowledgeExplain      = "knowledge_explain"
	IntentProductQuery          = "product_query"
	IntentProductRecommendation = "product_recommendation"
	IntentClauseExplain         = "clause_explain"
	IntentComparison            = "comparison"
	IntentUnderwritingBasic     = "underwriting_basic"
	IntentOutOfScope            = "out_of_scope"
)

var validIntents = map[string]struct{}{
	IntentKnowledgeExplain:      {},
	IntentProductQuery:          {},
	IntentProductRecommendation: {},
	IntentClauseExplain:         {},
	IntentComparison:            {},
	IntentUnderwritingBasic:     {},
	IntentOutOfScope:            {},
}

var followupRules = map[string]followupRule{
	IntentProductRecommendation: {Slots: []string{"age", "budget", "preference"}, MinMissing: 2},
	IntentComparison:            {Slots: []string{"product_names"}, MinMissing: 1},
	IntentClauseExplain:         {Slots: []string{"product_name", "clause_target"}, MinMissing: 1},
}

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

type followupRule struct {
	Slots      []string
	MinMissing int
}

func NewService(model llm.ChatModel) *Service {
	return &Service{model: model}
}

func (s *Service) Classify(ctx context.Context, message string) (schema.IntentResult, error) {
	return s.ClassifyWithHistory(ctx, message, nil)
}

func (s *Service) ClassifyWithHistory(ctx context.Context, message string, history []HistoryMessage) (schema.IntentResult, error) {
	startedAt := time.Now()
	if s == nil || s.model == nil {
		return schema.IntentResult{}, fmt.Errorf("intent llm model is not configured")
	}
	logx.Printf("运行日志", "runtime log", "intent classify_start message_chars=%d history=%d", len([]rune(strings.TrimSpace(message))), len(history))

	userPrompt, err := prompt.BuildIntentUserPromptWithHistory(message, FormatHistoryContext(history))
	if err != nil {
		logx.Printf("运行日志", "runtime log", "intent classify_failed stage=prompt duration_ms=%d err=%v", time.Since(startedAt).Milliseconds(), err)
		return schema.IntentResult{}, fmt.Errorf("build intent prompt: %w", err)
	}

	var data map[string]any
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: prompt.System},
		{Role: llm.RoleUser, Content: userPrompt},
	}
	if err := s.model.CallJSON(ctx, messages, 0.2, &data); err != nil {
		logx.Printf("运行日志", "runtime log", "intent classify_failed stage=llm duration_ms=%d err=%v", time.Since(startedAt).Milliseconds(), err)
		return schema.IntentResult{}, fmt.Errorf("classify intent: %w", err)
	}

	result := ApplyFollowupRules(ValidateIntent(data))
	logx.Printf("运行日志", "runtime log", "intent classify_success intent=%s needs_followup=%t missing_slots=%d duration_ms=%d", result.Intent, result.NeedsFollowup, len(result.MissingSlots), time.Since(startedAt).Milliseconds())
	return result, nil
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

func ValidateIntent(data map[string]any) schema.IntentResult {
	return schema.ValidateIntent(data)
}

func ApplyFollowupRules(result schema.IntentResult) schema.IntentResult {
	rule, ok := followupRules[result.Intent]
	if !ok {
		return result
	}

	required := make(map[string]struct{}, len(rule.Slots))
	for _, slot := range rule.Slots {
		required[slot] = struct{}{}
	}

	seen := map[string]struct{}{}
	actualMissing := make([]string, 0, len(result.MissingSlots))
	for _, slot := range result.MissingSlots {
		slot = strings.TrimSpace(slot)
		if slot == "" {
			continue
		}
		if _, ok := required[slot]; !ok {
			continue
		}
		if _, dup := seen[slot]; dup {
			continue
		}
		seen[slot] = struct{}{}
		actualMissing = append(actualMissing, slot)
	}

	result.MissingSlots = actualMissing
	result.NeedsFollowup = len(actualMissing) >= rule.MinMissing
	return result
}

func stringSlice(v any) []string {
	if typed, ok := v.([]string); ok {
		return compactStrings(typed)
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return compactStrings(out)
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
