package llm

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// NewEinoOpenAIChatModel creates an Eino ChatModel for OpenAI-compatible
// providers such as MiniMax, OpenAI, Qwen, Zhipu and Moonshot.
func NewEinoOpenAIChatModel(ctx context.Context, provider ProviderConfig) (model.ToolCallingChatModel, error) {
	if provider.Key == "" {
		return nil, errors.New("llm api key is empty")
	}
	if provider.Base == "" {
		return nil, errors.New("llm api base is empty")
	}
	modelName := providerModelName(provider.Model)
	if modelName == "" {
		return nil, errors.New("llm model is empty")
	}
	return openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: provider.Base,
		Model:   modelName,
		APIKey:  provider.Key,
	})
}

func (r *Registry) EinoChatModelForStage(ctx context.Context, stage string) (model.ToolCallingChatModel, ProviderConfig, error) {
	provider := r.ForStage(stage)
	chatModel, err := NewEinoOpenAIChatModel(ctx, provider)
	return chatModel, provider, err
}

func providerModelName(model string) string {
	if strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		return parts[1]
	}
	return model
}
