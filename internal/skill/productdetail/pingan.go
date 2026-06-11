package productdetail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"smartinsure-eino-backend/internal/htmlcleaner"
)

var pinganProductCodeRE = regexp.MustCompile(`(?i)\b(?:ZP|MP|P)\d{5,}\b`)

func (s *Service) fetchPlatformDetailSource(ctx context.Context, productURL, productName string) (PreparedDetailSource, bool) {
	if source, ok := s.fetchPinganDetailSource(ctx, productURL, productName); ok {
		return source, true
	}
	return PreparedDetailSource{ResolvedProductName: strings.TrimSpace(productName)}, false
}

func (s *Service) fetchPinganDetailSource(ctx context.Context, productURL, productName string) (PreparedDetailSource, bool) {
	endpoint, ok := buildPinganProductInfoURL(productURL)
	if !ok {
		return PreparedDetailSource{ResolvedProductName: strings.TrimSpace(productName)}, false
	}
	raw, err := s.fetcher.Fetch(ctx, endpoint)
	if err != nil || strings.TrimSpace(raw) == "" {
		return PreparedDetailSource{ResolvedProductName: strings.TrimSpace(productName)}, false
	}
	text, resolvedProductName, ok := parsePinganProductInfoText(raw, productName)
	if !ok {
		return PreparedDetailSource{ResolvedProductName: strings.TrimSpace(productName)}, false
	}
	return PreparedDetailSource{
		SourceURL:           endpoint,
		SourceType:          "platform_api",
		SourceFormat:        "json",
		RawPayload:          raw,
		CleanedText:         text,
		CNCharCount:         htmlcleaner.CountChinese(text),
		ResolvedProductName: resolvedProductName,
		FetchedAt:           s.now(),
	}, true
}

func buildPinganProductInfoURL(productURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(productURL))
	if err != nil {
		return "", false
	}
	if !strings.Contains(strings.ToLower(parsed.Host), "pingan.com") {
		return "", false
	}

	productCode := extractPinganProductCode(parsed)
	if productCode == "" {
		return "", false
	}

	scheme := parsed.Scheme
	if scheme == "" {
		scheme = "https"
	}
	host := parsed.Host
	if host == "" {
		host = "baoxian.pingan.com"
	}
	endpoint := url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   "/pa18shopnst/do/era/core/base/productInfo",
	}
	query := endpoint.Query()
	query.Set("productCode", productCode)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), true
}

func extractPinganProductCode(parsed *url.URL) string {
	candidates := []string{
		parsed.Query().Get("productCode"),
		parsed.Query().Get("product_code"),
		parsed.Query().Get("code"),
		parsed.Fragment,
		parsed.Path,
		parsed.RawQuery,
	}
	for _, candidate := range candidates {
		if match := pinganProductCodeRE.FindString(candidate); match != "" {
			return strings.ToUpper(match)
		}
	}
	return ""
}

