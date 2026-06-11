package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Settings struct {
	AppEnv        string
	LogLevel      string
	LogConfigPath string
	LogFilePath   string
	LogToConsole  bool

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

	AgentChatEnabled   bool
	AgentDefaultID     string
	AgentMemoryWindow  int
	AgentTraceEnabled  bool
	AgentMode          string
	AgentMaxIterations int
	// AgentToolTimeout is the per-call timeout for planner-selected Agent tools.
	// Direct product_detail/product_followup actions are handled by their
	// dedicated detail flow and should not depend on this short generic timeout.
	AgentToolTimeout         int
	AgentActionRepairEnabled bool
	AgentScratchpadMaxChars  int
	AgentObservationMaxChars int

	MemoryMessageLimit int
	MemoryMaxChars     int

	EmbeddingConfigPath string
	EmbeddingProvider   string
	EmbeddingModel      string
	EmbeddingAPIKey     string
	EmbeddingAPIBase    string
	EmbeddingRegion     string
	EmbeddingAPIType    string
	EmbeddingTimeout    int
	EmbeddingBatchSize  int
	EmbeddingDimensions int
	EmbeddingRetryTimes int

	RAGConfigPath string

	IngestNamespace    string
	IngestChunkSize    int
	IngestChunkOverlap int
	IngestMinCNChars   int

	RAGSearchEnabled   bool
	RAGSearchNamespace string
	RAGSearchTopK      int
	RAGSearchMinScore  float64
	RAGSearchTimeout   int

	ProductDetailSharedCacheEnabled bool
	ProductDetailRedisTTL           int
	ProductDetailAliasRedisTTL      int
	ProductDetailDBTTL              int
	ProductDetailLockTTL            int
	ProductDetailPromptVersion      string
	ProductDetailRAGEnabled         bool
	ProductDetailRAGNamespace       string
	ProductDetailRAGMinMatchRate    float64
	ProductDetailRAGAsyncTimeout    int
	ProductDetailRAGAsyncWorkers    int
	ProductDetailRAGQueueSize       int
}

