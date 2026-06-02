package chatflow

import (
	"context"
	"strings"
	"time"
)

const (
	EventStatus      = "status"
	EventDelta       = "delta"
	EventSources     = "sources"
	EventDisclaimer  = "disclaimer"
	EventDone        = "done"
	EventError       = "error"
	EventProducts    = "products"
	EventDetailItems = "detail_items"
)

const disclaimerText = "以上信息仅供参考，具体保障内容请以保险合同条款为准。"

var productSearchIntents = map[string]bool{
	"product_recommendation": true,
	"product_query":          true,
	"comparison":             true,
}

type Request struct {
	Message       string
	RequestID     string
	Action        string
	ProductURL    string
	ProductName   string
	AnonymousID   string
	UserID        string
	ChatSessionID string
	Metadata      map[string]any
	History       []ChatMessage
}

type ChatMessage struct {
	ID        string
	Role      string
	Content   string
	Metadata  map[string]any
	CreatedAt time.Time
}

type Event struct {
	Name string
	Data any
}

type Runner interface {
	Run(ctx context.Context, req Request) <-chan Event
}

type Flow struct {
	Intent   IntentClassifier
	Search   ProductSearcher
	Answer   AnswerStreamer
	Followup FollowupGenerator
	Fallback FallbackSearcher
	Detail   DetailRunner
}

type IntentClassifier interface {
	Classify(ctx context.Context, message string) (IntentResult, error)
}

type HistoryIntentClassifier interface {
	ClassifyWithHistory(ctx context.Context, message string, history []ChatMessage) (IntentResult, error)
}

type ProductSearcher interface {
	Search(ctx context.Context, message string) ([]ProductCard, error)
}

type AnswerStreamer interface {
	Stream(ctx context.Context, input AnswerInput) (<-chan string, <-chan error)
}

type FollowupGenerator interface {
	Generate(ctx context.Context, missingSlots []string) (string, error)
}

type FallbackSearcher interface {
	Search(ctx context.Context, query string) ([]SearchResultItem, error)
}

type DetailRunner interface {
	Run(ctx context.Context, req DetailRequest) <-chan Event
}

type DetailRequest struct {
	ProductURL   string
	ProductName  string
	UserQuestion string
	RequestID    string
	Action       string
}

type IntentResult struct {
	Intent        string   `json:"intent"`
	NeedsFollowup bool     `json:"needs_followup"`
	MissingSlots  []string `json:"missing_slots"`
	Reason        string   `json:"reason"`
}

type ProductCard struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Company    string   `json:"company"`
	Price      *string  `json:"price"`
	PriceLabel string   `json:"price_label"`
	Tags       []string `json:"tags"`
	URL        string   `json:"url"`
	Platform   string   `json:"platform"`
	Brief      string   `json:"brief"`
}

type SearchResultItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Site    string `json:"site"`
	Snippet string `json:"snippet"`
}

type SourceItem struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Site  string `json:"site"`
}

type AnswerInput struct {
	Message string
	Intent  string
	Results []SearchResultItem
	History []ChatMessage
}

func New() *Flow {
	return &Flow{
		Intent:   defaultIntentClassifier{},
		Answer:   defaultAnswerStreamer{},
		Fallback: defaultFallbackSearcher{},
	}
}

func (f *Flow) Run(ctx context.Context, req Request) <-chan Event {
	events := make(chan Event)
	go func() {
		defer close(events)
		f.ensureDefaults()
		f.run(ctx, req, events)
	}()
	return events
}

func (f *Flow) ensureDefaults() {
	if f.Intent == nil {
		f.Intent = defaultIntentClassifier{}
	}
	if f.Answer == nil {
		f.Answer = defaultAnswerStreamer{}
	}
	if f.Followup == nil {
		f.Followup = defaultFollowupGenerator{}
	}
	if f.Fallback == nil {
		f.Fallback = defaultFallbackSearcher{}
	}
}

