package followup

import (
	"context"
	"fmt"
	"strings"

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
	if s == nil || s.model == nil {
		return "", fmt.Errorf("followup llm model is not configured")
	}

	userPrompt, err := BuildUserPrompt(missingSlots)
	if err != nil {
		return "", err
	}
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: prompt.System},
		{Role: llm.RoleUser, Content: userPrompt},
	}
	text, err := s.model.CallText(ctx, messages, 0.4)
	if err != nil {
		return "", fmt.Errorf("generate followup: %w", err)
	}
	return strings.TrimSpace(text), nil
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
