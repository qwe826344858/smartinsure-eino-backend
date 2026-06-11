package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSettingsFromEnv(t *testing.T) {
	t.Setenv("LLM_TIMEOUT", "42")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("LOG_FILE_PATH", "logs/test.log")
	t.Setenv("LOG_TO_CONSOLE", "false")
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
	t.Setenv("PRODUCT_DETAIL_SHARED_CACHE_ENABLED", "false")
	t.Setenv("PRODUCT_DETAIL_REDIS_TTL", "123")
	t.Setenv("PRODUCT_DETAIL_ALIAS_REDIS_TTL", "456")
	t.Setenv("PRODUCT_DETAIL_DB_TTL", "789")
	t.Setenv("PRODUCT_DETAIL_LOCK_TTL", "10")
	t.Setenv("PRODUCT_DETAIL_PROMPT_VERSION", "detail_extract_test")
	t.Setenv("PRODUCT_DETAIL_RAG_ENABLED", "true")
	t.Setenv("PRODUCT_DETAIL_RAG_NAMESPACE", "product_details")
	t.Setenv("PRODUCT_DETAIL_RAG_MIN_MATCH_RATE", "0.72")
	t.Setenv("PRODUCT_DETAIL_RAG_ASYNC_TIMEOUT", "45")
	t.Setenv("PRODUCT_DETAIL_RAG_ASYNC_WORKERS", "3")
	t.Setenv("PRODUCT_DETAIL_RAG_QUEUE_SIZE", "12")
	t.Setenv("EMBEDDING_PROVIDER", "ark")
	t.Setenv("EMBEDDING_REGION", "cn-shanghai")
	t.Setenv("EMBEDDING_API_TYPE", "multimodal")
	t.Setenv("EMBEDDING_DIMENSIONS", "1024")
	t.Setenv("EMBEDDING_RETRY_TIMES", "3")
	t.Setenv("RAG_SEARCH_ENABLED", "true")
	t.Setenv("RAG_SEARCH_NAMESPACE", "product_details")
	t.Setenv("RAG_SEARCH_TOP_K", "9")
	t.Setenv("RAG_SEARCH_MIN_SCORE", "0.42")
	t.Setenv("RAG_SEARCH_TIMEOUT", "7")
	settings := Load()
	if settings.LLMTimeout != 42 || settings.LLMProvider != "openai" {
		t.Fatalf("unexpected settings: %+v", settings)
	}
	if settings.LogFilePath != "logs/test.log" || settings.LogToConsole {
		t.Fatalf("unexpected log settings: %+v", settings)
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
	if settings.ProductDetailSharedCacheEnabled {
		t.Fatalf("ProductDetailSharedCacheEnabled = true, want false")
	}
	if settings.ProductDetailRedisTTL != 123 || settings.ProductDetailAliasRedisTTL != 456 || settings.ProductDetailDBTTL != 789 || settings.ProductDetailLockTTL != 10 {
		t.Fatalf("unexpected product detail ttl settings: %+v", settings)
	}
	if settings.ProductDetailPromptVersion != "detail_extract_test" {
		t.Fatalf("ProductDetailPromptVersion = %q", settings.ProductDetailPromptVersion)
	}
	if !settings.ProductDetailRAGEnabled || settings.ProductDetailRAGNamespace != "product_details" ||
		settings.ProductDetailRAGMinMatchRate != 0.72 || settings.ProductDetailRAGAsyncTimeout != 45 ||
		settings.ProductDetailRAGAsyncWorkers != 3 || settings.ProductDetailRAGQueueSize != 12 {
		t.Fatalf("unexpected product detail rag settings: %+v", settings)
	}
	if settings.EmbeddingProvider != "ark" || settings.EmbeddingRegion != "cn-shanghai" || settings.EmbeddingAPIType != "multimodal" || settings.EmbeddingDimensions != 1024 || settings.EmbeddingRetryTimes != 3 {
		t.Fatalf("unexpected embedding settings: %+v", settings)
	}
	if !settings.RAGSearchEnabled || settings.RAGSearchNamespace != "product_details" || settings.RAGSearchTopK != 9 || settings.RAGSearchMinScore != 0.42 || settings.RAGSearchTimeout != 7 {
		t.Fatalf("unexpected rag search settings: %+v", settings)
	}
}

func TestLoadLoggingConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logging.yaml")
	if err := os.WriteFile(path, []byte(`
level: debug
file_path: logs/server-test.log
to_console: false
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadLoggingConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Level != "debug" || cfg.FilePath != "logs/server-test.log" {
		t.Fatalf("unexpected logging config values: %+v", cfg)
	}
	if cfg.ToConsole == nil || *cfg.ToConsole {
		t.Fatalf("unexpected console logging config: %+v", cfg)
	}
}

func TestLoadLoggingConfigFileFeedsSettingsAndEnvOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logging.yaml")
	if err := os.WriteFile(path, []byte(`
level: debug
file_path: logs/from-file.log
to_console: false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOG_CONFIG_PATH", path)
	t.Setenv("LOG_FILE_PATH", "logs/from-env.log")
	t.Setenv("LOG_TO_CONSOLE", "true")

	settings := Load()
	if settings.LogConfigPath != path || settings.LogLevel != "debug" {
		t.Fatalf("unexpected logging paths or level: %+v", settings)
	}
	if settings.LogFilePath != "logs/from-env.log" || !settings.LogToConsole {
		t.Fatalf("unexpected loaded logging settings: %+v", settings)
	}
}

func TestLoadEmbeddingConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "embedding.yaml")
	if err := os.WriteFile(path, []byte(`
provider: ark
model: ep-test
api_key: test-key
api_base: https://ark.cn-beijing.volces.com/api/v3
region: cn-beijing
api_type: multimodal
timeout: 45
batch_size: 8
dimensions: 1024
retry_times: 4
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadEmbeddingConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "ark" || cfg.Model != "ep-test" || cfg.APIKey != "test-key" || cfg.APIBase == "" || cfg.Region != "cn-beijing" || cfg.APIType != "multimodal" {
		t.Fatalf("unexpected config file values: %+v", cfg)
	}
	if cfg.Timeout != 45 || cfg.BatchSize != 8 || cfg.Dimensions != 1024 || cfg.RetryTimes != 4 {
		t.Fatalf("unexpected numeric config file values: %+v", cfg)
	}
}

func TestLoadEmbeddingConfigFileFeedsSettingsAndEnvOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "embedding.yaml")
	if err := os.WriteFile(path, []byte(`
provider: ark
model: ep-from-file
api_key: key-from-file
api_base: https://ark.cn-beijing.volces.com/api/v3
region: cn-beijing
api_type: text
timeout: 40
batch_size: 6
dimensions: 1024
retry_times: 2
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMBEDDING_CONFIG_PATH", path)
	t.Setenv("EMBEDDING_MODEL", "ep-from-env")
	t.Setenv("EMBEDDING_DIMENSIONS", "2048")

	settings := Load()
	if settings.EmbeddingConfigPath != path {
		t.Fatalf("EmbeddingConfigPath = %q", settings.EmbeddingConfigPath)
	}
	if settings.EmbeddingProvider != "ark" || settings.EmbeddingModel != "ep-from-env" || settings.EmbeddingAPIKey != "key-from-file" || settings.EmbeddingAPIType != "text" {
		t.Fatalf("unexpected loaded embedding settings: %+v", settings)
	}
	if settings.EmbeddingTimeout != 40 || settings.EmbeddingBatchSize != 6 || settings.EmbeddingDimensions != 2048 || settings.EmbeddingRetryTimes != 2 {
		t.Fatalf("unexpected numeric embedding settings: %+v", settings)
	}
}

func TestLoadRAGConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag.yaml")
	if err := os.WriteFile(path, []byte(`
mysql_dsn: user:pass@tcp(mysql:3306)/smartinsure?parseTime=true
redis_url: redis://localhost:6379/0
database_url: postgres://user:pass@postgres:5432/smartinsure?sslmode=disable
embedding_config_path: configs/embedding.local.yaml
product_detail_rag_enabled: true
product_detail_rag_namespace: product_details
product_detail_rag_min_match_rate: 0.7
product_detail_rag_async_timeout: 45
product_detail_rag_async_workers: 3
product_detail_rag_queue_size: 20
rag_search_enabled: true
rag_search_namespace: product_details
rag_search_top_k: 8
rag_search_min_score: 0.4
rag_search_timeout: 6
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadRAGConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MySQLDSN == "" || cfg.RedisURL == "" || cfg.DatabaseURL == "" || cfg.EmbeddingConfigPath != "configs/embedding.local.yaml" {
		t.Fatalf("unexpected connection config: %+v", cfg)
	}
	if !cfg.ProductDetailRAGEnabled || cfg.ProductDetailRAGNamespace != "product_details" || cfg.ProductDetailRAGMinMatchRate != 0.7 {
		t.Fatalf("unexpected product detail rag config: %+v", cfg)
	}
	if cfg.ProductDetailRAGAsyncTimeout != 45 || cfg.ProductDetailRAGAsyncWorkers != 3 || cfg.ProductDetailRAGQueueSize != 20 {
		t.Fatalf("unexpected product detail rag async config: %+v", cfg)
	}
	if !cfg.RAGSearchEnabled || cfg.RAGSearchTopK != 8 || cfg.RAGSearchMinScore != 0.4 || cfg.RAGSearchTimeout != 6 {
		t.Fatalf("unexpected rag search config: %+v", cfg)
	}
}

func TestLoadRAGConfigFileFeedsSettingsAndEmbeddingConfigPath(t *testing.T) {
	dir := t.TempDir()
	embeddingPath := filepath.Join(dir, "embedding.yaml")
	if err := os.WriteFile(embeddingPath, []byte(`
provider: ark
model: ep-from-rag-config
api_key: key-from-embedding-file
api_base: https://ark.cn-beijing.volces.com/api/v3
region: cn-beijing
api_type: multimodal
dimensions: 1024
`), 0o600); err != nil {
		t.Fatal(err)
	}
	ragPath := filepath.Join(dir, "rag.yaml")
	if err := os.WriteFile(ragPath, []byte(`
mysql_dsn: user:pass@tcp(mysql:3306)/smartinsure?parseTime=true
redis_url: redis://redis:6379/0
database_url: postgres://user:pass@postgres:5432/smartinsure?sslmode=disable
embedding_config_path: `+embeddingPath+`
product_detail_rag_enabled: true
product_detail_rag_min_match_rate: 0.75
product_detail_rag_async_timeout: 40
product_detail_rag_async_workers: 4
product_detail_rag_queue_size: 30
rag_search_enabled: true
rag_search_top_k: 7
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RAG_CONFIG_PATH", ragPath)
	t.Setenv("RAG_SEARCH_TOP_K", "9")

	settings := Load()
	if settings.RAGConfigPath != ragPath || settings.EmbeddingConfigPath != embeddingPath {
		t.Fatalf("unexpected config paths: %+v", settings)
	}
	if settings.MySQLDSN == "" || settings.RedisURL != "redis://redis:6379/0" || settings.DatabaseURL == "" {
		t.Fatalf("rag file connection settings were not loaded: %+v", settings)
	}
	if settings.EmbeddingProvider != "ark" || settings.EmbeddingModel != "ep-from-rag-config" || settings.EmbeddingAPIKey != "key-from-embedding-file" {
		t.Fatalf("embedding file from rag config was not loaded: %+v", settings)
	}
	if !settings.ProductDetailRAGEnabled || settings.ProductDetailRAGMinMatchRate != 0.75 || settings.ProductDetailRAGAsyncWorkers != 4 {
		t.Fatalf("product detail rag settings were not loaded: %+v", settings)
	}
	if !settings.RAGSearchEnabled || settings.RAGSearchTopK != 9 {
		t.Fatalf("rag search env override failed: %+v", settings)
	}
}

func TestLoadEmbeddingConfigFileResolvesRelativePathFromNestedWorkingDir(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "embedding.yaml"), []byte(`
provider: ark
model: ep-relative
api_key: key-relative
api_base: https://ark.cn-beijing.volces.com/api/v3
region: cn-beijing
api_type: multimodal
dimensions: 1024
`), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "internal", "rag", "embedding")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	t.Setenv("EMBEDDING_CONFIG_PATH", "configs/embedding.yaml")

	settings := Load()
	if settings.EmbeddingModel != "ep-relative" || settings.EmbeddingAPIKey != "key-relative" || settings.EmbeddingAPIType != "multimodal" {
		t.Fatalf("relative config was not loaded: %+v", settings)
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
