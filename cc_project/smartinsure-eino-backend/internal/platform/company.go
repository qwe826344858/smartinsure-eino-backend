package platform

import "regexp"

var companyRE = regexp.MustCompile(`众安保险|众安|中国人保|人保财险|人保健康|人保寿险|人保|中国平安|平安健康|平安人寿|平安|太平洋保险|太平洋产险|太平洋人寿|太平洋|中国人寿|国寿|泰康人寿|泰康在线|泰康|新华保险|新华人寿|新华|阳光保险|阳光人寿|阳光|中英人寿|中英|复星联合|复星|瑞华保险|瑞华|华贵保险|华贵人寿|华贵|中意人寿|中意|信泰人寿|信泰|百年人寿|百年|大家人寿|大家保险|昆仑健康|昆仑|和谐健康|和谐|招商仁和|招商|国富人寿|国富|北京人寿|中邮人寿|中邮|大地保险`)

func ExtractCompany(productName string) string {
	return companyRE.FindString(productName)
}
