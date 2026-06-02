package config

import (
	"os"
	"strconv"
)

type Settings struct {
	AppEnv   string
	LogLevel string

	LLMProvider   string
	LLMModel      string
	LLMAPIKey     string
	LLMAPIBase    string
	LLMTimeout    int
	LLMMaxRetries int

	SearchAPIKey  string
	SearchAPIURL  string
	SearchTimeout int
	SearchTopN    int

	MCPSearchURL     string
	MCPSearchEngines string

	Orchestrator string
	RedisURL     string
	DatabaseURL  string
	MySQLDSN     string

	AgentChatEnabled         bool
	AgentDefaultID           string
	AgentMemoryWindow        int
	AgentTraceEnabled        bool
	AgentMode                string
	AgentMaxIterations       int
	AgentToolTimeout         int
	AgentActionRepairEnabled bool
	AgentScratchpadMaxChars  int
	AgentObservationMaxChars int

	MemoryMessageLimit int
	MemoryMaxChars     int

	EmbeddingModel     string
	EmbeddingAPIKey    string
	EmbeddingAPIBase   string
	EmbeddingTimeout   int
	EmbeddingBatchSize int

	IngestNamespace    string
	IngestChunkSize    int
	IngestChunkOverlap int
	IngestMinCNChars   int
}

func Load() Settings {
	return Settings{
		AppEnv:   getEnv("APP_ENV", "development"),
		LogLevel: getEnv("LOG_LEVEL", "info"),

		LLMProvider:   getEnv("LLM_PROVIDER", "minimax"),
		LLMModel:      getEnv("LLM_MODEL", ""),
		LLMAPIKey:     getEnv("LLM_API_KEY", ""),
		LLMAPIBase:    getEnv("LLM_API_BASE", ""),
		LLMTimeout:    getEnvInt("LLM_TIMEOUT", 30),
		LLMMaxRetries: getEnvInt("LLM_MAX_RETRIES", 1),

		SearchAPIKey:  getEnv("SEARCH_API_KEY", ""),
		SearchAPIURL:  getEnv("SEARCH_API_URL", "https://api.search.example.com/v1/search"),
		SearchTimeout: getEnvInt("SEARCH_TIMEOUT", 15),
		SearchTopN:    getEnvInt("SEARCH_TOP_N", 10),

		MCPSearchURL:     getEnv("MCP_SEARCH_URL", "http://web-search:3000"),
		MCPSearchEngines: getEnv("MCP_SEARCH_ENGINES", "baidu,bing"),

		Orchestrator: getEnv("ORCHESTRATOR", "lite"),
		RedisURL:     getEnv("REDIS_URL", "redis://localhost:6379/0"),
		DatabaseURL:  getEnv("DATABASE_URL", ""),
		MySQLDSN:     getEnv("MYSQL_DSN", ""),

		AgentChatEnabled:         getEnvBool("AGENT_CHAT_ENABLED", true),
		AgentDefaultID:           getEnv("AGENT_DEFAULT_ID", "smartinsure-advisor"),
		AgentMemoryWindow:        getEnvInt("AGENT_MEMORY_WINDOW", 10),
		AgentTraceEnabled:        getEnvBool("AGENT_TRACE_ENABLED", true),
		AgentMode:                getEnv("AGENT_MODE", "plan_act"),
		AgentMaxIterations:       getEnvInt("AGENT_MAX_ITERATIONS", 4),
		AgentToolTimeout:         getEnvInt("AGENT_TOOL_TIMEOUT", 15),
		AgentActionRepairEnabled: getEnvBool("AGENT_ACTION_REPAIR_ENABLED", true),
		AgentScratchpadMaxChars:  getEnvInt("AGENT_SCRATCHPAD_MAX_CHARS", 6000),
		AgentObservationMaxChars: getEnvInt("AGENT_OBSERVATION_MAX_CHARS", 2000),

		MemoryMessageLimit: getEnvInt("MEMORY_MESSAGE_LIMIT", 20),
		MemoryMaxChars:     getEnvInt("MEMORY_MAX_CHARS", 12000),

		EmbeddingModel:     getEnv("EMBEDDING_MODEL", "text-embedding-3-small"),
		EmbeddingAPIKey:    getEnv("EMBEDDING_API_KEY", ""),
		EmbeddingAPIBase:   getEnv("EMBEDDING_API_BASE", ""),
		EmbeddingTimeout:   getEnvInt("EMBEDDING_TIMEOUT", 30),
		EmbeddingBatchSize: getEnvInt("EMBEDDING_BATCH_SIZE", 16),

		IngestNamespace:    getEnv("INGEST_NAMESPACE", "default"),
		IngestChunkSize:    getEnvInt("INGEST_CHUNK_SIZE", 1200),
		IngestChunkOverlap: getEnvInt("INGEST_CHUNK_OVERLAP", 200),
		IngestMinCNChars:   getEnvInt("INGEST_MIN_CN_CHARS", 50),
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func getEnvBool(key string, fallback bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}
