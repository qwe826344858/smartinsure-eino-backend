package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"smartinsure-eino-backend/internal/config"
	skillproduct "smartinsure-eino-backend/internal/skill/productdetail"
)

type cliOptions struct {
	Input        string
	Limit        int
	DryRun       bool
	EnsureSchema bool
}

type priceRow struct {
	ProductURL  string
	ProductName string
	Price       string
	PriceLabel  string
}

type importStats struct {
	Total   int
	Updated int
	Missed  int
	Failed  int
}

type priceUpdater interface {
	UpdatePriceByURL(context.Context, skillproduct.UpdateProductDetailPriceInput) (bool, error)
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
	file, err := os.Open(opts.Input)
	if err != nil {
		return err
	}
	defer file.Close()
	rows, err := readPriceRows(file)
	if err != nil {
		return err
	}
	if opts.DryRun {
		stats := importStats{Total: len(rows)}
		if opts.Limit > 0 && stats.Total > opts.Limit {
			stats.Total = opts.Limit
		}
		fmt.Printf("dry_run total=%d input=%s\n", stats.Total, opts.Input)
		return nil
	}

	settings := config.Load()
	if strings.TrimSpace(settings.MySQLDSN) == "" {
		return fmt.Errorf("MYSQL_DSN 未配置")
	}
	repo, err := skillproduct.OpenMySQLDetailRepository(settings.MySQLDSN)
	if err != nil {
		return err
	}
	defer repo.Close()
	if opts.EnsureSchema {
		if err := repo.EnsureSchema(ctx); err != nil {
			return err
		}
	}
	stats := importRows(ctx, repo, rows, opts.Limit)
	fmt.Printf("total=%d updated=%d missed=%d failed=%d input=%s\n", stats.Total, stats.Updated, stats.Missed, stats.Failed, opts.Input)
	if stats.Failed > 0 {
		return fmt.Errorf("import failed rows=%d", stats.Failed)
	}
	return nil
}

func parseArgs(args []string) (cliOptions, error) {
	fs := flag.NewFlagSet("importplatformprices", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts cliOptions
	fs.StringVar(&opts.Input, "input", "logs/platform_products.csv", "CSV input path from exportplatformproducts")
	fs.IntVar(&opts.Limit, "limit", 0, "maximum rows to import; 0 means all")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "parse CSV without writing MySQL")
	fs.BoolVar(&opts.EnsureSchema, "ensure-schema", true, "ensure product detail MySQL schema before importing")
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	opts.Input = strings.TrimSpace(opts.Input)
	if opts.Input == "" {
		return cliOptions{}, fmt.Errorf("input is required")
	}
	if opts.Limit < 0 {
		return cliOptions{}, fmt.Errorf("limit must be >= 0")
	}
	return opts, nil
}

func readPriceRows(r io.Reader) ([]priceRow, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := map[string]int{}
	for i, name := range records[0] {
		header[normalizeHeader(name)] = i
	}
	required := []string{"url", "price", "price_label"}
	for _, key := range required {
		if _, ok := header[key]; !ok {
			return nil, fmt.Errorf("csv missing %s column", key)
		}
	}

	rows := make([]priceRow, 0, len(records)-1)
	for _, record := range records[1:] {
		row := priceRow{
			ProductURL:  csvValue(record, header, "url"),
			ProductName: csvValue(record, header, "name"),
			Price:       csvValue(record, header, "price"),
			PriceLabel:  csvValue(record, header, "price_label"),
		}
		if row.PriceLabel == "" && row.Price != "" {
			row.PriceLabel = row.Price
		}
		if row.ProductURL == "" || (row.Price == "" && row.PriceLabel == "") {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func importRows(ctx context.Context, updater priceUpdater, rows []priceRow, limit int) importStats {
	var stats importStats
	for _, row := range rows {
		if limit > 0 && stats.Total >= limit {
			break
		}
		stats.Total++
		updated, err := updater.UpdatePriceByURL(ctx, skillproduct.UpdateProductDetailPriceInput{
			ProductURL: row.ProductURL,
			Price:      row.Price,
			PriceLabel: row.PriceLabel,
		})
		if err != nil {
			stats.Failed++
			fmt.Fprintf(os.Stderr, "price import failed row=%d url=%s err=%v\n", stats.Total, row.ProductURL, err)
			continue
		}
		if updated {
			stats.Updated++
		} else {
			stats.Missed++
		}
	}
	return stats
}

func normalizeHeader(value string) string {
	return strings.TrimSpace(strings.TrimPrefix(value, "\ufeff"))
}

func csvValue(record []string, header map[string]int, key string) string {
	index, ok := header[key]
	if !ok || index < 0 || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}
