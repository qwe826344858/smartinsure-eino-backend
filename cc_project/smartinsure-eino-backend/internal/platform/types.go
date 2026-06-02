package platform

import "context"

// ProductCard is the stable product-search DTO used by platform adapters.
// It mirrors the frontend-facing card contract from the Python backend.
type ProductCard struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Company    string   `json:"company,omitempty"`
	Price      *string  `json:"price,omitempty"`
	PriceLabel string   `json:"price_label"`
	Tags       []string `json:"tags,omitempty"`
	URL        string   `json:"url"`
	Platform   string   `json:"platform"`
	Brief      string   `json:"brief,omitempty"`
}

// Platform is implemented by each insurance marketplace adapter.
type Platform interface {
	Name() string
	Domain() string
	Search(ctx context.Context, keyword string, page int) ([]ProductCard, error)
}
