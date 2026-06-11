package platform

import (
	"context"
	"net/url"
	"os"
	"strconv"
)

const xiaoyusanURL = "https://www.xiaoyusan.com/index/searchData"

type Xiaoyusan struct {
	Client HTTPDoer
}

func (Xiaoyusan) Name() string   { return "小雨伞" }
func (Xiaoyusan) Domain() string { return "xiaoyusan.com" }

func (p Xiaoyusan) Search(ctx context.Context, keyword string, page int) ([]ProductCard, error) {
	if page <= 0 {
		page = 1
	}
	form := url.Values{
		"searchType": {"0"},
		"level":      {"1"},
		"scene":      {"h5list"},
		"searchText": {keyword},
		"page":       {strconv.Itoa(page)},
	}
	headers := map[string]string{
		"User-Agent":       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Accept":           "application/json, text/plain, */*",
		"Content-Type":     "application/x-www-form-urlencoded",
		"Referer":          "https://www.xiaoyusan.com/index/searchindex",
		"X-Requested-With": "XMLHttpRequest",
	}
	if cookie := os.Getenv("XYS_COOKIE"); cookie != "" {
		headers["Cookie"] = cookie
	}
	var data xysResponse
	if err := doForm(ctx, p.Client, xiaoyusanURL, headers, form, &data); err != nil {
		return ignoreExternalFailure(err)
	}
	if data.Ret != 0 {
		return []ProductCard{}, nil
	}
	products := make([]ProductCard, 0, len(data.Data.GoodsList))
	for _, item := range data.Data.GoodsList {
		if item.Title == "" || item.Src == "" {
			continue
		}
		price := item.ItemPrice
		if price == "" {
			price = item.Price
		}
		if price != "" {
			price += item.PriceUnit
		}
		tags := []string{}
		if item.InsuranceClassify.Text != "" {
			tags = append(tags, item.InsuranceClassify.Text)
		}
		if item.Classify.Text != "" {
			tags = append(tags, item.Classify.Text)
		}
		products = append(products, ProductCard{
			ID:         "xys_" + item.ProductID,
			Name:       item.Title,
			Company:    ExtractCompany(item.Title),
			Price:      stringPtr(price),
			PriceLabel: firstNonEmpty(price, "查看详情"),
			Tags:       trimList(tags, 3),
			URL:        item.Src,
			Platform:   p.Name(),
			Brief:      joinFirst(item.Advantage, 2),
		})
	}
	return products, nil
}

type xysResponse struct {
	Ret  int `json:"ret"`
	Data struct {
		GoodsList []struct {
			ProductID         string   `json:"productId"`
			Title             string   `json:"title"`
			Src               string   `json:"src"`
			ItemPrice         string   `json:"itemPrice"`
			Price             string   `json:"price"`
			PriceUnit         string   `json:"priceUnit"`
			Advantage         []string `json:"advantage"`
			InsuranceClassify struct {
				Text string `json:"text"`
			} `json:"insuranceClassify"`
			Classify struct {
				Text string `json:"text"`
			} `json:"classify"`
		} `json:"goodsList"`
	} `json:"data"`
}
