package parsers

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
)

type ProductDetail struct {
	Name    string
	Company string
	Price   string
	Brief   string
	Tags    []string
}

type Parser func(html string) ProductDetail

func GetParser(rawURL string) Parser {
	lower := strings.ToLower(rawURL)
	switch {
	case strings.Contains(lower, "xiaoyusan.com"):
		return ParseXiaoyusan
	case strings.Contains(lower, "pingan.com"):
		return ParsePingan
	case strings.Contains(lower, "huize.com"):
		return ParseHuize
	case strings.Contains(lower, "shenlanbao.com"):
		return ParseShenlanbao
	case strings.Contains(lower, "zhongan.com"):
		return ParseZhongan
	default:
		return ParseFallback
	}
}

func ParseXiaoyusan(pageHTML string) ProductDetail {
	detail := ProductDetail{}
	if raw := extractWindowObject(pageHTML, "window.staticData"); raw != "" {
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err == nil {
			if cfg, ok := data["insuranceConfig"].(map[string]any); ok {
				detail.Name = firstString(cfg, "title", "productname")
			}
			if cfg, ok := data["companyConfig"].(map[string]any); ok {
				detail.Company = firstString(cfg, "companyname")
			}
			if versions, ok := data["versions"].(map[string]any); ok {
				if list, ok := versions["list"].([]any); ok {
					parts := make([]string, 0, 3)
					for _, item := range list {
						duty, ok := item.(map[string]any)
						if !ok {
							continue
						}
						name := firstString(duty, "duty", "name")
						coverage := firstString(duty, "coverage", "amount")
						if name != "" && coverage != "" {
							parts = append(parts, name+coverage)
						}
						if len(parts) >= 3 {
							break
						}
					}
					detail.Brief = strings.Join(parts, "，")
				}
			}
		}
	}
	if detail.Price == "" {
		detail.Price = ParsePriceFromText(pageHTML)
	}
	return fallbackFill(pageHTML, detail)
}

func ParsePingan(pageHTML string) ProductDetail {
	detail := ProductDetail{
		Name:    titleText(pageHTML),
		Company: "中国平安",
		Price:   ParsePriceFromText(pageHTML),
		Brief:   metaDescription(pageHTML),
	}
	if company := regexp.MustCompile(`中国平安[\p{Han}]{0,16}?保险[\p{Han}]{0,16}?公司`).FindString(pageHTML); company != "" {
		detail.Company = company
	}
	detail.Name = regexp.MustCompile(`[_\-|].*?(平安|商城).*$`).ReplaceAllString(detail.Name, "")
	return fallbackFill(pageHTML, detail)
}

func ParseHuize(pageHTML string) ProductDetail {
	detail := ProductDetail{
		Name:    regexp.MustCompile(`\s*-\s*.*?(慧择|保险网).*$`).ReplaceAllString(titleText(pageHTML), ""),
		Company: companyFromText(pageHTML),
		Price:   ParsePriceFromText(pageHTML),
		Brief:   metaDescription(pageHTML),
	}
	return fallbackFill(pageHTML, detail)
}

func ParseShenlanbao(pageHTML string) ProductDetail {
	detail := ProductDetail{
		Name:    regexp.MustCompile(`\s*[-_|].*?(深蓝保|测评).*$`).ReplaceAllString(titleText(pageHTML), ""),
		Company: companyFromText(pageHTML),
		Price:   ParsePriceFromText(pageHTML),
		Brief:   metaDescription(pageHTML),
	}
	return fallbackFill(pageHTML, detail)
}

func ParseZhongan(pageHTML string) ProductDetail {
	detail := ProductDetail{
		Name:    regexp.MustCompile(`\s*[-_|].*?(众安|保险).*$`).ReplaceAllString(titleText(pageHTML), ""),
		Company: "众安保险",
		Price:   ParsePriceFromText(pageHTML),
		Brief:   metaDescription(pageHTML),
	}
	return fallbackFill(pageHTML, detail)
}

func ParseFallback(pageHTML string) ProductDetail {
	return fallbackFill(pageHTML, ProductDetail{
		Name:  titleText(pageHTML),
		Price: ParsePriceFromText(pageHTML),
		Brief: metaDescription(pageHTML),
	})
}

