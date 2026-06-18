package smartinsuredeep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"

	"smartinsure-eino-backend/internal/agent/chatflow"
)

const (
	// toolProductSearch 对应 DeepAgent 的产品搜索工具。
	toolProductSearch = "product_search"
	// toolKnowledgeSearch 对应 DeepAgent 的保险知识/RAG 检索工具。
	toolKnowledgeSearch = "knowledge_search"
	// toolProductDetail 对应 DeepAgent 主动选择的产品详情解析工具。
	toolProductDetail = "product_detail"
)

// insuranceTools 保存 DeepAgent 工具对底层 chatflow 能力的引用。
type insuranceTools struct {
	// search 复用生产产品搜索服务，返回产品卡片。
	search chatflow.ProductSearcher
	// knowledge 复用 fallback/RAG 知识检索服务，返回来源和摘要。
	knowledge chatflow.FallbackSearcher
	// priceLookup 用于把 RAG 召回结果补齐 MySQL 中的产品价格。
	priceLookup chatflow.ProductPriceLookup
	// detail 复用产品详情 runner，解析页面并返回 detail_items。
	detail chatflow.DetailRunner
	// toolTimeout 是 DeepAgent 自己选择工具时的单次调用超时。
	// 直达 product_detail 请求在 agent.go 中旁路到 chatflow，不受这个通用工具超时限制。
	toolTimeout time.Duration
	// ragOnly 表示当前工具集只服务 RAG Agent。默认 DeepAgent 的产品卡应来自 product_search，
	// 避免 knowledge_search 的无价格 RAG 召回结果被前端当作推荐产品卡展示。
	ragOnly bool
}

// productSearchInput 是 product_search 工具暴露给模型的 JSON 入参。
type productSearchInput struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"Insurance product search query."`
}

// productSearchOutput 是 product_search 工具返回给 DeepAgent 的结构化结果。
type productSearchOutput struct {
	Summary  string                 `json:"summary"`
	Products []chatflow.ProductCard `json:"products,omitempty"`
	Error    string                 `json:"error,omitempty"`
}

// knowledgeSearchInput 是 knowledge_search 工具暴露给模型的 JSON 入参。
type knowledgeSearchInput struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"Insurance knowledge or policy query."`
}

// knowledgeSearchOutput 是 knowledge_search 工具返回给 DeepAgent 的检索结果。
type knowledgeSearchOutput struct {
	Summary  string                      `json:"summary"`
	Results  []chatflow.SearchResultItem `json:"results,omitempty"`
	Sources  []chatflow.SourceItem       `json:"sources,omitempty"`
	Products []chatflow.ProductCard      `json:"products,omitempty"`
	Error    string                      `json:"error,omitempty"`
}

// productDetailInput 是 product_detail 工具暴露给模型的 JSON 入参。
type productDetailInput struct {
	ProductURL   string `json:"product_url" jsonschema:"required" jsonschema_description:"Insurance product detail page URL."`
	ProductName  string `json:"product_name,omitempty" jsonschema_description:"Insurance product name, if known."`
	UserQuestion string `json:"user_question,omitempty" jsonschema_description:"User question about this product."`
}

// productDetailOutput 是产品详情工具返回给 DeepAgent 的结构化摘要。
type productDetailOutput struct {
	Summary     string `json:"summary"`
	ProductName string `json:"product_name,omitempty"`
	DetailItems any    `json:"detail_items,omitempty"`
	Text        string `json:"text,omitempty"`
	Error       string `json:"error,omitempty"`
}

// newTools 将 chatflow 的搜索、知识检索、详情解析能力包装成 Eino ADK tools。
func newTools(flow *chatflow.Flow, timeout time.Duration) (adk.ToolsConfig, error) {
	return newToolsWithMode(flow, timeout, false)
}

// newRAGTools 只向 RAG Agent 暴露 knowledge_search。
// 该链路必须基于已入库向量库做产品匹配，不能主动平台搜索或抓详情页。
func newRAGTools(flow *chatflow.Flow, timeout time.Duration) (adk.ToolsConfig, error) {
	ragSearcher := chatflow.NewProductionRAGOnlySearcher()
	if flow == nil {
		flow = chatflow.New()
	}
	flow.Fallback = ragSearcher
	return newToolsWithMode(flow, timeout, true)
}

