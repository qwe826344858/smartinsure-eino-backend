package embedding

import (
	"context"
	"fmt"
	"smartinsure-eino-backend/internal/logx"
	"strings"
)

type Provider string

const (
	ProviderOpenAICompatible Provider = "openai_compatible"
	ProviderArk              Provider = "ark"
)

func NewEmbedder(ctx context.Context, cfg Config) (Embedder, error) {
	provider := normalizeProvider(cfg.Provider)
	logx.Printf("运行日志", "runtime log", "embedding init provider=%s model=%s region=%s api_type=%s dimensions=%d batch_size=%d timeout_seconds=%d retry_times=%d", provider, cfg.Model, cfg.Region, cfg.APIType, cfg.Dimensions, cfg.BatchSize, int(cfg.Timeout.Seconds()), cfg.RetryTimes)
	switch provider {
	case ProviderArk:
		return NewArkEmbedder(ctx, cfg)
	case ProviderOpenAICompatible:
		return NewClient(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %s", cfg.Provider)
	}
}

func normalizeProvider(provider Provider) Provider {
	value := strings.TrimSpace(strings.ToLower(string(provider)))
	switch value {
	case "", string(ProviderOpenAICompatible), "openai-compatible", "openai":
		return ProviderOpenAICompatible
	case string(ProviderArk):
		return ProviderArk
	default:
		return Provider(value)
	}
}
