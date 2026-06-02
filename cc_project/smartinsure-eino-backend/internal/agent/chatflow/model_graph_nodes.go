package chatflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"smartinsure-eino-backend/internal/compliance"
	"smartinsure-eino-backend/internal/llm"
	"smartinsure-eino-backend/internal/prompt"
	"smartinsure-eino-backend/internal/service/answer"
	"smartinsure-eino-backend/internal/service/followup"
	"smartinsure-eino-backend/internal/service/intent"
	toolcontract "smartinsure-eino-backend/internal/tool"
	toolsearch "smartinsure-eino-backend/internal/tool/search"

	einoschema "github.com/cloudwego/eino/schema"
)

const answerFallbackText = "抱歉，回答生成超时。请查看上方的产品卡片了解详情，或稍后重试。"

func (g *GraphFlow) intentPromptNode(ctx context.Context, state *graphState) ([]*einoschema.Message, error) {
	if err := state.emit(ctx, Event{Name: EventStatus, Data: map[string]string{"stage": "analyzing", "message": "正在分析您的问题..."}}); err != nil {
		return nil, err
	}
	userPrompt, err := prompt.BuildIntentUserPromptWithHistory(
		state.Request.Message,
		intent.FormatHistoryContext(toIntentHistory(state.Request.History)),
	)
	if err != nil {
		return nil, fmt.Errorf("build intent prompt: %w", err)
	}
	return toEinoMessages([]llm.Message{
		{Role: llm.RoleSystem, Content: prompt.System},
		{Role: llm.RoleUser, Content: userPrompt},
	}), nil
}

func (g *GraphFlow) intentParseNode(ctx context.Context, msg *einoschema.Message) (*graphState, error) {
	state, err := currentGraphState(ctx)
	if err != nil {
		return nil, err
	}
	content := cleanModelContent(msg)
	var data map[string]any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		_ = state.emit(ctx, errorEvent("INTERNAL_ERROR", fmt.Sprintf("classify intent: %v", err), state.Request.RequestID))
		return state, nil
	}
	result := intent.ApplyFollowupRules(intent.ValidateIntent(data))
	state.Intent = IntentResult{
		Intent:        result.Intent,
		NeedsFollowup: result.NeedsFollowup,
		MissingSlots:  result.MissingSlots,
		Reason:        result.Reason,
	}
	return state, nil
}

func (g *GraphFlow) followupPromptNode(_ context.Context, state *graphState) ([]*einoschema.Message, error) {
	userPrompt, err := followup.BuildUserPrompt(state.Intent.MissingSlots)
	if err != nil {
		return nil, err
	}
	return toEinoMessages([]llm.Message{
		{Role: llm.RoleSystem, Content: prompt.System},
		{Role: llm.RoleUser, Content: userPrompt},
	}), nil
}

func (g *GraphFlow) followupEmitNode(ctx context.Context, msg *einoschema.Message) (*graphState, error) {
	state, err := currentGraphState(ctx)
	if err != nil {
		return nil, err
	}
	text := compliance.Sanitize(strings.TrimSpace(llm.StripThink(messageContent(msg))))
	if text == "" {
		text = followupText(state.Intent.MissingSlots)
	}
	if err := state.emit(ctx, Event{Name: EventDelta, Data: map[string]string{"text": text}}); err != nil {
		return state, err
	}
	return state, state.emit(ctx, Event{Name: EventDone, Data: map[string]string{"requestId": state.Request.RequestID}})
}

func (g *GraphFlow) searchToolNode(ctx context.Context, state *graphState) (*graphState, error) {
	if err := state.emit(ctx, Event{Name: EventStatus, Data: map[string]string{"stage": "searching", "message": "正在搜索保险产品..."}}); err != nil {
		return state, err
	}
	output, err := g.searchTool.Invoke(ctx, toolsearch.SearchToolInput{
		Message: state.Request.Message,
		Intent:  state.Intent.Intent,
		History: toToolHistory(state.Request.History),
	})
	if err != nil {
		output = toolsearch.SearchToolOutput{}
	}
	if len(output.Products) > 0 {
		if err := state.emit(ctx, Event{Name: EventProducts, Data: map[string]any{"items": fromSchemaProductCards(output.Products)}}); err != nil {
			return state, err
		}
	}
	state.SearchOutput = output
	state.Results = fromSchemaSearchResults(output.Results)
	state.Sources = fromSchemaSources(output.Sources)
	return state, nil
}

func (g *GraphFlow) answerPromptNode(ctx context.Context, state *graphState) ([]*einoschema.Message, error) {
	if err := state.emit(ctx, Event{Name: EventStatus, Data: map[string]string{"stage": "answering", "message": "正在生成回答..."}}); err != nil {
		return nil, err
	}
	return toEinoMessages(answer.BuildAnswerMessagesWithHistory(
		state.Request.Message,
		state.Intent.Intent,
		toSchemaSearchResults(state.Results),
		toAnswerHistory(state.Request.History),
	)), nil
}

func (g *GraphFlow) answerStreamEmitNode(ctx context.Context, stream *einoschema.StreamReader[*einoschema.Message]) (*graphState, error) {
	state, err := currentGraphState(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	filter := newGraphThinkStreamFilter()
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return state, nil
			}
			if emitErr := state.emit(ctx, Event{Name: EventDelta, Data: map[string]string{"text": answerFallbackText}}); emitErr != nil {
				return state, emitErr
			}
			return state, nil
		}
		text := compliance.Sanitize(filter.feed(messageContent(msg)))
		if text == "" {
			continue
		}
		if err := state.emit(ctx, Event{Name: EventDelta, Data: map[string]string{"text": text}}); err != nil {
			return state, err
		}
	}
}

func toEinoMessages(messages []llm.Message) []*einoschema.Message {
	out := make([]*einoschema.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case llm.RoleSystem:
			out = append(out, einoschema.SystemMessage(message.Content))
		case llm.RoleAssistant:
			out = append(out, einoschema.AssistantMessage(message.Content, nil))
		default:
			out = append(out, einoschema.UserMessage(message.Content))
		}
	}
	return out
}

func cleanModelContent(msg *einoschema.Message) string {
	return llm.StripMarkdownFence(llm.StripThink(messageContent(msg)))
}

func messageContent(msg *einoschema.Message) string {
	if msg == nil {
		return ""
	}
	return msg.Content
}

func toToolHistory(history []ChatMessage) []toolcontract.ChatMessage {
	out := make([]toolcontract.ChatMessage, 0, len(history))
	for _, item := range history {
		out = append(out, toolcontract.ChatMessage{
			ID:        item.ID,
			Role:      item.Role,
			Content:   item.Content,
			Metadata:  item.Metadata,
			CreatedAt: item.CreatedAt,
		})
	}
	return out
}

type graphThinkStreamFilter struct {
	inThink bool
}

func newGraphThinkStreamFilter() *graphThinkStreamFilter {
	return &graphThinkStreamFilter{}
}

func (f *graphThinkStreamFilter) feed(text string) string {
	if text == "" {
		return ""
	}
	if f.inThink {
		end := strings.Index(text, "</think>")
		if end < 0 {
			return ""
		}
		f.inThink = false
		text = text[end+len("</think>"):]
	}
	start := strings.Index(text, "<think>")
	if start < 0 {
		return text
	}
	before := text[:start]
	after := text[start+len("<think>"):]
	end := strings.Index(after, "</think>")
	if end < 0 {
		f.inThink = true
		return before
	}
	return before + after[end+len("</think>"):]
}