func newToolsWithMode(flow *chatflow.Flow, timeout time.Duration, ragOnly bool) (adk.ToolsConfig, error) {
	if flow == nil {
		// flow 为空时兜底创建生产 flow，保证 DeepAgent 初始化时工具可用。
		flow = chatflow.NewProduction()
	}
	if timeout <= 0 {
		// 对模型规划出来的工具调用保持较短默认超时。
		// 如果允许模型主动解析新详情页，生产环境应显式调大 AGENT_TOOL_TIMEOUT。
		timeout = 15 * time.Second
	}
	tools := insuranceTools{
		search:      flow.Search,
		knowledge:   flow.Fallback,
		priceLookup: flow.Prices,
		detail:      flow.Detail,
		toolTimeout: timeout,
		ragOnly:     ragOnly,
	}
	knowledgeSearch, err := toolutils.InferTool[knowledgeSearchInput, knowledgeSearchOutput](
		toolKnowledgeSearch,
		"Search product-detail RAG knowledge and return supporting sources for product matching, explanations, or final answers.",
		tools.knowledgeSearch,
	)
	if err != nil {
		return adk.ToolsConfig{}, err
	}
	registeredTools := []tool.BaseTool{knowledgeSearch}
	if !ragOnly {
		productSearch, err := toolutils.InferTool[productSearchInput, productSearchOutput](
			toolProductSearch,
			"Search insurance products and return product cards for recommendation, query, or comparison tasks.",
			tools.productSearch,
		)
		if err != nil {
			return adk.ToolsConfig{}, err
		}
		registeredTools = append([]tool.BaseTool{productSearch}, registeredTools...)
	}
	if !ragOnly {
		productDetail, err := toolutils.InferTool[productDetailInput, productDetailOutput](
			toolProductDetail,
			"Fetch and extract insurance product detail information from a product URL.",
			tools.productDetail,
		)
		if err != nil {
			return adk.ToolsConfig{}, err
		}
		registeredTools = append(registeredTools, productDetail)
	}
	return adk.ToolsConfig{
		ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools: registeredTools,
			// 未注册工具一律拒绝，避免模型幻觉调用不存在的动作。
			UnknownToolsHandler: func(_ context.Context, name, _ string) (string, error) {
				payload, _ := json.Marshal(map[string]string{
					"error":   "unknown tool",
					"summary": "未知工具已被拒绝：" + name,
				})
				return string(payload), nil
			},
			// 工具按顺序执行，避免多个外部请求并发导致日志和前端事件难以追踪。
			ExecuteSequentially: true,
		},
	}, nil
}

// productSearch 执行 DeepAgent 选择的产品搜索工具。
func (t insuranceTools) productSearch(ctx context.Context, input productSearchInput) (productSearchOutput, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return productSearchOutput{Summary: "产品搜索 query 为空。", Error: "query is empty"}, nil
	}
	if t.search == nil {
		return productSearchOutput{Summary: "产品搜索工具未配置。", Error: "product search tool is not configured"}, nil
	}
	logx.Printf("运行日志", "runtime log", "deep_agent tool_start tool=%s query_chars=%d timeout_seconds=%d", toolProductSearch, len([]rune(query)), int(t.toolTimeout.Seconds()))
	// 所有 DeepAgent 主动工具调用都使用 toolTimeout 派生子 context。
	toolCtx, cancel := context.WithTimeout(ctx, t.toolTimeout)
	defer cancel()
	startedAt := time.Now()
	products, err := t.search.Search(toolCtx, query)
	if err != nil {
		logx.Printf("运行日志", "runtime log", "deep_agent tool_failed tool=%s duration_ms=%d err=%v", toolProductSearch, time.Since(startedAt).Milliseconds(), err)
		return productSearchOutput{Summary: err.Error(), Error: err.Error()}, nil
	}
	logx.Printf("运行日志", "runtime log", "deep_agent tool_success tool=%s products=%d duration_ms=%d", toolProductSearch, len(products), time.Since(startedAt).Milliseconds())
	return productSearchOutput{
		Summary:  "产品搜索返回候选产品。",
		Products: products,
	}, nil
}