func Load() Settings {
	ragConfigPath := getEnv("RAG_CONFIG_PATH", "configs/rag.yaml")
	ragFile := loadOptionalRAGConfigFile(ragConfigPath)
	embeddingConfigPath := getEnv("EMBEDDING_CONFIG_PATH", firstNonEmpty(ragFile.EmbeddingConfigPath, "configs/embedding.yaml"))
	embeddingFile := loadOptionalEmbeddingConfigFile(embeddingConfigPath)
	logConfigPath := getEnv("LOG_CONFIG_PATH", "configs/logging.yaml")
	loggingFile := loadOptionalLoggingConfigFile(logConfigPath)
	logToConsole := true
	if loggingFile.ToConsole != nil {
		logToConsole = *loggingFile.ToConsole
	}

	return Settings{
		AppEnv:        getEnv("APP_ENV", "development"),
		LogLevel:      getEnv("LOG_LEVEL", firstNonEmpty(loggingFile.Level, "info")),
		LogConfigPath: logConfigPath,
		LogFilePath:   getEnv("LOG_FILE_PATH", loggingFile.FilePath),
		LogToConsole:  getEnvBool("LOG_TO_CONSOLE", logToConsole),

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
		RedisURL:     getEnv("REDIS_URL", firstNonEmpty(ragFile.RedisURL, "redis://localhost:6379/0")),
		DatabaseURL:  getEnv("DATABASE_URL", ragFile.DatabaseURL),
		MySQLDSN:     getEnv("MYSQL_DSN", ragFile.MySQLDSN),

		AgentChatEnabled:   getEnvBool("AGENT_CHAT_ENABLED", true),
		AgentDefaultID:     getEnv("AGENT_DEFAULT_ID", "smartinsure-advisor"),
		AgentMemoryWindow:  getEnvInt("AGENT_MEMORY_WINDOW", 10),
		AgentTraceEnabled:  getEnvBool("AGENT_TRACE_ENABLED", true),
		AgentMode:          getEnv("AGENT_MODE", "plan_act"),
		AgentMaxIterations: getEnvInt("AGENT_MAX_ITERATIONS", 10),
		// Default to a short generic tool timeout; raise AGENT_TOOL_TIMEOUT if
		// planner-selected tools must perform slow network or LLM work.
		AgentToolTimeout:         getEnvInt("AGENT_TOOL_TIMEOUT", 15),
		AgentActionRepairEnabled: getEnvBool("AGENT_ACTION_REPAIR_ENABLED", true),
		AgentScratchpadMaxChars:  getEnvInt("AGENT_SCRATCHPAD_MAX_CHARS", 6000),
		AgentObservationMaxChars: getEnvInt("AGENT_OBSERVATION_MAX_CHARS", 2000),

		MemoryMessageLimit: getEnvInt("MEMORY_MESSAGE_LIMIT", 20),
		MemoryMaxChars:     getEnvInt("MEMORY_MAX_CHARS", 12000),

		EmbeddingConfigPath: embeddingConfigPath,
		EmbeddingProvider:   getEnv("EMBEDDING_PROVIDER", firstNonEmpty(embeddingFile.Provider, "openai_compatible")),
		EmbeddingModel:      getEnv("EMBEDDING_MODEL", firstNonEmpty(embeddingFile.Model, "text-embedding-3-small")),
		EmbeddingAPIKey:     getEnv("EMBEDDING_API_KEY", embeddingFile.APIKey),
		EmbeddingAPIBase:    getEnv("EMBEDDING_API_BASE", embeddingFile.APIBase),
		EmbeddingRegion:     getEnv("EMBEDDING_REGION", firstNonEmpty(embeddingFile.Region, "cn-beijing")),
		EmbeddingAPIType:    getEnv("EMBEDDING_API_TYPE", embeddingFile.APIType),
		EmbeddingTimeout:    getEnvInt("EMBEDDING_TIMEOUT", firstNonZero(embeddingFile.Timeout, 30)),
		EmbeddingBatchSize:  getEnvInt("EMBEDDING_BATCH_SIZE", firstNonZero(embeddingFile.BatchSize, 16)),
		EmbeddingDimensions: getEnvInt("EMBEDDING_DIMENSIONS", embeddingFile.Dimensions),
		EmbeddingRetryTimes: getEnvInt("EMBEDDING_RETRY_TIMES", firstNonZero(embeddingFile.RetryTimes, 2)),

		RAGConfigPath: ragConfigPath,

		IngestNamespace:    getEnv("INGEST_NAMESPACE", "default"),
		IngestChunkSize:    getEnvInt("INGEST_CHUNK_SIZE", 1200),
		IngestChunkOverlap: getEnvInt("INGEST_CHUNK_OVERLAP", 200),
		IngestMinCNChars:   getEnvInt("INGEST_MIN_CN_CHARS", 50),

		RAGSearchEnabled:   getEnvBool("RAG_SEARCH_ENABLED", ragFile.RAGSearchEnabled),
		RAGSearchNamespace: getEnv("RAG_SEARCH_NAMESPACE", firstNonEmpty(ragFile.RAGSearchNamespace, "product_details")),
		RAGSearchTopK:      getEnvInt("RAG_SEARCH_TOP_K", firstNonZero(ragFile.RAGSearchTopK, 5)),
		RAGSearchMinScore:  getEnvFloat("RAG_SEARCH_MIN_SCORE", ragFile.RAGSearchMinScore),
		RAGSearchTimeout:   getEnvInt("RAG_SEARCH_TIMEOUT", firstNonZero(ragFile.RAGSearchTimeout, 5)),

		ProductDetailSharedCacheEnabled: getEnvBool("PRODUCT_DETAIL_SHARED_CACHE_ENABLED", true),
		ProductDetailRedisTTL:           getEnvInt("PRODUCT_DETAIL_REDIS_TTL", 604800),
		ProductDetailAliasRedisTTL:      getEnvInt("PRODUCT_DETAIL_ALIAS_REDIS_TTL", 2592000),
		ProductDetailDBTTL:              getEnvInt("PRODUCT_DETAIL_DB_TTL", 2592000),
		ProductDetailLockTTL:            getEnvInt("PRODUCT_DETAIL_LOCK_TTL", 60),
		ProductDetailPromptVersion:      getEnv("PRODUCT_DETAIL_PROMPT_VERSION", "detail_extract_v1"),
		ProductDetailRAGEnabled:         getEnvBool("PRODUCT_DETAIL_RAG_ENABLED", ragFile.ProductDetailRAGEnabled),
		ProductDetailRAGNamespace:       getEnv("PRODUCT_DETAIL_RAG_NAMESPACE", firstNonEmpty(ragFile.ProductDetailRAGNamespace, "product_details")),
		ProductDetailRAGMinMatchRate:    getEnvFloat("PRODUCT_DETAIL_RAG_MIN_MATCH_RATE", firstNonZeroFloat(ragFile.ProductDetailRAGMinMatchRate, 0.6)),
		ProductDetailRAGAsyncTimeout:    getEnvInt("PRODUCT_DETAIL_RAG_ASYNC_TIMEOUT", firstNonZero(ragFile.ProductDetailRAGAsyncTimeout, 30)),
		ProductDetailRAGAsyncWorkers:    getEnvInt("PRODUCT_DETAIL_RAG_ASYNC_WORKERS", firstNonZero(ragFile.ProductDetailRAGAsyncWorkers, 2)),
		ProductDetailRAGQueueSize:       getEnvInt("PRODUCT_DETAIL_RAG_QUEUE_SIZE", firstNonZero(ragFile.ProductDetailRAGQueueSize, 100)),
	}
}

type EmbeddingConfigFile struct {
	Provider   string
	Model      string
	APIKey     string
	APIBase    string
	Region     string
	APIType    string
	Timeout    int
	BatchSize  int
	Dimensions int
	RetryTimes int
}

type LoggingConfigFile struct {
	Level     string
	FilePath  string
	ToConsole *bool
}

