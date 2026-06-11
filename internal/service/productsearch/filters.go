package productsearch

import (
	"regexp"
	"strconv"
	"strings"

	"smartinsure-eino-backend/internal/platform"
)

var irrelevantCategories = []string{
	"旅游险", "旅行险", "自驾游", "境外旅游", "国内旅游",
	"航空意外", "交通意外", "责任险", "商业责任", "雇主责任", "亚马逊",
	"财产险", "家财险", "车险", "运动保险", "车类运动", "留学", "签证", "宠物", "手机",
}

func FilterIrrelevant(products []platform.ProductCard, keywords []string) []platform.ProductCard {
	skip := map[string]bool{"旅游": true, "交通": true, "自驾": true, "留学": true, "运动": true, "车险": true, "家财": true}
	for _, kw := range keywords {
		if skip[kw] {
			return products
		}
	}
	filtered := make([]platform.ProductCard, 0, len(products))
	for _, p := range products {
		text := p.Name + " " + strings.Join(p.Tags, " ")
		irrelevant := false
		for _, cat := range irrelevantCategories {
			if strings.Contains(text, cat) {
				irrelevant = true
				break
			}
		}
		if !irrelevant {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

var (
	ageRangeRE      = regexp.MustCompile(`(\d+)\s*周?岁?\s*[-~至到]\s*(\d+)\s*周?岁`)
	childProductRE  = regexp.MustCompile(`少儿|儿童|宝宝|宝贝|学平|小顽童|大黄蜂`)
	seniorProductRE = regexp.MustCompile(`老年|高龄|银发|孝亲|父母`)
)

func FilterByAge(products []platform.ProductCard, userInput string) []platform.ProductCard {
	if strings.Contains(userInput, "全家") || strings.Contains(userInput, "家庭") || strings.Contains(userInput, "一家人") || strings.Contains(userInput, "一家") {
		return products
	}

	ageGroup := inferAgeGroup(userInput)
	if ageGroup == "" {
		return products
	}

	filtered := make([]platform.ProductCard, 0, len(products))
	for _, p := range products {
		text := p.Name + " " + strings.Join(p.Tags, " ")
		rangeMatch := ageRangeRE.FindStringSubmatch(text)
		if len(rangeMatch) == 3 {
			minAge, _ := strconv.Atoi(rangeMatch[1])
			maxAge, _ := strconv.Atoi(rangeMatch[2])
			if ageGroup == "adult" && maxAge <= 17 {
				continue
			}
			if ageGroup == "adult" && minAge >= 50 && seniorProductRE.MatchString(text) {
				continue
			}
			if ageGroup == "child" && minAge >= 18 {
				continue
			}
			if ageGroup == "senior" && maxAge <= 17 {
				continue
			}
		} else {
			if ageGroup == "adult" && (childProductRE.MatchString(text) || seniorProductRE.MatchString(text)) {
				continue
			}
			if ageGroup == "senior" && childProductRE.MatchString(text) {
				continue
			}
			if ageGroup == "child" && seniorProductRE.MatchString(text) {
				continue
			}
		}
		filtered = append(filtered, p)
	}
	return filtered
}

func inferAgeGroup(userInput string) string {
	if age, ok := platform.ExtractAge(userInput); ok {
		if age < 18 {
			return "child"
		}
		if age >= 55 {
			return "senior"
		}
		return "adult"
	}
	if containsAny(userInput, "宝宝", "孩子", "小孩", "儿童", "婴儿", "新生儿") {
		return "child"
	}
	if containsAny(userInput, "退休", "老人", "老年", "父母", "爸妈") {
		return "senior"
	}
	if containsAny(userInput, "大学生", "刚毕业", "应届", "上班族", "骑手", "外卖", "自由职业", "打工", "白领", "程序员", "怀孕", "产后") {
		return "adult"
	}
	return ""
}

func containsAny(s string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(s, value) {
			return true
		}
	}
	return false
}
