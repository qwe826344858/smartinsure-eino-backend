package chatflow

import (
	"context"
	"log"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/compliance"
	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/platform"
	"smartinsure-eino-backend/internal/schema"
	"smartinsure-eino-backend/internal/search/fallback"
	"smartinsure-eino-backend/internal/service/answer"
	"smartinsure-eino-backend/internal/service/followup"
	"smartinsure-eino-backend/internal/service/intent"
	"smartinsure-eino-backend/internal/service/productsearch"
	"smartinsure-eino-backend/internal/skill/productdetail"
)

const defaultProviderConfigPath = "configs/llm_providers.yaml"

const (
	OrchestratorLite      = "lite"
	OrchestratorEinoGraph = "eino_graph"
)

func NewProductionRunner() Runner {
	settings := config.Load()
	flow := NewProduction()
	switch strings.ToLower(strings.TrimSpace(settings.Orchestrator)) {
	case "eino", "graph", OrchestratorEinoGraph:
		opts := []GraphOption{}
		if models, err := graphChatModelsForProduction(settings); err == nil {
			opts = append(opts, WithGraphChatModels(models))
		} else {
			log.Printf("failed to initialize eino chat models, using graph fallback adapters: %v", err)
		}
		runner, err := NewGraphFlow(flow, opts...)
		if err != nil {
			log.Printf("failed to compile eino graph orchestrator, falling back to lite: %v", err)
			return flow
		}
		return runner
	default:
		return flow
	}
}

func graphChatModelsForProduction(settings config.Settings) (GraphChatModels, error) {
	registry, err := llm.LoadRegistry(defaultProviderConfigPath, settings)
	if err != nil {
		return GraphChatModels{}, err
	}
	ctx := context.Background()
	intentModel, _, err := registry.EinoChatModelForStage(ctx, "intent")
	if err != nil {
		return GraphChatModels{}, err
	}
	followupModel, _, err := registry.EinoChatModelForStage(ctx, "followup")
	if err != nil {
		return GraphChatModels{}, err
	}
	answerModel, _, err := registry.EinoChatModelForStage(ctx, "answer")
	if err != nil {
		return GraphChatModels{}, err
	}
	return GraphChatModels{
		Intent:   intentModel,
		Followup: followupModel,
		Answer:   answerModel,
	}, nil
}

func NewProduction() *Flow {
	flow := New()
	settings := config.Load()

	flow.Search = productSearchAdapter{service: productsearch.New()}
	flow.Fallback = fallbackAdapter{service: fallback.NewService(nil)}
	flow.Detail = detailAdapter{service: productdetail.NewService()}

	registry, err := llm.LoadRegistry(defaultProviderConfigPath, settings)
	if err != nil {
		return flow
	}

	if model := modelForStage(registry, settings, "intent"); model != nil {
		flow.Intent = intentAdapter{service: intent.NewService(model)}
	}
	if model := modelForStage(registry, settings, "answer"); model != nil {
		flow.Answer = answerAdapter{service: answer.NewService(model)}
	}
	if model := modelForStage(registry, settings, "followup"); model != nil {
		flow.Followup = followupAdapter{service: followup.NewService(model)}
	}
	if model := modelForStage(registry, settings, "detail"); model != nil {
		flow.Detail = detailAdapter{service: productdetail.NewService(productdetail.WithModel(model))}
	}
	return flow
}

func modelForStage(registry *llm.Registry, settings config.Settings, stage string) llm.ChatModel {
	if registry == nil {
		return nil
	}
	provider := registry.ForStage(stage)
	if provider.Key == "" || provider.Base == "" || provider.Model == "" {
		return nil
	}
	return llm.NewClient(
		provider,
		time.Duration(settings.LLMTimeout)*time.Second,
		settings.LLMMaxRetries,
	)
}

type intentAdapter struct {
	service *intent.Service
}

func (a intentAdapter) Classify(ctx context.Context, message string) (IntentResult, error) {
	result, err := a.service.Classify(ctx, message)
	return toChatflowIntentResult(result, err)
}

func (a intentAdapter) ClassifyWithHistory(ctx context.Context, message string, history []ChatMessage) (IntentResult, error) {
	result, err := a.service.ClassifyWithHistory(ctx, message, toIntentHistory(history))
	return toChatflowIntentResult(result, err)
}

func toChatflowIntentResult(result schema.IntentResult, err error) (IntentResult, error) {
	if err != nil {
		return IntentResult{}, err
	}
	return IntentResult{
		Intent:        result.Intent,
		NeedsFollowup: result.NeedsFollowup,
		MissingSlots:  result.MissingSlots,
		Reason:        result.Reason,
	}, nil
}

type answerAdapter struct {
	service *answer.Service
}

