package productdetail

import (
	"strings"

	"smartinsure-eino-backend/internal/schema"
)

type tagRule struct {
	name     string
	category string
	terms    []string
}

var (
	insuranceTagRules = []tagRule{
		{name: "医疗险", category: "insurance_tags", terms: []string{"医疗", "百万医疗", "中高端医疗"}},
		{name: "重疾险", category: "insurance_tags", terms: []string{"重疾险", "重大疾病保险", "疾病保险"}},
		{name: "意外险", category: "insurance_tags", terms: []string{"意外险", "意外伤害保险"}},
		{name: "年金险", category: "insurance_tags", terms: []string{"年金", "养老年金"}},
	}
	marketTagRules = []tagRule{
		{name: "高端", category: "market_tags", terms: []string{"高端", "中高端", "特需", "国际部", "vip", "私立医院"}},
		{name: "性价比", category: "market_tags", terms: []string{"性价比", "普惠", "惠民", "低保费"}},
	}
	audienceTagRules = []tagRule{
		{name: "家庭", category: "audience_tags", terms: []string{"家庭", "全家", "家享", "亲子"}},
		{name: "个人", category: "audience_tags", terms: []string{"个人", "个人版"}},
		{name: "儿童", category: "audience_tags", terms: []string{"少儿", "儿童", "宝宝", "学生", "青少年"}},
		{name: "老人", category: "audience_tags", terms: []string{"老人", "老年", "中老年", "银发"}},
		{name: "企业", category: "audience_tags", terms: []string{"企业", "团体", "员工", "雇主"}},
	}
	dutyTagRules = []tagRule{
		{name: "外购药", category: "duty_tags", terms: []string{"外购药"}},
		{name: "特药", category: "duty_tags", terms: []string{"特药", "特定药品"}},
		{name: "质子重离子", category: "duty_tags", terms: []string{"质子重离子"}},
		{name: "门急诊", category: "duty_tags", terms: []string{"门急诊", "门诊", "急诊"}},
		{name: "住院医疗", category: "duty_tags", terms: []string{"住院医疗", "住院费用", "医疗保险金"}},
		{name: "重大疾病", category: "duty_tags", terms: []string{"重大疾病", "重疾"}},
		{name: "健康管理", category: "duty_tags", terms: []string{"健康管理", "健康服务", "就医绿通"}},
		{name: "免赔额", category: "duty_tags", terms: []string{"免赔额", "免赔"}},
		{name: "续保", category: "duty_tags", terms: []string{"续保", "保证续保"}},
	}
)

func InferTags(input DetailInput) TagSet {
	productText := normalizeTagText(productName(input) + " " + input.Platform)
	dutyText := normalizeTagText(dutiesText(input.Detail.Duties))
	allText := strings.TrimSpace(productText + " " + dutyText)

	out := TagSet{
		RelatedDuties: map[string][]string{},
		categories:    map[string]string{},
	}
	add := func(target *[]string, category, tag string) {
		if appendUnique(target, tag) {
			out.categories[tag] = category
		}
	}

	for _, rule := range insuranceTagRules {
		if rule.name == "医疗险" {
			if containsAny(productText, rule.terms) || containsAny(dutyText, []string{"住院医疗", "医疗保险金", "门急诊", "外购药"}) {
				add(&out.InsuranceTags, rule.category, rule.name)
			}
			continue
		}
		if containsAny(productText, rule.terms) || (rule.name == "意外险" && containsAny(dutyText, []string{"意外", "伤残", "意外身故", "意外医疗"})) {
			add(&out.InsuranceTags, rule.category, rule.name)
		}
	}
	for _, rule := range marketTagRules {
		if containsAny(allText, rule.terms) {
			add(&out.MarketTags, rule.category, rule.name)
		}
	}
	for _, rule := range audienceTagRules {
		if containsAny(allText, rule.terms) {
			add(&out.AudienceTags, rule.category, rule.name)
		}
	}
	for _, duty := range input.Detail.Duties {
		text := normalizeTagText(duty.Name + " " + duty.Coverage + " " + duty.Description)
		for _, rule := range dutyTagRules {
			if containsAny(text, rule.terms) {
				add(&out.DutyTags, rule.category, rule.name)
				appendUniqueToMap(out.RelatedDuties, rule.name, strings.TrimSpace(duty.Name))
			}
		}
	}

	matchRate := input.Detail.MatchRate
	if matchRate >= 0.8 {
		appendUnique(&out.QualityTags, "高匹配")
	} else if matchRate > 0 && matchRate < 0.6 {
		appendUnique(&out.QualityTags, "低匹配")
	}
	out.Tags = concatUnique(out.InsuranceTags, out.MarketTags, out.AudienceTags, out.DutyTags)
	fillDefaultRelatedDuties(&out, input.Detail.Duties)
	return out
}

func dutyTagsForDuty(duty schema.DutyItem) []string {
	text := normalizeTagText(duty.Name + " " + duty.Coverage + " " + duty.Description)
	tags := make([]string, 0, 3)
	for _, rule := range dutyTagRules {
		if containsAny(text, rule.terms) {
			appendUnique(&tags, rule.name)
		}
	}
	return tags
}

func normalizeTagText(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}

func dutiesText(duties []schema.DutyItem) string {
	var b strings.Builder
	for _, duty := range duties {
		b.WriteString(duty.Name)
		b.WriteByte(' ')
		b.WriteString(duty.Coverage)
		b.WriteByte(' ')
		b.WriteString(duty.Description)
		b.WriteByte(' ')
	}
	return b.String()
}

func containsAny(text string, terms []string) bool {
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(text, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func concatUnique(groups ...[]string) []string {
	out := make([]string, 0)
	for _, group := range groups {
		for _, value := range group {
			appendUnique(&out, value)
		}
	}
	return out
}

func appendUnique(values *[]string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, existing := range *values {
		if existing == value {
			return false
		}
	}
	*values = append(*values, value)
	return true
}

func appendUniqueToMap(values map[string][]string, key, value string) {
	if values == nil || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
		return
	}
	items := values[key]
	if appendUnique(&items, value) {
		values[key] = items
	}
}

func fillDefaultRelatedDuties(tags *TagSet, duties []schema.DutyItem) {
	if tags == nil || len(duties) == 0 {
		return
	}
	defaultDuties := firstDutyNames(duties, 3)
	for _, tag := range tags.Tags {
		if len(tags.RelatedDuties[tag]) > 0 {
			continue
		}
		tags.RelatedDuties[tag] = append([]string(nil), defaultDuties...)
	}
}

func firstDutyNames(duties []schema.DutyItem, limit int) []string {
	if limit <= 0 {
		limit = 3
	}
	out := make([]string, 0, limit)
	for _, duty := range duties {
		if appendUnique(&out, strings.TrimSpace(duty.Name)) && len(out) >= limit {
			break
		}
	}
	return out
}