func ParsePriceFromText(text string) string {
	patterns := []struct {
		re     *regexp.Regexp
		format string
	}{
		{regexp.MustCompile(`(\d+(?:\.\d+)?)\s*元/(年|月)起`), "{val}元/{unit}起"},
		{regexp.MustCompile(`(\d+(?:\.\d+)?)\s*元/年`), "{val}元/年"},
		{regexp.MustCompile(`[¥￥]\s*(\d+(?:\.\d+)?)`), "¥{val}"},
		{regexp.MustCompile(`(\d+(?:\.\d+)?)\s*元起`), "{val}元起"},
		{regexp.MustCompile(`首月\s*(\d+(?:\.\d+)?)\s*元`), "首月{val}元"},
	}
	for _, pattern := range patterns {
		if match := pattern.re.FindStringSubmatch(text); len(match) > 1 {
			formatted := strings.ReplaceAll(pattern.format, "{val}", match[1])
			if len(match) > 2 {
				formatted = strings.ReplaceAll(formatted, "{unit}", match[2])
			}
			return formatted
		}
	}
	return ""
}

func ExtractTags(name, brief string) []string {
	combined := name + brief
	keywords := []string{
		"百万医疗", "保证续保", "重疾险", "意外险", "寿险",
		"年金险", "防癌险", "医疗险", "健康险", "0免赔",
		"20年续保", "终身", "长期医疗",
	}
	tags := make([]string, 0, 4)
	for _, kw := range keywords {
		if strings.Contains(combined, kw) && !contains(tags, kw) {
			tags = append(tags, kw)
		}
		if len(tags) >= 4 {
			break
		}
	}
	return tags
}

func fallbackFill(pageHTML string, detail ProductDetail) ProductDetail {
	detail.Name = strings.TrimSpace(html.UnescapeString(detail.Name))
	if detail.Name == "" {
		detail.Name = titleText(pageHTML)
	}
	if detail.Price == "" {
		detail.Price = ParsePriceFromText(pageHTML)
	}
	if detail.Brief == "" {
		detail.Brief = metaDescription(pageHTML)
	}
	if detail.Company == "" {
		detail.Company = companyFromText(pageHTML)
	}
	detail.Brief = truncate(detail.Brief, 100)
	detail.Tags = ExtractTags(detail.Name, detail.Brief)
	return detail
}

func titleText(pageHTML string) string {
	m := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`).FindStringSubmatch(pageHTML)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(stripTags(html.UnescapeString(m[1])))
}

func metaDescription(pageHTML string) string {
	re := regexp.MustCompile(`(?is)<meta\s+[^>]*(?:name|property)=["']description["'][^>]*content=["'](.*?)["'][^>]*>`)
	m := re.FindStringSubmatch(pageHTML)
	if len(m) < 2 {
		re = regexp.MustCompile(`(?is)<meta\s+[^>]*content=["'](.*?)["'][^>]*(?:name|property)=["']description["'][^>]*>`)
		m = re.FindStringSubmatch(pageHTML)
	}
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(stripTags(html.UnescapeString(m[1])))
}

func companyFromText(text string) string {
	limit := text
	if len([]rune(limit)) > 3000 {
		limit = string([]rune(limit)[:3000])
	}
	if m := regexp.MustCompile(`[\p{Han}]{2,8}(?:保险|人寿|财险|健康)`).FindString(limit); m != "" {
		return m
	}
	return ""
}

func stripTags(text string) string {
	return regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(text, "")
}

func firstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := data[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func extractWindowObject(text, marker string) string {
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	start := strings.Index(text[idx:], "{")
	if start < 0 {
		return ""
	}
	start += idx

	depth := 0
	inString := false
	escaped := false
	var quote rune
	for pos, r := range text[start:] {
		abs := start + pos
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				inString = false
			}
			continue
		}
		if r == '"' || r == '\'' {
			inString = true
			quote = r
			continue
		}
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : abs+len(string(r))]
			}
		}
	}
	return ""
}

func truncate(text string, max int) string {
	runes := []rune(strings.TrimSpace(text))
	if max <= 0 || len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max])
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
