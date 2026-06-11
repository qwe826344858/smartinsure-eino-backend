package productlegacy

import (
	"regexp"
	"strings"
)

type Platform struct {
	Domain            string
	Name              string
	ProductURLPattern *regexp.Regexp
}

func DefaultPlatforms() []Platform {
	return []Platform{
		{
			Domain:            "xiaoyusan.com",
			Name:              "小雨伞",
			ProductURLPattern: regexp.MustCompile(`xiaoyusan\.com/insurance/detail\?id=`),
		},
		{
			Domain:            "huize.com",
			Name:              "慧择",
			ProductURLPattern: regexp.MustCompile(`huize\.com/apps/cps/index/product/detail`),
		},
		{
			Domain:            "shenlanbao.com",
			Name:              "深蓝保",
			ProductURLPattern: regexp.MustCompile(`shenlanbao\.com/pingce/\d+`),
		},
		{
			Domain:            "baoxian.pingan.com",
			Name:              "平安保险",
			ProductURLPattern: regexp.MustCompile(`baoxian\.pingan\.com/product/\w+\.shtml|baoxian\.pingan\.com/pa18shopnst/.*productInfo`),
		},
		{
			Domain:            "i.zhongan.com",
			Name:              "众安保险",
			ProductURLPattern: regexp.MustCompile(`zhongan\.com/product/`),
		},
	}
}

func (p Platform) IsProductURL(rawURL string) bool {
	if p.ProductURLPattern == nil {
		return false
	}
	return p.ProductURLPattern.MatchString(strings.ToLower(rawURL))
}
