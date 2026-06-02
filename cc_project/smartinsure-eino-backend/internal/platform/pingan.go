package platform

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const pinganURL = "https://baoxian.pingan.com/pa18shopnst/do/era/shopProduct/search"

var pinganKeywordMap = map[string]string{"医疗": "医疗险", "重疾": "重疾险", "意外": "意外险"}

type Pingan struct {
	Client HTTPDoer
}

func (Pingan) Name() string   { return "平安保险" }
func (Pingan) Domain() string { return "baoxian.pingan.com" }

func (p Pingan) Search(ctx context.Context, keyword string, page int) ([]ProductCard, error) {
	searchKeyword := keyword
	if mapped, ok := pinganKeywordMap[keyword]; ok {
		searchKeyword = mapped
	}
	endpoint := pinganURL + "?keyword=" + url.QueryEscape(searchKeyword)
	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		"Accept":     "application/json",
		"Referer":    "https://baoxian.pingan.com/pa18shopnst/nstShop/index.html",
	}
	var data pinganResponse
	if err := doRequest(ctx, p.Client, http.MethodGet, endpoint, headers, nil, &data); err != nil {
		return ignoreExternalFailure(err)
	}
	if data.ResultCode != "200" {
		return []ProductCard{}, nil
	}
	products := make([]ProductCard, 0, len(data.Data))
	for _, item := range data.Data {
		if item.ProductName == "" || item.ProductURL == "" {
			continue
		}
		price := ""
		if item.ProductPrice != nil {
			price = fmt.Sprintf("%g%s", *item.ProductPrice, item.PriceUnit)
		}
		products = append(products, ProductCard{
			ID:         "pa_" + item.ProductCode,
			Name:       item.ProductName,
			Company:    "平安保险",
			Price:      stringPtr(price),
			PriceLabel: firstNonEmpty(price, "查看详情"),
			Tags:       []string{"平安"},
			URL:        item.ProductURL,
			Platform:   p.Name(),
			Brief:      strings.ReplaceAll(item.ProductDesc, "\n", "；"),
		})
	}
	return products, nil
}

type pinganResponse struct {
	ResultCode string `json:"resultCode"`
	Data       []struct {
		ProductCode  string   `json:"productCode"`
		ProductName  string   `json:"productName"`
		ProductURL   string   `json:"productUrl"`
		ProductPrice *float64 `json:"productPrice"`
		PriceUnit    string   `json:"priceUnit"`
		ProductDesc  string   `json:"productDesc"`
	} `json:"data"`
}
