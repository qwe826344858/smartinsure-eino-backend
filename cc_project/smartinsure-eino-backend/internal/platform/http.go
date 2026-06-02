package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

func defaultHTTPClient() HTTPDoer {
	return &http.Client{Timeout: 10 * time.Second}
}

func doJSON(ctx context.Context, client HTTPDoer, method, endpoint string, headers map[string]string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	return doRequest(ctx, client, method, endpoint, headers, reader, out)
}

func doForm(ctx context.Context, client HTTPDoer, endpoint string, headers map[string]string, values url.Values, out any) error {
	return doRequest(ctx, client, http.MethodPost, endpoint, headers, strings.NewReader(values.Encode()), out)
}

func doRequest(ctx context.Context, client HTTPDoer, method, endpoint string, headers map[string]string, body io.Reader, out any) error {
	if client == nil {
		client = defaultHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	for key, val := range headers {
		req.Header.Set(key, val)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("platform http status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	return nil
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func trimList(items []string, max int) []string {
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
		if len(out) == max {
			break
		}
	}
	return out
}

func ignoreExternalFailure(err error) ([]ProductCard, error) {
	if err == nil {
		return nil, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return []ProductCard{}, nil
}
