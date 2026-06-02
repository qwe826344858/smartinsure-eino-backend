package web

import (
	"context"
	"net/http"
	"time"

	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/search/fallback"
)

type Backend interface {
	Search(ctx context.Context, query string) ([]schema.SearchResultItem, error)
}

type Service struct {
	backends       []Backend
	fallback       *fallback.Service
	maxResults     int
	maxConcurrency int
	lowQuality     []string
}

type Options struct {
	Backends       []Backend
	Fallback       *fallback.Service
	MaxResults     int
	MaxConcurrency int
	LowQuality     []string
}

func NewService(opts Options) *Service {
	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 15
	}
	maxConcurrency := opts.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = 4
	}
	lowQuality := opts.LowQuality
	if len(lowQuality) == 0 {
		lowQuality = defaultLowQualityKeywords
	}
	fb := opts.Fallback
	if fb == nil {
		fb = fallback.NewService(nil)
	}
	return &Service{
		backends:       append([]Backend(nil), opts.Backends...),
		fallback:       fb,
		maxResults:     maxResults,
		maxConcurrency: maxConcurrency,
		lowQuality:     append([]string(nil), lowQuality...),
	}
}

type MiniMaxOptions struct {
	Endpoint   string
	APIKey     string
	HTTPClient *http.Client
	Timeout    time.Duration
}

type ExternalOptions struct {
	Endpoint   string
	APIKey     string
	HTTPClient *http.Client
	Timeout    time.Duration
	TopN       int
}

var defaultLowQualityKeywords = []string{
	"广告",
	"推广",
	"立即购买",
	"限时优惠",
	"点击领取",
	"免费领",
	"加微信",
	"扫码关注",
	"本地宝",
	"缴费标准",
	"缴费基数",
	"职工医保缴费",
}
