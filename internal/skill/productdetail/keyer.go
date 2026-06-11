package productdetail

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const (
	ProductPlatformPingan    = "pingan"
	ProductPlatformXiaoyusan = "xiaoyusan"
	ProductPlatformHuize     = "huize"
	ProductPlatformUnknown   = "unknown"
	productURLHashKeySegment = "url"
	defaultProductURLScheme  = "https"
)

var errEmptyProductURL = errors.New("productdetail: product url is empty")

type ProductIdentity struct {
	RawURL        string
	NormalizedURL string
	URLHash       string
	Platform      string
	ProductCode   string
	ProductKey    string
}

type ProductKeyer struct{}

func NewProductKeyer() ProductKeyer {
	return ProductKeyer{}
}

func (k ProductKeyer) Key(rawURL string) (ProductIdentity, error) {
	parsed, err := parseProductURL(rawURL)
	if err != nil {
		return ProductIdentity{}, err
	}
	normalizedURL := normalizeParsedProductURL(parsed)
	platform := inferProductPlatformFromHost(parsed.Hostname())
	urlHash := ProductURLHash(normalizedURL)

	productCode := ""
	if platform == ProductPlatformPingan {
		productCode = extractPinganProductCode(parsed)
	}
	productKey := BuildProductKey(platform, productCode, urlHash)
	return ProductIdentity{
		RawURL:        strings.TrimSpace(rawURL),
		NormalizedURL: normalizedURL,
		URLHash:       urlHash,
		Platform:      platform,
		ProductCode:   productCode,
		ProductKey:    productKey,
	}, nil
}

func (k ProductKeyer) NormalizeURL(rawURL string) (string, error) {
	parsed, err := parseProductURL(rawURL)
	if err != nil {
		return "", err
	}
	return normalizeParsedProductURL(parsed), nil
}

func NormalizeProductURL(rawURL string) (string, error) {
	return NewProductKeyer().NormalizeURL(rawURL)
}

func ProductURLHash(normalizedURL string) string {
	return SHA256Hex(strings.TrimSpace(normalizedURL))
}

func SHA256Hex(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

func BuildProductKey(platform, productCode, urlHash string) string {
	platform = strings.TrimSpace(strings.ToLower(platform))
	if platform == "" {
		platform = ProductPlatformUnknown
	}
	productCode = strings.TrimSpace(productCode)
	if productCode != "" {
		return platform + ":" + productCode
	}
	return platform + ":" + productURLHashKeySegment + ":" + strings.TrimSpace(urlHash)
}

func InferProductPlatform(rawURL string) string {
	parsed, err := parseProductURL(rawURL)
	if err != nil {
		return ProductPlatformUnknown
	}
	return inferProductPlatformFromHost(parsed.Hostname())
}

func parseProductURL(rawURL string) (*url.URL, error) {
	text := strings.TrimSpace(rawURL)
	if text == "" {
		return nil, errEmptyProductURL
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("productdetail: parse product url: %w", err)
	}
	if parsed.Scheme == "" && parsed.Host == "" && looksLikeBareHost(parsed.Path) {
		parsed, err = url.Parse(defaultProductURLScheme + "://" + text)
		if err != nil {
			return nil, fmt.Errorf("productdetail: parse product url: %w", err)
		}
	}
	if parsed.Scheme == "" && parsed.Host != "" {
		parsed.Scheme = defaultProductURLScheme
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("productdetail: product url host is empty")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("productdetail: unsupported product url scheme %q", parsed.Scheme)
	}
	return parsed, nil
}

func normalizeParsedProductURL(parsed *url.URL) string {
	normalized := *parsed
	normalized.Scheme = strings.ToLower(normalized.Scheme)
	if normalized.Scheme == "" {
		normalized.Scheme = defaultProductURLScheme
	}
	normalized.User = nil
	normalized.Host = normalizeHost(normalized)
	normalized.Fragment = ""
	normalized.RawFragment = ""
	normalized.Path = normalizePath(normalized.Path)
	normalized.RawPath = ""
	normalized.RawQuery = normalizeQuery(normalized.Query())
	return normalized.String()
}

func normalizeHost(parsed url.URL) string {
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port == "" || isDefaultPort(parsed.Scheme, port) {
		return host
	}
	return net.JoinHostPort(host, port)
}

func normalizePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return ""
	}
	return strings.TrimRight(value, "/")
}

func normalizeQuery(query url.Values) string {
	for key := range query {
		if isTrackingQueryParam(key) {
			delete(query, key)
		}
	}
	return query.Encode()
}

func isTrackingQueryParam(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "spm" ||
		key == "from" ||
		key == "channel" ||
		key == "_rag_check" ||
		key == "session" ||
		key == "session_id" ||
		key == "anonymous_id" ||
		strings.HasPrefix(key, "utm_") ||
		strings.HasPrefix(key, "share_")
}

func isDefaultPort(scheme, port string) bool {
	return (strings.EqualFold(scheme, "http") && port == "80") ||
		(strings.EqualFold(scheme, "https") && port == "443")
}

func looksLikeBareHost(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") {
		return false
	}
	firstSegment := value
	if idx := strings.Index(firstSegment, "/"); idx >= 0 {
		firstSegment = firstSegment[:idx]
	}
	return strings.Contains(firstSegment, ".") || strings.EqualFold(firstSegment, "localhost")
}

func inferProductPlatformFromHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch {
	case strings.Contains(host, "pingan.com"):
		return ProductPlatformPingan
	case strings.Contains(host, "xiaoyusan"):
		return ProductPlatformXiaoyusan
	case strings.Contains(host, "huize"):
		return ProductPlatformHuize
	default:
		return ProductPlatformUnknown
	}
}