func parsePinganProductInfoText(rawJSON, fallbackProductName string) (string, string, bool) {
	var resp pinganProductInfoResponse
	decoder := json.NewDecoder(strings.NewReader(rawJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&resp); err != nil {
		return "", "", false
	}

	productName := strings.TrimSpace(resp.ProductName)
	if productName == "" {
		productName = strings.TrimSpace(fallbackProductName)
	}
	duties := flattenPinganDuties(resp)
	if productName == "" || len(duties) == 0 {
		return "", "", false
	}

	text := formatPinganProductInfoText(resp, productName, duties)
	if htmlcleaner.CountChinese(text) == 0 {
		return "", "", false
	}
	return text, productName, true
}

func formatPinganProductInfoText(resp pinganProductInfoResponse, productName string, duties []pinganTextDuty) string {
	var b strings.Builder
	b.WriteString("产品名称：")
	b.WriteString(productName)
	b.WriteString("\n商品渠道：平安保险商城\n")
	if productCode := firstText(resp.ProductCode, firstPackageProductCode(resp.PackageList)); productCode != "" {
		b.WriteString("产品编码：")
		b.WriteString(productCode)
		b.WriteString("\n")
	}
	if resp.TechnicProductCode != "" {
		b.WriteString("技术产品编码：")
		b.WriteString(resp.TechnicProductCode)
		b.WriteString("\n")
	}
	if resp.LeastApplicantAge > 0 || resp.TopApplicantAge > 0 {
		b.WriteString("投保年龄：")
		if resp.LeastApplicantAge > 0 {
			b.WriteString(fmt.Sprintf("%d周岁起", resp.LeastApplicantAge))
		}
		if resp.TopApplicantAge > 0 {
			b.WriteString(fmt.Sprintf("，最高%d周岁", resp.TopApplicantAge))
		}
		b.WriteString("\n")
	}
	if resp.IsSupportAutoRenew != "" || resp.RenewalGracePeriod > 0 {
		b.WriteString("续保信息：")
		if resp.IsSupportAutoRenew == "1" || strings.EqualFold(resp.IsSupportAutoRenew, "Y") {
			b.WriteString("支持自动续保")
		} else if resp.IsSupportAutoRenew != "" {
			b.WriteString("不支持自动续保")
		}
		if resp.RenewalGracePeriod > 0 {
			b.WriteString(fmt.Sprintf("，续保宽限期%d天", resp.RenewalGracePeriod))
		}
		b.WriteString("\n")
	}
	b.WriteString("保障责任：\n")
	for _, duty := range duties {
		b.WriteString(duty.Name)
		b.WriteString("：保额/限额")
		b.WriteString(duty.Coverage)
		if duty.Description != "" {
			b.WriteString("，")
			b.WriteString(duty.Description)
		}
		if duty.PlanName != "" {
			b.WriteString("，所属条款：")
			b.WriteString(duty.PlanName)
		}
		if duty.IsOptional {
			b.WriteString("，可选责任")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func flattenPinganDuties(resp pinganProductInfoResponse) []pinganTextDuty {
	seen := map[string]bool{}
	duties := make([]pinganTextDuty, 0, 8)
	for _, pkg := range resp.PackageList {
		for _, plan := range pkg.PlanInfoList {
			for _, duty := range plan.DutyInfoList {
				item, ok := convertPinganDuty(plan, duty)
				if !ok {
					continue
				}
				key := item.Name + "\x00" + item.Coverage + "\x00" + item.Description
				if seen[key] {
					continue
				}
				seen[key] = true
				duties = append(duties, item)
				if len(duties) >= 12 {
					return duties
				}
			}
		}
	}
	return duties
}

func convertPinganDuty(plan pinganPlanInfo, duty pinganDutyInfo) (pinganTextDuty, bool) {
	if strings.TrimSpace(duty.IsShowDuty) == "0" {
		return pinganTextDuty{}, false
	}

	name := strings.TrimSpace(firstText(duty.DutyName, duty.DutyChineseName))
	if name == "" {
		return pinganTextDuty{}, false
	}
	description := truncateRunes(strings.TrimSpace(duty.DutyDesc), 200)
	coverage := pinganDutyCoverage(duty, name+" "+description)
	return pinganTextDuty{
		Name:        name,
		Coverage:    coverage,
		Description: description,
		PlanName:    strings.TrimSpace(plan.PlanName),
		IsOptional:  isPinganOptionalDuty(duty),
	}, true
}

func pinganDutyCoverage(duty pinganDutyInfo, text string) string {
	for _, amount := range []*float64{
		duty.TotalInsuredAmount,
		duty.InsuredAmount,
		duty.FixAmount,
		duty.MaximumAmount,
		duty.DutyMaximumAmount,
	} {
		if formatted := formatPinganAmount(amount); formatted != "" {
			return formatted
		}
	}
	if match := coverageRE.FindString(text); match != "" {
		return strings.TrimSpace(match)
	}
	return "以页面说明为准"
}

func formatPinganAmount(amount *float64) string {
	if amount == nil || *amount <= 0 {
		return ""
	}
	value := *amount
	if value >= 10000 {
		wan := value / 10000
		return trimFloat(wan) + "万"
	}
	return trimFloat(value) + "元"
}

func trimFloat(value float64) string {
	text := fmt.Sprintf("%.2f", value)
	text = strings.TrimRight(text, "0")
	return strings.TrimRight(text, ".")
}

func isPinganOptionalDuty(duty pinganDutyInfo) bool {
	text := duty.DutyName + duty.DutyChineseName + duty.DutyDesc
	if strings.Contains(text, "可选") || strings.Contains(text, "自选") || strings.Contains(text, "加购") {
		return true
	}
	return strings.TrimSpace(duty.ForceSelected) == "0" || strings.TrimSpace(duty.RequiredCoverage) == "0"
}

func firstPackageProductCode(packages []pinganPackage) string {
	for _, pkg := range packages {
		if strings.TrimSpace(pkg.ProductCode) != "" {
			return strings.TrimSpace(pkg.ProductCode)
		}
	}
	return ""
}

func firstText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

type pinganProductInfoResponse struct {
	ProductCode        string          `json:"productCode"`
	ProductName        string          `json:"productName"`
	TechnicProductCode string          `json:"technicProductCode"`
	IsSupportAutoRenew string          `json:"isSupportAutoRenew"`
	LeastApplicantAge  int             `json:"leastApplicantAge"`
	TopApplicantAge    int             `json:"topApplicantAge"`
	RenewalGracePeriod int             `json:"renewalGracePeriod"`
	PackageList        []pinganPackage `json:"packageList"`
}

type pinganPackage struct {
	ProductCode  string           `json:"productCode"`
	PlanInfoList []pinganPlanInfo `json:"planInfoList"`
}

type pinganPlanInfo struct {
	PlanName     string           `json:"planName"`
	DutyInfoList []pinganDutyInfo `json:"dutyInfoList"`
}

type pinganDutyInfo struct {
	IsShowDuty         string   `json:"isShowDuty"`
	DutyName           string   `json:"dutyName"`
	DutyChineseName    string   `json:"dutyChineseName"`
	DutyDesc           string   `json:"dutyDesc"`
	TotalInsuredAmount *float64 `json:"totalInsuredAmount"`
	InsuredAmount      *float64 `json:"insuredAmount"`
	FixAmount          *float64 `json:"fixAmount"`
	MaximumAmount      *float64 `json:"maximumAmount"`
	DutyMaximumAmount  *float64 `json:"dutyMaximumAmount"`
	ForceSelected      string   `json:"forceSelected"`
	RequiredCoverage   string   `json:"requiredCoverage"`
}

type pinganTextDuty struct {
	Name        string
	Coverage    string
	Description string
	PlanName    string
	IsOptional  bool
}
