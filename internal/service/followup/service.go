package followup

import (
	"context"
	"fmt"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/prompt"
)

type Service struct {
	model llm.ChatModel
}

func NewService(model llm.ChatModel) *Service {
	return &Service{model: model}
}

func (s *Service) Generate(ctx context.Context, missingSlots []string) (string, error) {
	startedAt := time.Now()
	if s == nil || s.model == nil {
		return "", fmt.Errorf("followup llm model is not configured")
	}
	logx.Printf("运行日志", "runtime log", "followup generate_start missing_slots=%d", len(missingSlots))

	userPrompt, err := BuildUserPrompt(missingSlots)
	if err != nil {
		logx.Printf("运行日志", "runtime log", "followup generate_failed stage=prompt duration_ms=%d err=%v", time.Since(startedAt).Milliseconds(), err)
		return "", err
	}
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: prompt.System},
		{Role: llm.RoleUser, Content: userPrompt},
	}
	text, err := s.model.CallText(ctx, messages, 0.4)
	if err != nil {
		logx.Printf("运行日志", "runtime log", "followup generate_failed stage=llm duration_ms=%d err=%v", time.Since(startedAt).Milliseconds(), err)
		return "", fmt.Errorf("generate followup: %w", err)
	}
	text = strings.TrimSpace(text)
	logx.Printf("运行日志", "runtime log", "followup generate_success chars=%d duration_ms=%d", len([]rune(text)), time.Since(startedAt).Milliseconds())
	return text, nil
}

func BuildUserPrompt(missingSlots []string) (string, error) {
	return prompt.Render(prompt.FollowupTemplate, struct {
		MissingSlots string
	}{MissingSlots: formatSlots(missingSlots)})
}

func formatSlots(slots []string) string {
	clean := make([]string, 0, len(slots))
	for _, slot := range slots {
		if slot = strings.TrimSpace(slot); slot != "" {
			clean = append(clean, slot)
		}
	}
	if len(clean) == 0 {
		return "（无）"
	}
	return strings.Join(clean, "、")
}
