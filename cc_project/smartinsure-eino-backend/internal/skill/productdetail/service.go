package productdetail

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/htmlcleaner"
	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/prompt"
	"smartinsure-eino-backend/internal/schema"
)

const (
	defaultMinCNChars      = 50
	defaultMaxExtractRetry = 3
)

type Request struct {
	Action       string
	ProductURL   string
	ProductName  string
	Message      string
	UserQuestion string
	RequestID    string
}

type Event struct {
	Name string
	Data any
}

type Service struct {
	model           llm.ChatModel
	fetcher         Fetcher
	cache           *ProductDetailCache
	minCNChars      int
	maxExtractRetry int
}

type Option func(*Service)

func WithModel(model llm.ChatModel) Option {
	return func(s *Service) {
		s.model = model
	}
}

func WithFetcher(fetcher Fetcher) Option {
	return func(s *Service) {
		s.fetcher = fetcher
	}
}

func WithCache(cache *ProductDetailCache) Option {
	return func(s *Service) {
		s.cache = cache
	}
}

func WithMinCNChars(min int) Option {
	return func(s *Service) {
		s.minCNChars = min
	}
}

func NewService(opts ...Option) *Service {
	s := &Service{
		fetcher:         NewHTTPFetcher(15 * time.Second),
		cache:           DefaultCache,
		minCNChars:      defaultMinCNChars,
		maxExtractRetry: defaultMaxExtractRetry,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.cache == nil {
		s.cache = NewCache(DefaultCacheTTL)
	}
	if s.fetcher == nil {
		s.fetcher = NewHTTPFetcher(15 * time.Second)
	}
	if s.minCNChars <= 0 {
		s.minCNChars = defaultMinCNChars
	}
	if s.maxExtractRetry <= 0 {
		s.maxExtractRetry = defaultMaxExtractRetry
	}
	return s
}

type Skill struct {
	service *Service
}

func NewSkill(service *Service) *Skill {
	if service == nil {
		service = NewService()
	}
	return &Skill{service: service}
}

func (s *Skill) Run(ctx context.Context, req Request) <-chan Event {
	return s.service.Run(ctx, req)
}

// Run emits status/detail_items/delta/error events. The caller owns final
// disclaimer and done events so this package can be embedded in chatflow.
func (s *Service) Run(ctx context.Context, req Request) <-chan Event {
	events := make(chan Event)
	go func() {
		defer close(events)
		s.run(ctx, req, events)
	}()
	return events
}

func (s *Service) run(ctx context.Context, req Request, events chan<- Event) {
	if s == nil {
		s = NewService()
	}
	productURL := strings.TrimSpace(req.ProductURL)
	if productURL == "" {
		emitDelta(ctx, events, "请先提供产品页面链接，才能读取保障详情。")
		return
	}

	question := strings.TrimSpace(req.UserQuestion)
	if question == "" {
		question = strings.TrimSpace(req.Message)
	}

	if req.Action == "product_followup" {
		detail, ok := s.cache.Get(productURL)
		if !ok {
			emitDelta(ctx, events, "暂未找到该产品的详情缓存，请先查看产品详情后再追问。")
			return
		}
		emitStatus(ctx, events, "answering", "正在基于已缓存的保障信息回答追问...")
		s.emitAnswer(ctx, events, detail, question)
		return
	}

	if detail, ok := s.cache.Get(productURL); ok {
		emitStatus(ctx, events, "answering", "正在生成保障解读...")
		s.emitAnswer(ctx, events, detail, question)
		return
	}

	emitStatus(ctx, events, "reading", "正在读取产品页面...")
	rawHTML, err := s.fetcher.Fetch(ctx, productURL)
	if err != nil || strings.TrimSpace(rawHTML) == "" {
		emitDelta(ctx, events, fmt.Sprintf("暂时无法访问该产品页面，建议直接查看：%s", productURL))
		return
	}

	cleanedText, cnCount := htmlcleaner.CleanHTML(rawHTML)
	if cnCount < s.minCNChars {
		emitDelta(ctx, events, fmt.Sprintf("该页面内容较少，无法提取保障详情，建议直接查看：%s", productURL))
		return
	}

	emitStatus(ctx, events, "analyzing", "正在分析保障项目...")
	detail, ok := s.extractDetail(ctx, cleanedText, cnCount, productURL, req.ProductName)
	if !ok {
		emitDelta(ctx, events, fmt.Sprintf("暂时无法自动解析此产品的保障详情，建议直接查看：%s", productURL))
		return
	}

	emit(ctx, events, Event{Name: schema.SSEEventDetailItems, Data: DetailItemsPayload(detail)})
	s.cache.Set(productURL, detail)

	emitStatus(ctx, events, "answering", "正在生成通俗解读...")
	s.emitAnswer(ctx, events, detail, question)
}

func (s *Service) extractDetail(ctx context.Context, cleanedText string, cnCount int, productURL, productName string) (schema.ProductDetail, bool) {
	if s.model != nil {
		for attempt := 1; attempt <= s.maxExtractRetry; attempt++ {
			input := cleanedText
			if attempt == s.maxExtractRetry && len([]rune(input)) > 3000 {
				input = string([]rune(input)[:3000])
			}
			raw, err := s.model.CallText(ctx, BuildExtractMessages(input), 0.2)
			if err != nil {
				continue
			}
			payload, ok := ParseExtractionJSON(raw)
			if !ok || len(payload.Duties) == 0 {
				continue
			}
			passed, matchRate, _ := ValidateExtraction(payload.Duties, cleanedText)
			if !passed {
				continue
			}
			name := strings.TrimSpace(payload.ProductName)
			if name == "" {
				name = strings.TrimSpace(productName)
			}
			return schema.ProductDetail{
				ProductName: name,
				ProductURL:  productURL,
				Platform:    InferPlatform(productURL),
				Duties:      payload.Duties,
				CNCharCount: cnCount,
				MatchRate:   matchRate,
			}, true
		}
	}

	detail := HeuristicExtract(cleanedText, productURL, productName, cnCount)
	return detail, len(detail.Duties) > 0
}

func BuildExtractMessages(cleanedText string) []llm.Message {
	userPrompt, err := prompt.Render(prompt.DetailExtractTemplate, struct {
		CleanedText string
	}{CleanedText: cleanedText})
	if err != nil {
		userPrompt = "请提取以下保险产品页面文本中的保障项目：\n" + cleanedText
	}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: prompt.DetailExtractSystem},
		{Role: llm.RoleUser, Content: userPrompt},
	}
}