// knowledgeSearch 执行 DeepAgent 选择的知识检索工具，底层可能优先走 RAG，再 fallback 到本地知识库。
func (t insuranceTools) knowledgeSearch(ctx context.Context, input knowledgeSearchInput) (knowledgeSearchOutput, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return knowledgeSearchOutput{Summary: "知识检索 query 为空。", Error: "query is empty"}, nil
	}
	if t.knowledge == nil {
		return knowledgeSearchOutput{Summary: "知识检索工具未配置。", Error: "knowledge search tool is not configured"}, nil
	}
	logx.Printf("运行日志", "runtime log", "deep_agent tool_start tool=%s query_chars=%d timeout_seconds=%d", toolKnowledgeSearch, len([]rune(query)), int(t.toolTimeout.Seconds()))
	toolCtx, cancel := context.WithTimeout(ctx, t.toolTimeout)
	defer cancel()
	startedAt := time.Now()
	results, err := t.knowledge.Search(toolCtx, query)
	if err != nil {
		logx.Printf("运行日志", "runtime log", "deep_agent tool_failed tool=%s duration_ms=%d err=%v", toolKnowledgeSearch, time.Since(startedAt).Milliseconds(), err)
		return knowledgeSearchOutput{Summary: err.Error(), Error: err.Error()}, nil
	}
	logx.Printf("运行日志", "runtime log", "deep_agent tool_success tool=%s results=%d sources=%d duration_ms=%d", toolKnowledgeSearch, len(results), len(uniqueSources(results)), time.Since(startedAt).Milliseconds())
	out := knowledgeSearchOutput{
		Summary: "知识检索返回来源。",
		Results: results,
		// Sources 单独返回，方便 emitToolResult 映射为 SSE sources 事件。
		Sources: uniqueSources(results),
	}
	if t.ragOnly {
		out.Products = productCardsFromKnowledgeResults(ctx, results, t.priceLookup)
	}
	return out, nil
}

// productDetail 执行 DeepAgent 主动选择的详情解析工具。
// 注意：前端明确 action=product_detail 的请求不会走这里，而是在 agent.go 中直接走 chatflow。
func (t insuranceTools) productDetail(ctx context.Context, input productDetailInput) (productDetailOutput, error) {
	productURL := strings.TrimSpace(input.ProductURL)
	if productURL == "" {
		return productDetailOutput{Summary: "product_url 不能为空。", Error: "product_url is empty"}, nil
	}
	if t.detail == nil {
		return productDetailOutput{Summary: "产品详情工具未配置。", Error: "product detail tool is not configured"}, nil
	}
	// 该路径只处理 DeepAgent 自己选择的 product_detail 工具调用。
	// 显式 UI/API 详情 action 会绕过这里，避免 URL 被模型复述错误和通用工具超时。
	logx.Printf("运行日志", "runtime log", "deep_agent tool_start tool=%s host=%s url_hash=%s timeout_seconds=%d", toolProductDetail, logURLHost(productURL), logHash(productURL), int(t.toolTimeout.Seconds()))
	toolCtx, cancel := context.WithTimeout(ctx, t.toolTimeout)
	defer cancel()
	startedAt := time.Now()
	events := t.detail.Run(toolCtx, chatflow.DetailRequest{
		ProductURL:   productURL,
		ProductName:  strings.TrimSpace(input.ProductName),
		UserQuestion: strings.TrimSpace(input.UserQuestion),
		Action:       "product_detail",
	})
	out := productDetailOutput{
		Summary:     "已执行产品详情解析。",
		ProductName: strings.TrimSpace(input.ProductName),
	}
	var text strings.Builder
	for events != nil {
		select {
		case <-toolCtx.Done():
			// 产品详情解析可能包含抓页和 LLM 抽取，超过 DeepAgent 工具超时后返回可解释错误。
			out.Summary = "产品详情解析超时或被取消。"
			out.Error = toolCtx.Err().Error()
			logx.Printf("运行日志", "runtime log", "deep_agent tool_timeout tool=%s host=%s url_hash=%s duration_ms=%d err=%v", toolProductDetail, logURLHost(productURL), logHash(productURL), time.Since(startedAt).Milliseconds(), toolCtx.Err())
			return out, nil
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			switch event.Name {
			case chatflow.EventDetailItems:
				// detail_items 原样保留，后续 emitToolResult 会映射成 SSE detail_items。
				out.DetailItems = event.Data
			case chatflow.EventDelta:
				// delta 是详情解读文本，收集成工具返回文本，供 DeepAgent 总结。
				if chunk := deltaText(event.Data); chunk != "" {
					text.WriteString(chunk)
				}
			case chatflow.EventError:
				// chatflow 内部错误提取成字符串，放入工具输出 error 字段。
				out.Error = errorText(event.Data)
			}
		}
	}
	out.Text = text.String()
	hasDetailItems := out.DetailItems != nil
	logx.Printf("运行日志", "runtime log", "deep_agent tool_success tool=%s host=%s url_hash=%s has_detail_items=%t text_chars=%d duration_ms=%d", toolProductDetail, logURLHost(productURL), logHash(productURL), hasDetailItems, len([]rune(out.Text)), time.Since(startedAt).Milliseconds())
	return out, nil
}

