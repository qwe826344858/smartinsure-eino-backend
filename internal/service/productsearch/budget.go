package productsearch

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"smartinsure-eino-backend/internal/platform"
)

var budgetREs = []*regexp.Regexp{
	regexp.MustCompile(`预算[每]?[年]?\s*(\d+\.?\d*)\s*万`),
	regexp.MustCompile(`预算[每]?[年]?\s*(\d+\.?\d*)`),
	regexp.MustCompile(`(\d+\.?\d*)\s*万\s*[元块]?\s*[/每]?\s*年`),
	regexp.MustCompile(`(\d+\.?\d*)\s*[元块]\s*[/每]?\s*年`),
	regexp.MustCompile(`[每一]年\s*(\d+\.?\d*)\s*万\s*[元块]`),
	regexp.MustCompile(`[每一]年\s*(\d+\.?\d*)\s*[元块]`),
	regexp.MustCompile(`年预算\s*(\d+\.?\d*)\s*万`),
	regexp.MustCompile(`年预算\s*(\d+\.?\d*)`),
	regexp.MustCompile(`(\d+\.?\d*)\s*万?\s*[元块]左右`),
	regexp.MustCompile(`(\d+\.?\d*)\s*万?\s*[元块]以内`),
	regexp.MustCompile(`不超过\s*(\d+\.?\d*)\s*万?\s*[元块]`),
	regexp.MustCompile(`(\d+\.?\d*)\s*[元块]`),
}

func ExtractBudget(userInput string) (float64, bool) {
	for _, re := range budgetREs {
		m := re.FindStringSubmatch(userInput)
		if len(m) != 2 {
			continue
		}
		val, err := strconv.ParseFloat(m[1], 64)
		if err != nil || val <= 0 {
			continue
		}
		if strings.Contains(m[0], "万") {
			val *= 10000
		}
		if val >= 50 && val <= 100000 {
			return val, true
		}
	}
	return 0, false
}

func EstimateAnnualPrice(price *string) (float64, bool) {
	if price == nil || *price == "" {
		return 0, false
	}
	m := regexp.MustCompile(`(\d+\.?\d*)`).FindStringSubmatch(*price)
	if len(m) != 2 {
		return 0, false
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil || val <= 0 {
		return 0, false
	}
	if strings.Contains(*price, "月") {
		val *= 12
	}
	return val, true
}

type budgetScore struct {
	score float64
	item  platform.ProductCard
}

func FilterByBudget(products []platform.ProductCard, budget, tolerance, lowerRatio float64) []platform.ProductCard {
	if budget <= 0 {
		return products
	}
	if tolerance < 0 {
		tolerance = 0
	}
	if lowerRatio < 0 {
		lowerRatio = 0
	}
	upperBound := budget * (1 + tolerance)
	lowerBound := budget * lowerRatio
	scored := make([]budgetScore, 0, len(products))
	for _, p := range products {
		annual, ok := EstimateAnnualPrice(p.Price)
		if !ok {
			scored = append(scored, budgetScore{score: math.MaxFloat64, item: p})
			continue
		}
		if annual < lowerBound || annual > upperBound {
			continue
		}
		scored = append(scored, budgetScore{score: math.Abs(annual - budget), item: p})
	}
	sortByBudgetScore(scored)
	out := make([]platform.ProductCard, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.item)
	}
	return out
}