func BuildAnswerMessages(detail schema.ProductDetail, userQuestion string) []llm.Message {
	duties := FormatDuties(detail.Duties)
	var userPrompt string
	var err error
	if strings.TrimSpace(userQuestion) != "" {
		userPrompt, err = prompt.Render(prompt.DetailFollowupTemplate, struct {
			ProductName     string
			DutiesFormatted string
			UserQuestion    string
		}{ProductName: detail.ProductName, DutiesFormatted: duties, UserQuestion: userQuestion})
	} else {
		userPrompt, err = prompt.Render(prompt.DetailExplainTemplate, struct {
			ProductName     string
			DutiesFormatted string
		}{ProductName: detail.ProductName, DutiesFormatted: duties})
	}
	if err != nil {
		userPrompt = fmt.Sprintf("产品名称：%s\n保障项目：\n%s\n用户追问：%s", detail.ProductName, duties, userQuestion)
	}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: prompt.DetailExplainSystem},
		{Role: llm.RoleUser, Content: userPrompt},
	}
}

func FormatDuties(duties []schema.DutyItem) string {
	if len(duties) == 0 {
		return "（无可用保障项）"
	}
	parts := make([]string, 0, len(duties))
	for _, duty := range duties {
		optional := ""
		if duty.IsOptional {
			optional = "【可选】"
		}
		parts = append(parts, fmt.Sprintf("- %s（%s）%s: %s", duty.Name, duty.Coverage, optional, duty.Description))
	}
	return strings.Join(parts, "\n")
}

func DetailItemsPayload(detail schema.ProductDetail) schema.SSEDetailItemsPayload {
	return schema.SSEDetailItemsPayload{
		ProductName: detail.ProductName,
		Duties:      append([]schema.DutyItem(nil), detail.Duties...),
	}
}

func (s *Service) emitAnswer(ctx context.Context, events chan<- Event, detail schema.ProductDetail, userQuestion string) {
	for chunk := range s.answerStream(ctx, detail, userQuestion) {
		if strings.TrimSpace(chunk) != "" {
			emitDelta(ctx, events, chunk)
		}
	}
}

