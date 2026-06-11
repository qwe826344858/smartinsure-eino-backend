package embedding

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/embedding/ark"
)

const defaultArkRegion = "cn-beijing"

type ArkEmbedder struct {
	inner     *ark.Embedder
	batchSize int
}

func NewArkEmbedder(ctx context.Context, cfg Config) (*ArkEmbedder, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("embedding ark api key is empty")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("embedding ark model is empty")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	retryTimes := cfg.RetryTimes
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = defaultArkRegion
	}

	arkCfg := &ark.EmbeddingConfig{
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
		BaseURL: normalizeArkBaseURL(cfg.APIBase),
		Region:  region,
		Timeout: &timeout,
	}
	if apiType := normalizeArkAPIType(cfg.APIType, cfg.APIBase); apiType != nil {
		arkCfg.APIType = apiType
	}
	if retryTimes > 0 {
		arkCfg.RetryTimes = &retryTimes
	}
	if cfg.Dimensions > 0 {
		arkCfg.Dimensions = &cfg.Dimensions
	}

	inner, err := ark.NewEmbedder(ctx, arkCfg)
	if err != nil {
		return nil, err
	}
	logx.Printf("运行日志", "runtime log", "embedding ark init_success model=%s region=%s api_type=%s dimensions=%d batch_size=%d timeout_seconds=%d retry_times=%d", cfg.Model, region, cfg.APIType, cfg.Dimensions, batchSize, int(timeout.Seconds()), retryTimes)
	return &ArkEmbedder{inner: inner, batchSize: batchSize}, nil
}

func (e *ArkEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if e == nil || e.inner == nil {
		return nil, errors.New("embedding ark embedder is nil")
	}
	batchSize := e.batchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	vectors := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		batchStartedAt := time.Now()
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batchVectors, err := e.inner.EmbedStrings(ctx, texts[start:end])
		if err != nil {
			logx.Printf("运行日志", "runtime log", "embedding ark batch_failed start=%d end=%d size=%d duration_ms=%d err=%v", start, end, end-start, time.Since(batchStartedAt).Milliseconds(), err)
			return nil, err
		}
		if len(batchVectors) != end-start {
			logx.Printf("运行日志", "runtime log", "embedding ark batch_invalid_count start=%d end=%d got=%d want=%d duration_ms=%d", start, end, len(batchVectors), end-start, time.Since(batchStartedAt).Milliseconds())
			return nil, fmt.Errorf("embedding response count mismatch: got=%d want=%d", len(batchVectors), end-start)
		}
		for i, vector := range batchVectors {
			if len(vector) == 0 {
				logx.Printf("运行日志", "runtime log", "embedding ark batch_empty_vector position=%d duration_ms=%d", start+i, time.Since(batchStartedAt).Milliseconds())
				return nil, fmt.Errorf("embedding at position %d is empty", start+i)
			}
		}
		dims := 0
		if len(batchVectors) > 0 {
			dims = len(batchVectors[0])
		}
		logx.Printf("运行日志", "runtime log", "embedding ark batch_success start=%d end=%d size=%d dims=%d duration_ms=%d", start, end, len(batchVectors), dims, time.Since(batchStartedAt).Milliseconds())
		vectors = append(vectors, batchVectors...)
	}
	return vectors, nil
}

var _ Embedder = (*ArkEmbedder)(nil)

func normalizeArkAPIType(raw string, apiBase string) *ark.APIType {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		base := strings.ToLower(strings.TrimRight(apiBase, "/"))
		if strings.HasSuffix(base, "/embeddings/multimodal") {
			apiType := ark.APITypeMultiModal
			return &apiType
		}
		return nil
	}
	switch value {
	case "text", "text_api":
		apiType := ark.APITypeText
		return &apiType
	case "multimodal", "multi_modal", "multi-modal", "multi_modal_api":
		apiType := ark.APITypeMultiModal
		return &apiType
	default:
		return nil
	}
}

func normalizeArkBaseURL(raw string) string {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return value
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/embeddings/multimodal"):
		parsed.Path = strings.TrimSuffix(path, "/embeddings/multimodal")
	case strings.HasSuffix(path, "/embeddings"):
		parsed.Path = strings.TrimSuffix(path, "/embeddings")
	default:
		parsed.Path = path
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}
