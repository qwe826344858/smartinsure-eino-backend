package ingest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	DefaultFetchTimeout = 20 * time.Second
	DefaultMaxBodyBytes = 4 << 20
)

var defaultHeaders = map[string]string{
	"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
	"Accept":     "text/html,application/xhtml+xml",
}

type Fetcher interface {
	Fetch(ctx context.Context, url string) (string, error)
}

type HTTPFetcher struct {
	Client       *http.Client
	MaxBodyBytes int64
}

func NewHTTPFetcher(timeout time.Duration) *HTTPFetcher {
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}
	return &HTTPFetcher{
		Client:       &http.Client{Timeout: timeout},
		MaxBodyBytes: DefaultMaxBodyBytes,
	}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, url string) (string, error) {
	client := http.DefaultClient
	maxBodyBytes := int64(DefaultMaxBodyBytes)
	if f != nil {
		if f.Client != nil {
			client = f.Client
		}
		if f.MaxBodyBytes > 0 {
			maxBodyBytes = f.MaxBodyBytes
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	for key, value := range defaultHeaders {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if len(body) > 1024 {
			body = body[:1024]
		}
		return "", fmt.Errorf("fetch webpage: status=%d body=%s", resp.StatusCode, string(body))
	}
	return string(body), nil
}