// uniqueSources 对检索结果来源去重，避免 sources SSE 重复展示同一链接。
func uniqueSources(results []chatflow.SearchResultItem) []chatflow.SourceItem {
	seen := map[string]struct{}{}
	sources := make([]chatflow.SourceItem, 0, len(results))
	for _, result := range results {
		key := result.URL
		if key == "" {
			key = result.Title + "|" + result.Site
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		sources = append(sources, chatflow.SourceItem{
			Title:      result.Title,
			URL:        result.URL,
			Site:       result.Site,
			ProductURL: firstNonEmpty(result.ProductURL, result.URL),
		})
	}
	return sources
}

func productCardsFromKnowledgeResults(ctx context.Context, results []chatflow.SearchResultItem, priceLookup chatflow.ProductPriceLookup) []chatflow.ProductCard {
	seen := map[string]struct{}{}
	products := make([]chatflow.ProductCard, 0, len(results))
	for _, result := range results {
		productURL := firstNonEmpty(result.ProductURL, result.URL)
		productName := firstNonEmpty(result.ProductName, productNameFromTitle(result.Title))
		if productURL == "" {
			continue
		}
		key := firstNonEmpty(productURL, result.Site+"|"+productName)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		tags := append([]string(nil), result.Tags...)
		if len(tags) == 0 && strings.TrimSpace(result.Site) != "" {
			tags = []string{strings.TrimSpace(result.Site)}
		}
		price, priceLabel := knowledgeProductPrice(ctx, priceLookup, productURL)
		products = append(products, chatflow.ProductCard{
			ID:         "rag_" + logHash(key),
			Name:       firstNonEmpty(productName, result.Title),
			Company:    strings.TrimSpace(result.Site),
			Price:      stringPtrOrNil(price),
			PriceLabel: firstNonEmpty(priceLabel, "详见产品页"),
			Tags:       tags,
			URL:        productURL,
			Platform:   result.Site,
			Brief:      firstNonEmpty(result.Snippet, "来自已入库 RAG 产品知识库的召回结果"),
		})
	}
	return products
}

func knowledgeProductPrice(ctx context.Context, priceLookup chatflow.ProductPriceLookup, productURL string) (string, string) {
	if priceLookup == nil || strings.TrimSpace(productURL) == "" {
		return "", ""
	}
	price, ok, err := priceLookup.LookupProductPrice(ctx, productURL)
	if err != nil {
		logx.Printf("运行日志", "runtime log", "deep_agent price_lookup_failed host=%s url_hash=%s err=%v", logURLHost(productURL), logHash(productURL), err)
		return "", ""
	}
	if !ok {
		return "", ""
	}
	return strings.TrimSpace(price.Price), strings.TrimSpace(price.PriceLabel)
}

func stringPtrOrNil(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func productNameFromTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	if before, _, ok := strings.Cut(title, " - "); ok {
		return strings.TrimSpace(before)
	}
	return title
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// deltaText 从不同形态的 SSE data 中提取 text 字段。
func deltaText(data any) string {
	switch value := data.(type) {
	case map[string]string:
		return value["text"]
	case map[string]any:
		if text, ok := value["text"].(string); ok {
			return text
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return payload.Text
}

// errorText 从不同形态的 SSE error data 中提取 message 字段。
func errorText(data any) string {
	switch value := data.(type) {
	case map[string]string:
		if value["message"] != "" {
			return value["message"]
		}
	case map[string]any:
		if text, ok := value["message"].(string); ok {
			return text
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return payload.Message
}

// logURLHost 只提取 URL host，避免日志输出完整产品链接。
func logURLHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return ""
	}
	return parsed.Hostname()
}

// logHash 生成短 hash，便于日志定位同一 URL，同时避免输出完整 URL。
func logHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	text := hex.EncodeToString(sum[:])
	if len(text) <= 12 {
		return text
	}
	return text[:12]
}