func (s *Service) answerStream(ctx context.Context, detail schema.ProductDetail, userQuestion string) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		if s.model != nil {
			stream, err := s.model.StreamText(ctx, BuildAnswerMessages(detail, userQuestion), 0.4)
			if err == nil {
				wrote := false
				for chunk := range stream {
					if chunk.Err != nil {
						out <- fallbackAnswer(detail, userQuestion)
						return
					}
					if chunk.Text == "" {
						continue
					}
					wrote = true
					select {
					case <-ctx.Done():
						return
					case out <- chunk.Text:
					}
				}
				if wrote {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
		case out <- fallbackAnswer(detail, userQuestion):
		}
	}()
	return out
}

func fallbackAnswer(detail schema.ProductDetail, userQuestion string) string {
	if strings.TrimSpace(userQuestion) != "" {
		matches := matchDuties(detail.Duties, userQuestion)
		if len(matches) == 0 {
			return fmt.Sprintf("该产品的公开信息中未涉及此项。当前已识别的保障项包括：%s。具体责任和限制仍以合同条款为准。", dutyNames(detail.Duties))
		}
		return fmt.Sprintf("基于已提取的保障信息，%s。具体赔付条件、免赔额和等待期仍以合同条款为准。", summarizeDuties(matches))
	}
	return fmt.Sprintf("已为你提取 %s 的主要保障：%s。以上为页面可识别信息，具体保障内容以保险合同条款为准。", displayProductName(detail.ProductName), summarizeDuties(detail.Duties))
}

func matchDuties(duties []schema.DutyItem, question string) []schema.DutyItem {
	var matched []schema.DutyItem
	for _, duty := range duties {
		if intersects(question, duty.Name) || intersects(question, duty.Description) {
			matched = append(matched, duty)
		}
	}
	return matched
}

func intersects(a, b string) bool {
	return containsAnyToken(a, b) || containsAnyToken(b, a)
}

func containsAnyToken(haystack, needleSource string) bool {
	for _, seg := range cnSegmentRE.FindAllString(needleSource, -1) {
		runes := []rune(seg)
		for size := 4; size >= 2; size-- {
			if len(runes) < size {
				continue
			}
			for i := 0; i+size <= len(runes); i++ {
				if strings.Contains(haystack, string(runes[i:i+size])) {
					return true
				}
			}
		}
	}
	return false
}

func summarizeDuties(duties []schema.DutyItem) string {
	if len(duties) == 0 {
		return "暂未识别到明确保障项"
	}
	limit := len(duties)
	if limit > 4 {
		limit = 4
	}
	parts := make([]string, 0, limit)
	for _, duty := range duties[:limit] {
		coverage := strings.TrimSpace(duty.Coverage)
		if coverage == "" {
			coverage = "以页面说明为准"
		}
		desc := strings.TrimSpace(duty.Description)
		if desc != "" {
			parts = append(parts, fmt.Sprintf("%s保额/限额为%s，%s", duty.Name, coverage, desc))
		} else {
			parts = append(parts, fmt.Sprintf("%s保额/限额为%s", duty.Name, coverage))
		}
	}
	return strings.Join(parts, "；")
}

func dutyNames(duties []schema.DutyItem) string {
	if len(duties) == 0 {
		return "暂无"
	}
	names := make([]string, 0, len(duties))
	for _, duty := range duties {
		names = append(names, duty.Name)
	}
	return strings.Join(names, "、")
}

func displayProductName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "该产品"
	}
	return name
}

func InferPlatform(productURL string) string {
	parsed, err := url.Parse(productURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	host := strings.ToLower(parsed.Host)
	switch {
	case strings.Contains(host, "xiaoyusan"):
		return "xiaoyusan"
	case strings.Contains(host, "huize"):
		return "huize"
	case strings.Contains(host, "pingan"):
		return "pingan"
	default:
		return host
	}
}

func emitStatus(ctx context.Context, events chan<- Event, stage, message string) {
	emit(ctx, events, Event{Name: schema.SSEEventStatus, Data: schema.SSEStatusPayload{Stage: stage, Message: message}})
}

func emitDelta(ctx context.Context, events chan<- Event, text string) {
	emit(ctx, events, Event{Name: schema.SSEEventDelta, Data: schema.SSEDeltaPayload{Text: text}})
}

func emit(ctx context.Context, events chan<- Event, event Event) {
	select {
	case <-ctx.Done():
	case events <- event:
	}
}
