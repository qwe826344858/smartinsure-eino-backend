package productsearch

import (
	"regexp"
	"strconv"
)

var (
	familyRE           = regexp.MustCompile(`全家|一家人|家庭|一家[一二两三四五六\d]口|[一二两三四五六\d]口之?家`)
	familySizeREs      = []*regexp.Regexp{regexp.MustCompile(`一家\s*([一二两三四五六\d])\s*口`), regexp.MustCompile(`([一二两三四五六\d])\s*口之?家`)}
	childCountInFamily = regexp.MustCompile(`([一二两三四五六\d]+)\s*个?\s*(?:孩子|小孩|娃|宝宝|儿女|子女)`)
	cnNum              = map[string]int{"一": 1, "二": 2, "两": 2, "三": 3, "四": 4, "五": 5, "六": 6}
)

func DetectFamilySize(userInput string) int {
	if !familyRE.MatchString(userInput) {
		return 1
	}
	for _, re := range familySizeREs {
		m := re.FindStringSubmatch(userInput)
		if len(m) == 2 {
			if n, ok := parseCNOrDigit(m[1]); ok && n >= 2 && n <= 8 {
				return n
			}
		}
	}
	m := childCountInFamily.FindStringSubmatch(userInput)
	if len(m) == 2 {
		if childCount, ok := parseCNOrDigit(m[1]); ok && childCount >= 1 && childCount <= 5 {
			return childCount + 2
		}
	}
	return 3
}

func parseCNOrDigit(value string) (int, bool) {
	if n, ok := cnNum[value]; ok {
		return n, true
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return n, true
}
