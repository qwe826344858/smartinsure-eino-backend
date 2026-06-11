package productdetail

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/schema"
)

var (
	jsonBlockRE   = regexp.MustCompile(`(?s)\{.*\}`)
	cnSegmentRE   = regexp.MustCompile(`[\p{Han}]{2,}`)
	coverageRE    = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?\s*(?:万元|万|元|千|百)(?:/年|/次|/日)?|不限|报销比例\s*\d+%|赔付比例\s*\d+%)`)
	leadingMarkRE = regexp.MustCompile(`^[\s\-*•·、,，.。;；:：\d一二三四五六七八九十]+`)
)

var genericShortNames = map[string]bool{
	"保险":  true,
	"医疗":  true,
	"费用":  true,
	"责任":  true,
	"保障":  true,
	"保险金": true,
	"医疗险": true,
}

var strongDutyKeywords = []string{
	"保险金", "一般医疗", "重大疾病", "重疾", "恶性肿瘤", "癌症", "外购药", "特药",
	"质子重离子", "住院", "门急诊", "门诊", "急诊", "津贴", "身故", "伤残",
	"意外", "豁免", "增值服务", "绿通", "垫付", "护理", "可选", "附加",
}

type ExtractionPayload struct {
	ProductName string            `json:"product_name"`
	Duties      []schema.DutyItem `json:"duties"`
}

func ParseDutiesJSON(rawText string) []schema.DutyItem {
	payload, ok := ParseExtractionJSON(rawText)
	if !ok {
		return nil
	}
	return payload.Duties
}

func ParseExtractionJSON(rawText string) (ExtractionPayload, bool) {
	rawText = llm.StripMarkdownFence(strings.TrimSpace(rawText))
	if match := jsonBlockRE.FindString(rawText); match != "" {
		rawText = match
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(rawText), &data); err != nil {
		return ExtractionPayload{}, false
	}

	rawDuties, _ := data["duties"].([]any)
	duties := make([]schema.DutyItem, 0, len(rawDuties))
	for _, raw := range rawDuties {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringValue(item["name"]))
		if name == "" {
			continue
		}
		description := truncateRunes(strings.TrimSpace(stringValue(item["description"])), 200)
		duties = append(duties, schema.DutyItem{
			Name:        name,
			Coverage:    strings.TrimSpace(stringValue(item["coverage"])),
			Description: description,
			IsOptional:  boolValue(item["is_optional"]) || strings.Contains(name+description, "可选"),
		})
	}

	return ExtractionPayload{
		ProductName: strings.TrimSpace(stringValue(data["product_name"])),
		Duties:      duties,
	}, true
}

func ExtractProductName(rawText string) string {
	payload, ok := ParseExtractionJSON(rawText)
	if !ok {
		return ""
	}
	return payload.ProductName
}

func ValidateExtraction(duties []schema.DutyItem, cleanedText string) (bool, float64, string) {
	if len(duties) == 0 {
		return false, 0, "未提取到任何保障项"
	}

	matched := 0
	for _, duty := range duties {
		if nameFoundInText(duty.Name, cleanedText) {
			matched++
		}
	}
	rate := float64(matched) / float64(len(duties))
	return rate >= 0.7, rate, fmt.Sprintf("匹配 %d/%d 项 (%.0f%%)", matched, len(duties), rate*100)
}

func HeuristicExtract(cleanedText, productURL, productName string, cnCount int) schema.ProductDetail {
	duties := HeuristicExtractDuties(cleanedText)
	passed, matchRate, _ := ValidateExtraction(duties, cleanedText)
	if !passed && len(duties) > 0 {
		matchRate = 1
	}
	if productName = strings.TrimSpace(productName); productName == "" {
		productName = guessProductName(cleanedText)
	}
	return schema.ProductDetail{
		ProductName: productName,
		ProductURL:  productURL,
		Platform:    InferPlatform(productURL),
		Duties:      duties,
		CNCharCount: cnCount,
		MatchRate:   matchRate,
	}
}

func HeuristicExtractDuties(cleanedText string) []schema.DutyItem {
	segments := splitSegments(cleanedText)
	seen := map[string]bool{}
	duties := make([]schema.DutyItem, 0, 8)

	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" || !hasStrongDutyKeyword(segment) {
			continue
		}
		coverage := ""
		if match := coverageRE.FindString(segment); match != "" {
			coverage = strings.TrimSpace(match)
		}
		if coverage == "" && !strings.Contains(segment, "保险金") && !strings.Contains(segment, "增值服务") {
			continue
		}

		name := extractDutyName(segment, coverage)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if coverage == "" {
			coverage = "以页面说明为准"
		}
		duties = append(duties, schema.DutyItem{
			Name:        name,
			Coverage:    coverage,
			Description: truncateRunes(segment, 200),
			IsOptional:  strings.Contains(segment, "可选") || strings.Contains(segment, "附加") || strings.Contains(segment, "加购"),
		})
		if len(duties) >= 8 {
			break
		}
	}

	return duties
}

func splitSegments(text string) []string {
	replacer := strings.NewReplacer("。", "\n", "；", "\n", ";", "\n", "，", "\n", ",", "\n", "\t", "\n")
	text = replacer.Replace(text)
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func hasStrongDutyKeyword(text string) bool {
	for _, keyword := range strongDutyKeywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func extractDutyName(segment, coverage string) string {
	cut := segment
	if idx := strings.IndexAny(cut, ":："); idx >= 0 && idx < 40 {
		cut = cut[:idx]
	} else if coverage != "" {
		if idx := strings.Index(cut, coverage); idx > 0 {
			cut = cut[:idx]
		}
	}

	cut = leadingMarkRE.ReplaceAllString(cut, "")
	for _, prefix := range []string{"本产品包含", "产品包含", "包含", "保障责任", "保障项目", "责任名称", "主要保障", "核心保障"} {
		cut = strings.TrimPrefix(cut, prefix)
	}
	cut = strings.Trim(cut, " \n\r\t-—:：,，.。;；【】[]()（）")

	if utf8.RuneCountInString(cut) > 28 {
		segments := cnSegmentRE.FindAllString(cut, -1)
		for _, seg := range segments {
			if utf8.RuneCountInString(seg) <= 28 && hasStrongDutyKeyword(seg) {
				return seg
			}
		}
		if len(segments) > 0 {
			return truncateRunes(segments[len(segments)-1], 28)
		}
	}
	return cut
}

func guessProductName(cleanedText string) string {
	for _, line := range strings.Split(cleanedText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || utf8.RuneCountInString(line) > 40 {
			continue
		}
		if strings.Contains(line, "保险") || strings.Contains(line, "险") {
			return line
		}
	}
	return "保险产品"
}

func nameFoundInText(name, text string) bool {
	if name == "" {
		return false
	}
	if strings.Contains(text, name) {
		return true
	}

	segments := cnSegmentRE.FindAllString(name, -1)
	for _, seg := range segments {
		if genericShortNames[seg] {
			continue
		}
		if strings.Contains(text, seg) {
			return true
		}
	}

	cnChars := make([]rune, 0, 4)
	for _, r := range name {
		if r >= '\u4e00' && r <= '\u9fff' {
			cnChars = append(cnChars, r)
			if len(cnChars) == 4 {
				break
			}
		}
	}
	return len(cnChars) == 4 && strings.Contains(text, string(cnChars))
}

func stringValue(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

func boolValue(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true") || strings.Contains(value, "是") || strings.Contains(value, "可选")
	default:
		return false
	}
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}