func (a answerAdapter) Stream(ctx context.Context, input AnswerInput) (<-chan string, <-chan error) {
	stream, err := a.service.GenerateStreamWithHistory(ctx, input.Message, input.Intent, toSchemaSearchResults(input.Results), toAnswerHistory(input.History))
	if err != nil {
		text := make(chan string)
		errs := make(chan error, 1)
		close(text)
		errs <- err
		close(errs)
		return text, errs
	}

	text := make(chan string)
	errs := make(chan error, 1)
	go func() {
		defer close(text)
		defer close(errs)
		for chunk := range stream {
			if chunk.Err != nil {
				errs <- chunk.Err
				return
			}
			if chunk.Text != "" {
				text <- compliance.Sanitize(chunk.Text)
			}
		}
	}()
	return text, errs
}

func toAnswerHistory(history []ChatMessage) []answer.HistoryMessage {
	out := make([]answer.HistoryMessage, 0, len(history))
	for _, item := range history {
		out = append(out, answer.HistoryMessage{
			ID:        item.ID,
			Role:      item.Role,
			Content:   item.Content,
			Metadata:  item.Metadata,
			CreatedAt: item.CreatedAt,
		})
	}
	return out
}

func toIntentHistory(history []ChatMessage) []intent.HistoryMessage {
	out := make([]intent.HistoryMessage, 0, len(history))
	for _, item := range history {
		out = append(out, intent.HistoryMessage{
			ID:        item.ID,
			Role:      item.Role,
			Content:   item.Content,
			Metadata:  item.Metadata,
			CreatedAt: item.CreatedAt,
		})
	}
	return out
}

type followupAdapter struct {
	service *followup.Service
}

func (a followupAdapter) Generate(ctx context.Context, missingSlots []string) (string, error) {
	text, err := a.service.Generate(ctx, missingSlots)
	return compliance.Sanitize(text), err
}

type fallbackAdapter struct {
	service *fallback.Service
}

func (a fallbackAdapter) Search(_ context.Context, query string) ([]SearchResultItem, error) {
	return fromSchemaSearchResults(a.service.Search(query)), nil
}

type productSearchAdapter struct {
	service *productsearch.Service
}

func (a productSearchAdapter) Search(ctx context.Context, message string) ([]ProductCard, error) {
	products, err := a.service.Search(ctx, message, productsearch.Options{MaxPerPlatform: 10, MaxTotal: 10})
	if err != nil {
		return nil, err
	}
	return fromPlatformProducts(products), nil
}

func fromPlatformProducts(products []platform.ProductCard) []ProductCard {
	out := make([]ProductCard, 0, len(products))
	for _, product := range products {
		out = append(out, ProductCard{
			ID:         product.ID,
			Name:       product.Name,
			Company:    product.Company,
			Price:      product.Price,
			PriceLabel: product.PriceLabel,
			Tags:       append([]string(nil), product.Tags...),
			URL:        product.URL,
			Platform:   product.Platform,
			Brief:      product.Brief,
		})
	}
	return out
}

func toSchemaSearchResults(results []SearchResultItem) []schema.SearchResultItem {
	out := make([]schema.SearchResultItem, 0, len(results))
	for _, result := range results {
		out = append(out, schema.SearchResultItem{
			Title:   result.Title,
			URL:     result.URL,
			Site:    result.Site,
			Snippet: result.Snippet,
		})
	}
	return out
}

func fromSchemaSearchResults(results []schema.SearchResultItem) []SearchResultItem {
	out := make([]SearchResultItem, 0, len(results))
	for _, result := range results {
		out = append(out, SearchResultItem{
			Title:   result.Title,
			URL:     result.URL,
			Site:    result.Site,
			Snippet: result.Snippet,
		})
	}
	return out
}

type detailAdapter struct {
	service *productdetail.Service
}

func (a detailAdapter) Run(ctx context.Context, req DetailRequest) <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		if a.service == nil {
			return
		}
		events := a.service.Run(ctx, productdetail.Request{
			Action:       req.Action,
			ProductURL:   req.ProductURL,
			ProductName:  req.ProductName,
			UserQuestion: req.UserQuestion,
			RequestID:    req.RequestID,
		})
		for event := range events {
			out <- Event{Name: event.Name, Data: sanitizeDeltaData(event.Name, event.Data)}
		}
	}()
	return out
}

func sanitizeDeltaData(name string, data any) any {
	if name != EventDelta {
		return data
	}
	switch payload := data.(type) {
	case schema.SSEDeltaPayload:
		payload.Text = compliance.Sanitize(payload.Text)
		return payload
	case map[string]string:
		copied := make(map[string]string, len(payload))
		for key, value := range payload {
			if key == "text" {
				value = compliance.Sanitize(value)
			}
			copied[key] = value
		}
		return copied
	case map[string]any:
		copied := make(map[string]any, len(payload))
		for key, value := range payload {
			if key == "text" {
				if text, ok := value.(string); ok {
					value = compliance.Sanitize(text)
				}
			}
			copied[key] = value
		}
		return copied
	default:
		return data
	}
}