func (f *Flow) run(ctx context.Context, req Request, events chan<- Event) {
	if req.Action == "product_detail" || req.Action == "product_followup" {
		f.runDetail(ctx, req, events)
		return
	}

	events <- Event{Name: EventStatus, Data: map[string]string{"stage": "analyzing", "message": "正在分析您的问题..."}}
	intent, err := f.classify(ctx, req)
	if err != nil {
		events <- errorEvent("INTERNAL_ERROR", err.Error(), req.RequestID)
		return
	}

	if intent.Intent == "out_of_scope" {
		events <- Event{Name: EventDelta, Data: map[string]string{"text": "我目前专注于保险咨询。您如果想了解重疾险、医疗险、条款解读或产品对比，我可以继续帮您。"}}
		events <- Event{Name: EventDone, Data: map[string]string{"requestId": req.RequestID}}
		return
	}

	if intent.NeedsFollowup {
		text, err := f.Followup.Generate(ctx, intent.MissingSlots)
		if err != nil || strings.TrimSpace(text) == "" {
			text = followupText(intent.MissingSlots)
		}
		events <- Event{Name: EventDelta, Data: map[string]string{"text": text}}
		events <- Event{Name: EventDone, Data: map[string]string{"requestId": req.RequestID}}
		return
	}

	events <- Event{Name: EventStatus, Data: map[string]string{"stage": "searching", "message": "正在搜索保险产品..."}}
	if productSearchIntents[intent.Intent] && f.Search != nil {
		products, err := f.Search.Search(ctx, req.Message)
		if err == nil && len(products) > 0 {
			events <- Event{Name: EventProducts, Data: map[string]any{"items": products}}
		}
	}

	results, err := f.Fallback.Search(ctx, req.Message)
	if err != nil {
		results = nil
	}
	sources := uniqueSources(results)

	events <- Event{Name: EventStatus, Data: map[string]string{"stage": "answering", "message": "正在生成回答..."}}
	chunks, errs := f.Answer.Stream(ctx, AnswerInput{Message: req.Message, Intent: intent.Intent, Results: results, History: req.History})
	for chunks != nil || errs != nil {
		select {
		case <-ctx.Done():
			events <- errorEvent("INTERNAL_ERROR", ctx.Err().Error(), req.RequestID)
			return
		case chunk, ok := <-chunks:
			if !ok {
				chunks = nil
				continue
			}
			if chunk != "" {
				events <- Event{Name: EventDelta, Data: map[string]string{"text": chunk}}
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				events <- Event{Name: EventDelta, Data: map[string]string{"text": "抱歉，回答生成超时。请查看上方的产品卡片了解详情，或稍后重试。"}}
				chunks = nil
				errs = nil
			}
		}
	}

	if len(sources) > 0 {
		events <- Event{Name: EventSources, Data: map[string]any{"items": sources}}
	}
	events <- Event{Name: EventDisclaimer, Data: map[string]string{"text": disclaimerText}}
	events <- Event{Name: EventDone, Data: map[string]string{"requestId": req.RequestID}}
}

func (f *Flow) classify(ctx context.Context, req Request) (IntentResult, error) {
	if classifier, ok := f.Intent.(HistoryIntentClassifier); ok {
		return classifier.ClassifyWithHistory(ctx, req.Message, req.History)
	}
	return f.Intent.Classify(ctx, req.Message)
}

func (f *Flow) runDetail(ctx context.Context, req Request, events chan<- Event) {
	if strings.TrimSpace(req.ProductURL) == "" {
		events <- errorEvent("INVALID_ARGUMENT", "productUrl 不能为空", req.RequestID)
		events <- Event{Name: EventDone, Data: map[string]string{"requestId": req.RequestID}}
		return
	}
	if f.Detail == nil {
		events <- Event{Name: EventError, Data: map[string]any{
			"code":      "NOT_IMPLEMENTED",
			"message":   "产品详情/追问 Skill 尚未配置",
			"requestId": req.RequestID,
		}}
		events <- Event{Name: EventDone, Data: map[string]string{"requestId": req.RequestID}}
		return
	}

	detailEvents := f.Detail.Run(ctx, DetailRequest{
		ProductURL:   strings.TrimSpace(req.ProductURL),
		ProductName:  strings.TrimSpace(req.ProductName),
		UserQuestion: strings.TrimSpace(req.Message),
		RequestID:    req.RequestID,
		Action:       req.Action,
	})
	sawDone := false
	for detailEvents != nil {
		select {
		case <-ctx.Done():
			events <- errorEvent("INTERNAL_ERROR", ctx.Err().Error(), req.RequestID)
			events <- Event{Name: EventDone, Data: map[string]string{"requestId": req.RequestID}}
			return
		case event, ok := <-detailEvents:
			if !ok {
				detailEvents = nil
				continue
			}
			if event.Name == "" {
				continue
			}
			if event.Name == EventDone {
				sawDone = true
			}
			events <- event
		}
	}
	if !sawDone {
		events <- Event{Name: EventDone, Data: map[string]string{"requestId": req.RequestID}}
	}
}

func errorEvent(code, message, requestID string) Event {
	return Event{Name: EventError, Data: map[string]string{"code": code, "message": message, "requestId": requestID}}
}

func uniqueSources(results []SearchResultItem) []SourceItem {
	seen := make(map[string]bool, len(results))
	sources := make([]SourceItem, 0, len(results))
	for _, result := range results {
		if result.URL == "" || seen[result.URL] {
			continue
		}
		seen[result.URL] = true
		sources = append(sources, SourceItem{Title: result.Title, URL: result.URL, Site: result.Site})
	}
	return sources
}

func followupText(missing []string) string {
	if len(missing) == 0 {
		return "为了更准确地给出建议，请补充被保人的年龄、预算和想重点保障的风险。"
	}
	return "为了更准确地给出建议，请补充：" + strings.Join(missing, "、") + "。"
}
