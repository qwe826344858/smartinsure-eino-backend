package productdetail

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Fetcher interface {
	Fetch(ctx context.Context, url string) (string, error)
}

type HTTPFetcher struct {
	Client *http.Client
}

func NewHTTPFetcher(timeout time.Duration) *HTTPFetcher {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &HTTPFetcher{
		Client: &http.Client{Timeout: timeout},
	}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, url string) (string, error) {
	client := http.DefaultClient
	if f != nil && f.Client != nil {
		client = f.Client
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("fetch product page: status=%d body=%s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
