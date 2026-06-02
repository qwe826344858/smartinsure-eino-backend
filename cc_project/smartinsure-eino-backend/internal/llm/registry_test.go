package llm

import (
	"testing"

	"smartinsure-eino-backend/internal/config"
)

func TestRegistryRoutesAndFallback(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ok")
	file := config.LLMProviderFile{
		DefaultProvider: "minimax",
		Routing: map[string]config.RouteConfig{
			"intent": {Provider: "openai"},
			"answer": {Provider: "minimax"},
		},
		Providers: map[string]config.ProviderFileConfig{
			"minimax": {
				Description:  "MiniMax",
				Prefix:       "openai",
				DefaultModel: "MiniMax-M2.5-highspeed",
				DefaultBase:  "https://api.minimaxi.com/v1",
				Enabled:      true,
			},
			"openai": {
				Description:  "OpenAI",
				Prefix:       "openai",
				DefaultModel: "gpt-4o-mini",
				APIKeyEnv:    "OPENAI_API_KEY",
				DefaultBase:  "https://api.openai.com/v1",
				Enabled:      true,
			},
		},
	}
	reg := NewRegistry(file, config.Settings{})
	if got := reg.ForStage("intent"); got.Name != "openai" {
		t.Fatalf("intent should route to openai: %+v", got)
	}
	if got := reg.ForStage("answer"); got.Name != "openai" {
		t.Fatalf("answer should fallback to available openai: %+v", got)
	}
}
