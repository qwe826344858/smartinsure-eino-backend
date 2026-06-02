package platform

import (
	"regexp"
	"strconv"
)

var keywordMapping = map[string]string{
	"医疗保险": "医疗", "医疗险": "医疗", "百万医疗": "医疗", "百万医疗险": "医疗", "健康险": "医疗", "住院医疗": "医疗", "补充医疗": "医疗", "高端医疗": "医疗",
	"重疾险": "重疾", "重大疾病": "重疾", "重大疾病险": "重疾", "大病险": "重疾", "大病保险": "重疾",
	"意外险": "意外", "意外保险": "意外", "意外伤害": "意外", "综合意外": "意外",
	"寿险": "寿险", "人寿保险": "寿险", "定期寿险": "定期寿险", "终身寿险": "终身寿险", "定寿": "定期寿险",
	"年金险": "年金", "年金保险": "年金", "养老险": "养老", "养老保险": "养老", "养老金": "养老",
	"教育金": "教育金", "教育金保险": "教育金", "教育险": "教育金",
	"防癌险": "防癌", "防癌医疗": "防癌", "防癌医疗险": "防癌",
	"少儿险": "少儿", "少儿保险": "少儿", "儿童保险": "少儿", "儿童险": "少儿",
	"旅游险": "旅游", "旅行险": "旅游", "旅游保险": "旅游",
	"车险": "车险", "汽车保险": "车险",
	"家财险": "家财", "家庭财产保险": "家财",
}

var insuranceTypeRE = regexp.MustCompile(`百万医疗险|百万医疗|住院医疗|补充医疗|高端医疗|医疗保险|医疗险|重大疾病险|重大疾病|大病保险|大病险|重疾险|意外伤害|意外保险|综合意外|意外险|家庭财产保险|家财险|定期寿险|终身寿险|人寿保险|寿险|年金保险|年金险|养老保险|养老险|养老金|教育金保险|教育金|教育险|防癌医疗险|防癌医疗|防癌险|少儿保险|少儿险|儿童保险|儿童险|旅游保险|旅行险|旅游险|汽车保险|车险|健康险`)

var audienceMapping = map[string][]string{
	"宝宝": {"少儿医疗", "少儿重疾", "少儿意外"}, "孩子": {"少儿医疗", "少儿重疾", "少儿意外"}, "小孩": {"少儿医疗", "少儿重疾", "少儿意外"}, "儿童": {"少儿医疗", "少儿重疾", "少儿意外"}, "婴儿": {"少儿医疗", "少儿重疾", "少儿意外"}, "新生儿": {"少儿医疗", "少儿重疾", "少儿意外"},
	"老人": {"医疗", "防癌", "意外"}, "老年": {"医疗", "防癌", "意外"}, "退休": {"医疗", "防癌", "意外"},
	"全家": {"医疗", "意外", "重疾"}, "家庭": {"医疗", "意外", "重疾"}, "一家人": {"医疗", "意外", "重疾"},
	"父母": {"医疗", "防癌", "意外"}, "爸妈": {"医疗", "防癌", "意外"}, "爸爸妈妈": {"医疗", "防癌", "意外"},
	"住院": {"医疗"}, "生病": {"医疗"}, "看病": {"医疗"}, "手术": {"医疗"},
	"养老": {"年金", "养老"},
	"教育": {"教育金", "少儿重疾"}, "上学": {"教育金", "少儿重疾"},
	"猝死": {"意外", "寿险"}, "身故": {"意外", "寿险"}, "去世": {"意外", "寿险"},
}

var audienceRE = regexp.MustCompile(`爸爸妈妈|新生儿|一家人|孩子|小孩|儿童|婴儿|宝宝|老人|老年|退休|全家|家庭|父母|爸妈|住院|生病|看病|手术|养老|教育|上学|猝死|身故|去世`)
var AgeRE = regexp.MustCompile(`(\d{1,3})\s*岁`)

var simpleBudgetREs = []*regexp.Regexp{
	regexp.MustCompile(`预算[每]?[年]?\s*(\d+\.?\d*)`),
	regexp.MustCompile(`(\d+\.?\d*)\s*[元块]?\s*[/每]?\s*年`),
	regexp.MustCompile(`年[预]?[算费]?\s*(\d+\.?\d*)`),
	regexp.MustCompile(`(\d+\.?\d*)\s*[元块]左右`),
	regexp.MustCompile(`(\d+\.?\d*)\s*[元块]以内`),
}

func ExtractKeywords(userInput string) []string {
	var keywords []string
	seen := map[string]bool{}
	add := func(kw string) {
		if kw != "" && !seen[kw] {
			seen[kw] = true
			keywords = append(keywords, kw)
		}
	}

	for _, raw := range insuranceTypeRE.FindAllString(userInput, -1) {
		if mapped, ok := keywordMapping[raw]; ok {
			add(mapped)
		} else {
			add(raw)
		}
	}
	for _, aud := range audienceRE.FindAllString(userInput, -1) {
		for _, kw := range audienceMapping[aud] {
			add(kw)
		}
	}
	if len(keywords) == 0 {
		for _, kw := range fallbackKeywords(userInput) {
			add(kw)
		}
	}
	return keywords
}

func ExtractKeyword(userInput string) string {
	keywords := ExtractKeywords(userInput)
	if len(keywords) == 0 {
		return "保险"
	}
	return keywords[0]
}

func ExtractAge(userInput string) (int, bool) {
	m := AgeRE.FindStringSubmatch(userInput)
	if len(m) != 2 {
		return 0, false
	}
	age, err := strconv.Atoi(m[1])
	if err != nil || age < 0 || age > 150 {
		return 0, false
	}
	return age, true
}

func fallbackKeywords(userInput string) []string {
	if budget, ok := extractBudgetSimple(userInput); ok {
		switch {
		case budget <= 500:
			return []string{"意外", "医疗"}
		case budget <= 3000:
			return []string{"医疗", "意外", "重疾"}
		default:
			return []string{"重疾", "医疗", "寿险"}
		}
	}
	if age, ok := ExtractAge(userInput); ok {
		if age < 18 {
			return []string{"少儿医疗", "少儿意外"}
		}
		if age >= 55 {
			return []string{"医疗", "防癌"}
		}
		return []string{"医疗", "重疾", "意外"}
	}
	return []string{"医疗", "重疾", "意外"}
}

func extractBudgetSimple(userInput string) (float64, bool) {
	for _, re := range simpleBudgetREs {
		m := re.FindStringSubmatchIndex(userInput)
		if len(m) < 4 {
			continue
		}
		val, err := strconv.ParseFloat(userInput[m[2]:m[3]], 64)
		if err != nil || val <= 0 {
			continue
		}
		if m[1] < len(userInput) && []rune(userInput[m[1]:])[0] == '万' {
			val *= 10000
		}
		if val >= 50 && val <= 100000 {
			return val, true
		}
	}
	return 0, false
}
