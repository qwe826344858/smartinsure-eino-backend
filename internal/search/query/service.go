package query

import (
	"context"
	"fmt"
	"regexp"
	"smartinsure-eino-backend/internal/logx"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/prompt"
	"smartinsure-eino-backend/internal/schema"
)

const (
	IntentProductQuery          = schema.IntentProductQuery
	IntentProductRecommendation = schema.IntentProductRecommendation
)

var (
	insuranceTypeRE = regexp.MustCompile(`(百万医疗|医疗保险|医疗险|重疾险|意外险|寿险|年金险|防癌险|健康险|养老保险|定期寿险|终身寿险)`)
	productNameRE   = regexp.MustCompile(`《([^》]{2,40})》|「([^」]{2,40})」|【([^】]{2,40})】`)
)

type Service struct {
	model llm.ChatModel
	now   func() time.Time
}

type Option func(*Service)

func NewService(model llm.ChatModel, opts ...Option) *Service {
	s := &Service{
		model: model,
		now:   time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func (s *Service) Generate(ctx context.Context, message, intent string) ([]string, error) {
	startedAt := time.Now()
	if s == nil {
		s = NewService(nil)
	}
	logx.Printf("运行日志", "runtime log", "query generate_start intent=%s message_chars=%d has_model=%t", intent, len([]rune(strings.TrimSpace(message))), s.model != nil)

	var queries []string
	if s.model != nil {
		generated, err := s.generateWithLLM(ctx, message, intent)
		if err == nil {
			queries = generated
			logx.Printf("运行日志", "runtime log", "query llm_success intent=%s queries=%d", intent, len(generated))
		} else {
			logx.Printf("运行日志", "runtime log", "query llm_failed intent=%s err=%v", intent, err)
		}
	}
	if len(queries) == 0 {
		queries = s.fallback(message, intent)
		logx.Printf("运行日志", "runtime log", "query fallback_used intent=%s queries=%d", intent, len(queries))
	}

	queries = normalizeQueries(queries)
	queries = ensureMinimumQueries(queries, s.fallback(message, intent), 3)
	if isTimeSensitiveIntent(intent) {
		queries = s.injectCurrentYear(queries)
	}
	if len(queries) > 5 {
		queries = queries[:5]
	}
	logx.Printf("运行日志", "runtime log", "query generate_success intent=%s queries=%d duration_ms=%d", intent, len(queries), time.Since(startedAt).Milliseconds())
	return queries, nil
}

func (s *Service) generateWithLLM(ctx context.Context, message, intent string) ([]string, error) {
	userPrompt, err := prompt.BuildQueryUserPrompt(message, intent)
	if err != nil {
		return nil, fmt.Errorf("build query prompt: %w", err)
	}
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: prompt.System},
		{Role: llm.RoleUser, Content: userPrompt},
	}
	var data map[string]any
	if err := s.model.CallJSON(ctx, messages, 0.3, &data); err != nil {
		return nil, fmt.Errorf("generate query: %w", err)
	}
	return schema.ValidateQuery(data).Queries, nil
}

func (s *Service) fallback(message, intent string) []string {
	core := extractCoreKeyword(message)
	switch strings.TrimSpace(intent) {
	case schema.IntentProductRecommendation:
		return []string{
			core + " 推荐",
			core + " 产品测评",
			core + " 排行 价格",
			core + " 保费 多少钱",
		}
	case schema.IntentProductQuery:
		return []string{
			core + " 测评",
			core + " 价格 保费",
			core + " 保障责任",
			core + " 投保 条款",
		}
	case schema.IntentComparison:
		return []string{
			core + " 对比",
			core + " 区别",
			core + " 哪个好",
		}
	case schema.IntentClauseExplain:
		return []string{
			core + " 条款解读",
			core + " 责任免除",
			core + " 等待期 赔付条件",
		}
	case schema.IntentUnderwritingBasic:
		return []string{
			core + " 核保",
			core + " 健康告知",
			core + " 带病投保",
		}
	default:
		return []string{
			core + " 保险知识",
			core + " 保险解读",
			core + " 常见问题",
		}
	}
}

func (s *Service) injectCurrentYear(queries []string) []string {
	year := fmt.Sprintf("%d", s.now().Year())
	out := make([]string, 0, len(queries))
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if !strings.Contains(q, year) {
			q += " " + year
		}
		out = append(out, q)
	}
	return out
}

func isTimeSensitiveIntent(intent string) bool {
	switch strings.TrimSpace(intent) {
	case schema.IntentProductRecommendation, schema.IntentProductQuery:
		return true
	default:
		return false
	}
}

func extractCoreKeyword(message string) string {
	text := strings.TrimSpace(message)
	if text == "" {
		return "保险"
	}
	if match := productNameRE.FindStringSubmatch(text); len(match) > 0 {
		for _, group := range match[1:] {
			if strings.TrimSpace(group) != "" {
				return strings.TrimSpace(group)
			}
		}
	}
	if match := insuranceTypeRE.FindString(text); match != "" {
		switch match {
		case "医疗保险", "医疗险", "健康险":
			return "百万医疗险"
		case "养老保险":
			return "年金险"
		default:
			return match
		}
	}
	runes := []rune(text)
	if len(runes) > 24 {
		text = string(runes[:24])
	}
	return strings.Trim(text, " ，。！？?：:")
}

func normalizeQueries(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, q := range in {
		q = strings.Join(strings.Fields(strings.TrimSpace(q)), " ")
		if q == "" {
			continue
		}
		if _, ok := seen[q]; ok {
			continue
		}
		seen[q] = struct{}{}
		out = append(out, q)
	}
	return out
}

func ensureMinimumQueries(primary, fallback []string, min int) []string {
	if len(primary) >= min {
		return primary
	}
	out := append([]string(nil), primary...)
	seen := map[string]struct{}{}
	for _, q := range out {
		seen[q] = struct{}{}
	}
	for _, q := range normalizeQueries(fallback) {
		if _, ok := seen[q]; ok {
			continue
		}
		seen[q] = struct{}{}
		out = append(out, q)
		if len(out) >= min {
			break
		}
	}
	return out
}
