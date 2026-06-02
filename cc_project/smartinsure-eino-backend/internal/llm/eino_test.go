package llm

import (
	"context"
	"strings"
	"testing"

	"smartinsure-eino-backend/internal/config"
)

func TestEinoChatModelForStageRoutesProviders(t *testing.T) {
	t.Setenv("EINO_TEST_KEY", "test-key")

	reg := NewRegistry(config.LLMProviderFile{
		DefaultProvider: "minimax",
		Routing: map[string]config.RouteConfig{
			"intent":   {Provider: "openai"},
			"answer":   {Provider: "minimax"},
			"followup": {Provider: "qwen"},
			"detail":   {Provider: "moonshot"},
		},
		Providers: map[string]config.ProviderFileConfig{
			"minimax": {
				Prefix:       "minimax",
				DefaultModel: "MiniMax-Text-01",
				APIKeyEnv:    "EINO_TEST_KEY",
				DefaultBase:  "https://minimax.example/v1",
				Enabled:      true,
			},
			"openai": {
				Prefix:       "openai",
				DefaultModel: "gpt-4o-mini",
				APIKeyEnv:    "EINO_TEST_KEY",
				DefaultBase:  "https://openai.example/v1",
				Enabled:      true,
			},
			"qwen": {
				Prefix:       "openai",
				DefaultModel: "qwen-plus",
				APIKeyEnv:    "EINO_TEST_KEY",
				DefaultBase:  "https://qwen.example/v1",
				Enabled:      true,
			},
			"moonshot": {
				Prefix:       "openai",
				DefaultModel: "moonshot-v1-8k",
				APIKeyEnv:    "EINO_TEST_KEY",
				DefaultBase:  "https://moonshot.example/v1",
				Enabled:      true,
			},
		},
	}, config.Settings{})

	tests := []struct {
		stage        string
		wantName     string
		wantModel    string
		wantBase     string
		wantEinoName string
	}{
		{stage: "intent", wantName: "openai", wantModel: "openai/gpt-4o-mini", wantBase: "https://openai.example/v1", wantEinoName: "gpt-4o-mini"},
		{stage: "answer", wantName: "minimax", wantModel: "minimax/MiniMax-Text-01", wantBase: "https://minimax.example/v1", wantEinoName: "MiniMax-Text-01"},
		{stage: "followup", wantName: "qwen", wantModel: "openai/qwen-plus", wantBase: "https://qwen.example/v1", wantEinoName: "qwen-plus"},
		{stage: "detail", wantName: "moonshot", wantModel: "openai/moonshot-v1-8k", wantBase: "https://moonshot.example/v1", wantEinoName: "moonshot-v1-8k"},
	}

	for _, tt := range tests {
		t.Run(tt.stage, func(t *testing.T) {
			chatModel, provider, err := reg.EinoChatModelForStage(context.Background(), tt.stage)
			if err != nil {
				t.Fatalf("EinoChatModelForStage() error = %v", err)
			}
			if chatModel == nil {
				t.Fatal("EinoChatModelForStage() returned nil chat model")
			}
			if provider.Name != tt.wantName {
				t.Fatalf("provider name = %q, want %q", provider.Name, tt.wantName)
			}
			if provider.Model != tt.wantModel {
				t.Fatalf("provider model = %q, want %q", provider.Model, tt.wantModel)
			}
			if provider.Base != tt.wantBase {
				t.Fatalf("provider base = %q, want %q", provider.Base, tt.wantBase)
			}
			if got := providerModelName(provider.Model); got != tt.wantEinoName {
				t.Fatalf("providerModelName(%q) = %q, want %q", provider.Model, got, tt.wantEinoName)
			}
		})
	}
}

func TestNewEinoOpenAIChatModelRequiresProviderFields(t *testing.T) {
	tests := []struct {
		name     string
		provider ProviderConfig
		wantErr  string
	}{
		{
			name:     "missing key",
			provider: ProviderConfig{Base: "https://llm.example/v1", Model: "openai/test-model"},
			wantErr:  "llm api key is empty",
		},
		{
			name:     "missing base",
			provider: ProviderConfig{Key: "test-key", Model: "openai/test-model"},
			wantErr:  "llm api base is empty",
		},
		{
			name:     "missing model",
			provider: ProviderConfig{Key: "test-key", Base: "https://llm.example/v1"},
			wantErr:  "llm model is empty",
		},
		{
			name:     "empty model after provider prefix",
			provider: ProviderConfig{Key: "test-key", Base: "https://llm.example/v1", Model: "openai/"},
			wantErr:  "llm model is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chatModel, err := NewEinoOpenAIChatModel(context.Background(), tt.provider)
			if err == nil {
				t.Fatalf("NewEinoOpenAIChatModel() error = nil, want %q", tt.wantErr)
			}
			if chatModel != nil {
				t.Fatal("NewEinoOpenAIChatModel() returned non-nil chat model on error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewEinoOpenAIChatModel() error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestProviderModelName(t *testing.T) {
	tests := map[string]string{
		"":                         "",
		"gpt-4o-mini":              "gpt-4o-mini",
		"minimax/MiniMax-Text-01":  "MiniMax-Text-01",
		"openai/moonshot-v1-8k":    "moonshot-v1-8k",
		"provider/nested/model-id": "nested/model-id",
	}
	for input, want := range tests {
		if got := providerModelName(input); got != want {
			t.Fatalf("providerModelName(%q) = %q, want %q", input, got, want)
		}
	}
}
