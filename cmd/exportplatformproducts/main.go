package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/platform"
)

const defaultKeywords = "医疗,百万医疗,高端医疗,重疾,意外,寿险,少儿,防癌,年金,养老,教育金,旅游,家财,车险"

type cliOptions struct {
	Output         string
	Platforms      string
	Keywords       string
	MaxPerPlatform int
	PageLimit      int
	Timeout        time.Duration
	RequestTimeout time.Duration
	Delay          time.Duration
	WithBOM        bool
}

type exportRow struct {
	PlatformKey string
	Keyword     string
	Page        int
	Product     platform.ProductCard
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}
	keywords := splitList(opts.Keywords)
	if len(keywords) == 0 {
		return fmt.Errorf("keywords is empty")
	}
	platforms, err := selectedPlatforms(opts.Platforms)
	if err != nil {
		return err
	}
	if len(platforms) == 0 {
		return fmt.Errorf("platforms is empty")
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	rows := make([]exportRow, 0, len(platforms)*opts.MaxPerPlatform)
	for _, entry := range platforms {
		startedAt := time.Now()
		collected, err := collectPlatform(ctx, entry.key, entry.platform, keywords, opts)
		if err != nil {
			return err
		}
		rows = append(rows, collected...)
		fmt.Fprintf(os.Stderr, "[%s] collected=%d duration_ms=%d\n", entry.key, len(collected), time.Since(startedAt).Milliseconds())
	}
	if err := writeCSV(opts.Output, rows, opts.WithBOM); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "done: output=%s rows=%d\n", opts.Output, len(rows))
	return nil
}

func parseArgs(args []string) (cliOptions, error) {
	var opts cliOptions
	fs := flag.NewFlagSet("exportplatformproducts", flag.ContinueOnError)
	fs.StringVar(&opts.Output, "output", "logs/platform_products.csv", "CSV output path")
	fs.StringVar(&opts.Platforms, "platforms", "xiaoyusan,pingan,huize", "comma separated platform keys")
	fs.StringVar(&opts.Keywords, "keywords", defaultKeywords, "comma separated search keywords")
	fs.IntVar(&opts.MaxPerPlatform, "max-per-platform", 200, "maximum rows per platform")
	fs.IntVar(&opts.PageLimit, "page-limit", 30, "maximum pages per keyword")
	fs.DurationVar(&opts.Timeout, "timeout", 30*time.Minute, "overall timeout")
	fs.DurationVar(&opts.RequestTimeout, "request-timeout", 15*time.Second, "per platform request timeout")
	fs.DurationVar(&opts.Delay, "delay", 200*time.Millisecond, "delay between platform requests")
	fs.BoolVar(&opts.WithBOM, "bom", true, "write UTF-8 BOM for spreadsheet compatibility")
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if strings.TrimSpace(opts.Output) == "" {
		return cliOptions{}, fmt.Errorf("output is empty")
	}
	if opts.MaxPerPlatform <= 0 || opts.MaxPerPlatform > 200 {
		opts.MaxPerPlatform = 200
	}
	if opts.PageLimit <= 0 {
		opts.PageLimit = 30
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 15 * time.Second
	}
	if opts.Delay < 0 {
		opts.Delay = 0
	}
	return opts, nil
}

type platformEntry struct {
	key      string
	platform platform.Platform
}

func selectedPlatforms(raw string) ([]platformEntry, error) {
	available := map[string]platform.Platform{
		"xiaoyusan": platform.Xiaoyusan{},
		"xys":       platform.Xiaoyusan{},
		"pingan":    platform.Pingan{},
		"pa":        platform.Pingan{},
		"huize":     platform.Huize{},
		"hz":        platform.Huize{},
	}
	keys := splitList(raw)
	if len(keys) == 0 {
		keys = []string{"xiaoyusan", "pingan", "huize"}
	}
	seen := map[string]bool{}
	out := make([]platformEntry, 0, len(keys))
	for _, key := range keys {
		normalized := strings.ToLower(strings.TrimSpace(key))
		p, ok := available[normalized]
		if !ok {
			return nil, fmt.Errorf("unknown platform %q", key)
		}
		canonical := canonicalPlatformKey(normalized)
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		out = append(out, platformEntry{key: canonical, platform: p})
	}
	return out, nil
}

func collectPlatform(ctx context.Context, platformKey string, p platform.Platform, keywords []string, opts cliOptions) ([]exportRow, error) {
	rows := make([]exportRow, 0, opts.MaxPerPlatform)
	seen := map[string]bool{}
	for _, keyword := range keywords {
		if len(rows) >= opts.MaxPerPlatform {
			break
		}
		for page := 1; page <= opts.PageLimit && len(rows) < opts.MaxPerPlatform; page++ {
			if err := ctx.Err(); err != nil {
				return rows, err
			}
			requestCtx, cancel := context.WithTimeout(ctx, opts.RequestTimeout)
			items, err := p.Search(requestCtx, keyword, page)
			cancel()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[%s] keyword=%s page=%d error=%v\n", platformKey, keyword, page, err)
				break
			}
			if len(items) == 0 {
				break
			}
			added := 0
			for _, item := range items {
				key := productKey(platformKey, item)
				if key == "" || seen[key] {
					continue
				}
				seen[key] = true
				rows = append(rows, exportRow{
					PlatformKey: platformKey,
					Keyword:     keyword,
					Page:        page,
					Product:     item,
				})
				added++
				if len(rows) >= opts.MaxPerPlatform {
					break
				}
			}
			fmt.Fprintf(os.Stderr, "[%s] keyword=%s page=%d items=%d added=%d total=%d\n", platformKey, keyword, page, len(items), added, len(rows))
			if added == 0 {
				break
			}
			if opts.Delay > 0 {
				select {
				case <-ctx.Done():
					return rows, ctx.Err()
				case <-time.After(opts.Delay):
				}
			}
		}
	}
	return rows, nil
}

func writeCSV(path string, rows []exportRow, withBOM bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if withBOM {
		if _, err := file.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
			return err
		}
	}
	writer := csv.NewWriter(file)
	header := []string{"platform_key", "platform", "keyword", "page", "id", "name", "company", "price", "price_label", "tags", "url", "brief"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		price := ""
		if row.Product.Price != nil {
			price = *row.Product.Price
		}
		record := []string{
			row.PlatformKey,
			row.Product.Platform,
			row.Keyword,
			fmt.Sprintf("%d", row.Page),
			row.Product.ID,
			row.Product.Name,
			row.Product.Company,
			price,
			row.Product.PriceLabel,
			strings.Join(row.Product.Tags, "|"),
			row.Product.URL,
			row.Product.Brief,
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func splitList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func canonicalPlatformKey(key string) string {
	switch key {
	case "xys":
		return "xiaoyusan"
	case "pa":
		return "pingan"
	case "hz":
		return "huize"
	default:
		return key
	}
}

func productKey(platformKey string, item platform.ProductCard) string {
	for _, value := range []string{item.URL, item.ID, item.Name} {
		value = strings.TrimSpace(value)
		if value != "" {
			return platformKey + ":" + value
		}
	}
	return ""
}
