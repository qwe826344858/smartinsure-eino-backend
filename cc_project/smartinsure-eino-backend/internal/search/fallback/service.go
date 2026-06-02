package fallback

import (
	"net/url"
	"strings"

	"smartinsure-eino-backend/internal/schema"
)

type KnowledgeItem struct {
	Title    string
	URL      string
	Site     string
	Snippet  string
	Keywords []string
}

type Service struct {
	knowledge []KnowledgeItem
}

func NewService(items []KnowledgeItem) *Service {
	if len(items) == 0 {
		items = DefaultKnowledge()
	}
	return &Service{knowledge: items}
}

func (s *Service) Search(query string) []schema.SearchResultItem {
	if s == nil || len(s.knowledge) == 0 {
		s = NewService(nil)
	}

	seen := map[string]struct{}{}
	matched := make([]schema.SearchResultItem, 0)
	for _, entry := range s.knowledge {
		if !matches(query, entry.Keywords) {
			continue
		}
		normalized := NormalizeURL(entry.URL)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		matched = append(matched, toSearchResult(entry))
	}
	if len(matched) == 0 {
		return []schema.SearchResultItem{toSearchResult(DefaultGeneralItem())}
	}
	return matched
}

func (s *Service) SearchAll(queries []string) []schema.SearchResultItem {
	seen := map[string]struct{}{}
	out := make([]schema.SearchResultItem, 0)
	for _, q := range queries {
		for _, item := range s.Search(q) {
			normalized := NormalizeURL(item.URL)
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

func NormalizeURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String()
}

func DefaultKnowledge() []KnowledgeItem {
	return append([]KnowledgeItem(nil), defaultKnowledge...)
}

func DefaultGeneralItem() KnowledgeItem {
	return KnowledgeItem{
		Title:   "保险知识科普：一文读懂保险基础概念",
		URL:     "https://zhuanlan.zhihu.com/p/398765432",
		Site:    "zhuanlan.zhihu.com",
		Snippet: "涵盖保险分类、投保流程、常见术语解读等基础知识，帮助消费者快速了解保险。",
	}
}

func matches(query string, keywords []string) bool {
	for _, kw := range keywords {
		if kw != "" && strings.Contains(query, kw) {
			return true
		}
	}
	return false
}

func toSearchResult(item KnowledgeItem) schema.SearchResultItem {
	return schema.SearchResultItem{Title: item.Title, URL: item.URL, Site: item.Site, Snippet: item.Snippet}
}

var defaultKnowledge = []KnowledgeItem{
	{Title: "什么是免赔额？免赔额越低越好吗？", URL: "https://zhuanlan.zhihu.com/p/350682461", Site: "zhuanlan.zhihu.com", Snippet: "免赔额是保险公司不予赔付的金额部分，常见于医疗险中。一般年免赔额为1万元，超过部分才可报销。免赔额高低直接影响保费和理赔门槛。", Keywords: []string{"免赔额", "免赔", "起赔线", "赔付门槛"}},
	{Title: "保险等待期是什么意思？等待期内出险怎么办？", URL: "https://zhuanlan.zhihu.com/p/362051847", Site: "zhuanlan.zhihu.com", Snippet: "等待期也叫观察期，是保险合同生效后的一段免责期。重疾险等待期通常为90-180天，医疗险为30天。等待期内出险保险公司不予赔付。", Keywords: []string{"等待期", "观察期", "免责期", "等待期内出险"}},
	{Title: "重疾险怎么选？2024年重疾险选购指南", URL: "https://zhuanlan.zhihu.com/p/389456123", Site: "zhuanlan.zhihu.com", Snippet: "重疾险即重大疾病保险，确诊约定重疾后一次性给付保额。选购时需关注保障病种、赔付次数、轻中症保障及保费豁免条款。", Keywords: []string{"重疾险", "重大疾病", "重疾", "大病保险", "重疾保障"}},
	{Title: "百万医疗险和重疾险有什么区别？", URL: "https://zhuanlan.zhihu.com/p/401234567", Site: "zhuanlan.zhihu.com", Snippet: "医疗险是报销型保险，凭医疗费用发票报销；重疾险是给付型保险，确诊即赔。百万医疗险保费低保额高，但通常有1万免赔额，且为一年期需续保。", Keywords: []string{"医疗险", "百万医疗", "住院医疗", "医疗保险", "医疗报销"}},
	{Title: "意外险买哪种好？意外险选购攻略", URL: "https://zhuanlan.zhihu.com/p/378901234", Site: "zhuanlan.zhihu.com", Snippet: "意外险保障因意外伤害导致的身故、伤残及医疗费用。综合意外险通常包含意外身故/伤残、意外医疗、住院津贴等保障，保费低杠杆高。", Keywords: []string{"意外险", "意外伤害", "意外保险", "综合意外"}},
	{Title: "定期寿险和终身寿险怎么选？", URL: "https://zhuanlan.zhihu.com/p/356789012", Site: "zhuanlan.zhihu.com", Snippet: "寿险以身故为赔付条件。定期寿险保障一定年限，保费低适合家庭经济支柱；终身寿险保障终身，兼具储蓄功能，适合资产传承规划。", Keywords: []string{"寿险", "定期寿险", "终身寿险", "人寿保险", "身故保障"}},
	{Title: "年金险值得买吗？年金险的优缺点分析", URL: "https://zhuanlan.zhihu.com/p/412345678", Site: "zhuanlan.zhihu.com", Snippet: "年金险是一种以生存为给付条件的保险，适合养老规划和长期资金储备。优点是收益确定写入合同，缺点是流动性差、前期退保有损失。", Keywords: []string{"年金险", "年金", "养老保险", "养老金", "养老规划"}},
	{Title: "保额怎么确定？不同险种保额选多少合适？", URL: "https://www.iachina.cn/art/2023/11/15/art_72_106521.html", Site: "www.iachina.cn", Snippet: "保额即保险金额，是保险公司承担赔偿的最高限额。重疾险建议保额30-50万，寿险保额建议为年收入的10倍，医疗险建议选百万保额。", Keywords: []string{"保额", "保险金额", "保多少", "保额选择"}},
	{Title: "保费怎么算？影响保费的因素有哪些？", URL: "https://www.iachina.cn/art/2023/9/20/art_72_105832.html", Site: "www.iachina.cn", Snippet: "保费是投保人为获得保险保障而支付的费用。影响保费的主要因素包括年龄、性别、健康状况、职业、保额、保障期限和缴费年限等。", Keywords: []string{"保费", "保险费", "保费计算", "交多少钱", "保费预算"}},
	{Title: "保险理赔流程全解析：出险后如何快速获得赔付？", URL: "https://www.cbirc.gov.cn/cn/view/pages/tongjishuju/tongjishuju.html", Site: "www.cbirc.gov.cn", Snippet: "保险理赔一般流程：出险报案→准备材料→提交申请→保险公司审核→赔付到账。建议第一时间报案，保留好医疗单据、诊断证明等材料。", Keywords: []string{"理赔", "保险理赔", "出险", "报案", "赔付", "理赔流程"}},
	{Title: "保险核保是什么意思？核保不通过怎么办？", URL: "https://baike.baidu.com/item/保险核保", Site: "baike.baidu.com", Snippet: "核保是保险公司对投保申请进行风险评估的过程。核保结果包括标准体承保、加费承保、除外承保和拒保。核保不通过可尝试多家投保或选择智能核保产品。", Keywords: []string{"核保", "保险核保", "核保不通过", "风险评估", "承保"}},
	{Title: "健康告知怎么填？如实告知的注意事项", URL: "https://zhuanlan.zhihu.com/p/345678901", Site: "zhuanlan.zhihu.com", Snippet: "健康告知是投保时必须如实回答的健康相关问题。遵循有问必答、不问不答原则。未如实告知可能导致拒赔。常见问题涉及既往症、住院史、体检异常等。", Keywords: []string{"健康告知", "如实告知", "告知义务", "带病投保", "既往症"}},
	{Title: "2024年热门保险产品对比测评", URL: "https://zhuanlan.zhihu.com/p/423456789", Site: "zhuanlan.zhihu.com", Snippet: "从保障范围、保费性价比、理赔服务等维度横向对比热门重疾险、医疗险、意外险产品，帮助消费者选择最适合自己的保险方案。", Keywords: []string{"保险对比", "产品对比", "对比测评", "哪个好", "性价比"}},
	{Title: "保险怎么买最划算？家庭保险配置方案推荐", URL: "https://zhuanlan.zhihu.com/p/398765432", Site: "zhuanlan.zhihu.com", Snippet: "科学的家庭保险配置应先保障后理财，优先配置医疗险和意外险，再补充重疾险和寿险。预算有限时可选择定期保障，后续加保。", Keywords: []string{"保险推荐", "保险配置", "怎么买保险", "保险方案", "买保险"}},
	{Title: "保险合同条款怎么看？关键条款解读指南", URL: "https://www.iachina.cn/art/2024/1/10/art_72_107201.html", Site: "www.iachina.cn", Snippet: "保险条款是保险合同的核心内容。重点关注保险责任、责任免除、等待期、犹豫期、现金价值等关键条款，避免投保后才发现保障不符预期。", Keywords: []string{"条款解读", "保险条款", "合同条款", "责任免除", "保险合同"}},
	{Title: "保险犹豫期是什么？犹豫期内退保全额退款吗？", URL: "https://baike.baidu.com/item/犹豫期", Site: "baike.baidu.com", Snippet: "犹豫期是投保人签收保单后的一段时间（通常10-20天），在此期间退保可全额退还保费。过了犹豫期退保只能退现金价值，可能有较大损失。", Keywords: []string{"犹豫期", "退保", "全额退保", "现金价值"}},
	{Title: "中国银行保险监督管理委员会关于规范互联网保险销售的通知", URL: "https://www.cbirc.gov.cn/cn/view/pages/ItemDetail.html?docId=925393", Site: "www.cbirc.gov.cn", Snippet: "银保监会规范互联网保险销售行为，要求保险机构在销售页面显著位置展示保险条款、免责条款等重要信息，保障消费者知情权和选择权。", Keywords: []string{"银保监会", "保险监管", "互联网保险", "保险销售", "消费者权益"}},
}
