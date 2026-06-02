package platform

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

const huizeURL = "https://search.huize.com/api/v4/pc/search/product/list"

type Huize struct {
	Client HTTPDoer
}

func (Huize) Name() string   { return "慧择" }
func (Huize) Domain() string { return "huize.com" }

func (p Huize) Search(ctx context.Context, keyword string, page int) ([]ProductCard, error) {
	if page <= 0 {
		page = 1
	}
	body := map[string]any{
		"pageIndex":    page,
		"pageSize":     10,
		"searchName":   keyword,
		"insureMinAge": nil,
		"insureMaxAge": nil,
		"sortType":     1,
	}
	headers := map[string]string{
		"User-Agent":   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Accept":       "application/json, text/plain, */*",
		"Content-Type": "application/json;charset=UTF-8",
		"Origin":       "https://search.huize.com",
		"Referer":      "https://search.huize.com/chanpin/" + url.PathEscape(keyword),
	}
	var data huizeResponse
	if err := doJSON(ctx, p.Client, http.MethodPost, huizeURL, headers, body, &data); err != nil {
		return ignoreExternalFailure(err)
	}
	products := make([]ProductCard, 0, len(data.Data))
	for _, item := range data.Data {
		if item.ProductName == "" || item.PCLocationURL == "" {
			continue
		}
		price := ""
		if item.DefaultPrice != nil && *item.DefaultPrice > 0 {
			priceYuan := *item.DefaultPrice / 100
			price = fmt.Sprintf("%g元/年起", priceYuan)
		}
		tags := []string{item.SecondInsuranceCategoryName}
		for _, rule := range item.InsuranceRule {
			if rule.RuleName == "投保年龄" {
				tags = append(tags, rule.RuleValue)
				break
			}
		}
		products = append(products, ProductCard{
			ID:         fmt.Sprintf("hz_%v", item.ProductID),
			Name:       item.ProductName,
			Company:    item.CompanyName,
			Price:      stringPtr(price),
			PriceLabel: firstNonEmpty(price, "查看详情"),
			Tags:       trimList(tags, 3),
			URL:        item.PCLocationURL,
			Platform:   p.Name(),
			Brief:      firstNonEmpty(item.Summary, joinFirst(item.FeatureContent, 2)),
		})
	}
	return products, nil
}

type huizeResponse struct {
	Total int `json:"total"`
	Data  []struct {
		ProductID                   any      `json:"productId"`
		ProductName                 string   `json:"productName"`
		PCLocationURL               string   `json:"pcLocationUrl"`
		DefaultPrice                *float64 `json:"defaultPrice"`
		CompanyName                 string   `json:"companyName"`
		FeatureContent              []string `json:"featureContent"`
		Summary                     string   `json:"summary"`
		SecondInsuranceCategoryName string   `json:"secondInsuranceCategoryName"`
		InsuranceRule               []struct {
			RuleName  string `json:"ruleName"`
			RuleValue string `json:"ruleValue"`
		} `json:"insuranceRule"`
	} `json:"data"`
}