type RAGConfigFile struct {
	MySQLDSN                     string
	RedisURL                     string
	DatabaseURL                  string
	EmbeddingConfigPath          string
	ProductDetailRAGEnabled      bool
	ProductDetailRAGNamespace    string
	ProductDetailRAGMinMatchRate float64
	ProductDetailRAGAsyncTimeout int
	ProductDetailRAGAsyncWorkers int
	ProductDetailRAGQueueSize    int
	RAGSearchEnabled             bool
	RAGSearchNamespace           string
	RAGSearchTopK                int
	RAGSearchMinScore            float64
	RAGSearchTimeout             int
}

func LoadLoggingConfigFile(path string) (LoggingConfigFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return LoggingConfigFile{}, err
	}
	defer file.Close()

	var cfg LoggingConfigFile
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		key, value, ok := splitKV(line)
		if !ok {
			continue
		}
		value = unquote(value)
		switch key {
		case "level":
			cfg.Level = value
		case "file_path", "log_file_path", "path":
			cfg.FilePath = value
		case "to_console", "console":
			parsed := parseBool(value)
			cfg.ToConsole = &parsed
		}
	}
	return cfg, scanner.Err()
}

func LoadEmbeddingConfigFile(path string) (EmbeddingConfigFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return EmbeddingConfigFile{}, err
	}
	defer file.Close()

	var cfg EmbeddingConfigFile
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		key, value, ok := splitKV(line)
		if !ok {
			continue
		}
		value = unquote(value)
		switch key {
		case "provider":
			cfg.Provider = value
		case "model":
			cfg.Model = value
		case "api_key":
			cfg.APIKey = value
		case "api_base":
			cfg.APIBase = value
		case "region":
			cfg.Region = value
		case "api_type":
			cfg.APIType = value
		case "timeout":
			cfg.Timeout = parsePositiveInt(value)
		case "batch_size":
			cfg.BatchSize = parsePositiveInt(value)
		case "dimensions":
			cfg.Dimensions = parsePositiveInt(value)
		case "retry_times":
			cfg.RetryTimes = parsePositiveInt(value)
		}
	}
	return cfg, scanner.Err()
}

func LoadRAGConfigFile(path string) (RAGConfigFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return RAGConfigFile{}, err
	}
	defer file.Close()

	var cfg RAGConfigFile
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		key, value, ok := splitKV(line)
		if !ok {
			continue
		}
		value = unquote(value)
		switch key {
		case "mysql_dsn":
			cfg.MySQLDSN = value
		case "redis_url":
			cfg.RedisURL = value
		case "database_url":
			cfg.DatabaseURL = value
		case "embedding_config_path":
			cfg.EmbeddingConfigPath = value
		case "product_detail_rag_enabled":
			cfg.ProductDetailRAGEnabled = parseBool(value)
		case "product_detail_rag_namespace":
			cfg.ProductDetailRAGNamespace = value
		case "product_detail_rag_min_match_rate":
			cfg.ProductDetailRAGMinMatchRate = parsePositiveFloat(value)
		case "product_detail_rag_async_timeout":
			cfg.ProductDetailRAGAsyncTimeout = parsePositiveInt(value)
		case "product_detail_rag_async_workers":
			cfg.ProductDetailRAGAsyncWorkers = parsePositiveInt(value)
		case "product_detail_rag_queue_size":
			cfg.ProductDetailRAGQueueSize = parsePositiveInt(value)
		case "rag_search_enabled":
			cfg.RAGSearchEnabled = parseBool(value)
		case "rag_search_namespace":
			cfg.RAGSearchNamespace = value
		case "rag_search_top_k":
			cfg.RAGSearchTopK = parsePositiveInt(value)
		case "rag_search_min_score":
			cfg.RAGSearchMinScore = parsePositiveFloat(value)
		case "rag_search_timeout":
			cfg.RAGSearchTimeout = parsePositiveInt(value)
		}
	}
	return cfg, scanner.Err()
}

func loadOptionalEmbeddingConfigFile(path string) EmbeddingConfigFile {
	if strings.TrimSpace(path) == "" {
		return EmbeddingConfigFile{}
	}
	resolved := resolveConfigPath(path)
	cfg, err := LoadEmbeddingConfigFile(resolved)
	if err != nil {
		return EmbeddingConfigFile{}
	}
	return cfg
}

func loadOptionalLoggingConfigFile(path string) LoggingConfigFile {
	if strings.TrimSpace(path) == "" {
		return LoggingConfigFile{}
	}
	resolved := resolveConfigPath(path)
	cfg, err := LoadLoggingConfigFile(resolved)
	if err != nil {
		return LoggingConfigFile{}
	}
	return cfg
}

func loadOptionalRAGConfigFile(path string) RAGConfigFile {
	if strings.TrimSpace(path) == "" {
		return RAGConfigFile{}
	}
	resolved := resolveConfigPath(path)
	cfg, err := LoadRAGConfigFile(resolved)
	if err != nil {
		return RAGConfigFile{}
	}
	return cfg
}

func resolveConfigPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	dir, err := os.Getwd()
	if err != nil {
		return path
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return path
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

func getEnvFloat(key string, fallback float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroFloat(values ...float64) float64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func parsePositiveInt(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func parsePositiveFloat(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func parseBool(raw string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return value
}
