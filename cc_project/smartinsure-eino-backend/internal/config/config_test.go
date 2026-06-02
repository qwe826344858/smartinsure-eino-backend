package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSettingsFromEnv(t *testing.T) {
	t.Setenv("LLM_TIMEOUT", "42")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("AGENT_CHAT_ENABLED", "false")
	t.Setenv("AGENT_DEFAULT_ID", "agent-test")
	t.Setenv("AGENT_MEMORY_WINDOW", "7")
	t.Setenv("AGENT_TRACE_ENABLED", "false")
	t.Setenv("AGENT_MODE", "plan_act")
	t.Setenv("AGENT_MAX_ITERATIONS", "5")
	t.Setenv("AGENT_TOOL_TIMEOUT", "12")
	t.Setenv("AGENT_ACTION_REPAIR_ENABLED", "false")
	t.Setenv("AGENT_SCRATCHPAD_MAX_CHARS", "4000")
	t.Setenv("AGENT_OBSERVATION_MAX_CHARS", "1200")
	settings := Load()
	if settings.LLMTimeout != 42 || settings.LLMProvider != "openai" {
		t.Fatalf("unexpected settings: %+v", settings)
	}
	if settings.AgentChatEnabled {
		t.Fatalf("AgentChatEnabled = true, want false")
	}
	if settings.AgentDefaultID != "agent-test" || settings.AgentMemoryWindow != 7 {
		t.Fatalf("unexpected agent settings: %+v", settings)
	}
	if settings.AgentTraceEnabled {
		t.Fatalf("AgentTraceEnabled = true, want false")
	}
	if settings.AgentMode != "plan_act" || settings.AgentMaxIterations != 5 || settings.AgentToolTimeout != 12 {
		t.Fatalf("unexpected plan-act settings: %+v", settings)
	}
	if settings.AgentActionRepairEnabled || settings.AgentScratchpadMaxChars != 4000 || settings.AgentObservationMaxChars != 1200 {
		t.Fatalf("unexpected action/observation settings: %+v", settings)
	}
}

func TestLoadLLMProviderFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.yaml")
	err := os.WriteFile(path, []byte(`
default_provider: minimax
routing:
  intent:
    provider: openai
providers:
  minimax:
    description: "MiniMax"
    prefix: "openai"
    default_model: "MiniMax-M2.5-highspeed"
    api_key_env: "MINIMAX_API_KEY"
    api_base_env: "MINIMAX_API_BASE"
    default_base: "https://api.minimaxi.com/v1"
  disabled:
    enabled: false
    default_model: "x"
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadLLMProviderFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProvider != "minimax" {
		t.Fatalf("default provider mismatch: %s", cfg.DefaultProvider)
	}
	if cfg.Routing["intent"].Provider != "openai" {
		t.Fatalf("routing mismatch: %+v", cfg.Routing)
	}
	if cfg.Providers["minimax"].DefaultModel != "MiniMax-M2.5-highspeed" {
		t.Fatalf("provider mismatch: %+v", cfg.Providers["minimax"])
	}
	if cfg.Providers["disabled"].Enabled {
		t.Fatalf("disabled provider should be false")
	}
}
