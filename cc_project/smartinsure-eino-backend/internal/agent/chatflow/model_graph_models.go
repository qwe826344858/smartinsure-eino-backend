package chatflow

import (
	"context"
	"encoding/json"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	einoschema "github.com/cloudwego/eino/schema"
)

func (m GraphChatModels) withFallback(flow *Flow) GraphChatModels {
	if m.Intent == nil {
		m.Intent = flowIntentChatModel{flow: flow}
	}
	if m.Followup == nil {
		m.Followup = flowFollowupChatModel{flow: flow}
	}
	if m.Answer == nil {
		m.Answer = flowAnswerChatModel{flow: flow}
	}
	return m
}

type flowIntentChatModel struct {
	flow *Flow
}

func (m flowIntentChatModel) Generate(ctx context.Context, _ []*einoschema.Message, _ ...einomodel.Option) (*einoschema.Message, error) {
	state, err := currentGraphState(ctx)
	if err != nil {
		return nil, err
	}
	result, err := m.flow.classify(ctx, state.Request)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return einoschema.AssistantMessage(string(payload), nil), nil
}

func (m flowIntentChatModel) Stream(ctx context.Context, input []*einoschema.Message, opts ...einomodel.Option) (*einoschema.StreamReader[*einoschema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return einoschema.StreamReaderFromArray([]*einoschema.Message{msg}), nil
}

type flowFollowupChatModel struct {
	flow *Flow
}

func (m flowFollowupChatModel) Generate(ctx context.Context, _ []*einoschema.Message, _ ...einomodel.Option) (*einoschema.Message, error) {
	state, err := currentGraphState(ctx)
	if err != nil {
		return nil, err
	}
	text, err := m.flow.Followup.Generate(ctx, state.Intent.MissingSlots)
	if err != nil || strings.TrimSpace(text) == "" {
		text = followupText(state.Intent.MissingSlots)
	}
	return einoschema.AssistantMessage(text, nil), nil
}

func (m flowFollowupChatModel) Stream(ctx context.Context, input []*einoschema.Message, opts ...einomodel.Option) (*einoschema.StreamReader[*einoschema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return einoschema.StreamReaderFromArray([]*einoschema.Message{msg}), nil
}

type flowAnswerChatModel struct {
	flow *Flow
}

func (m flowAnswerChatModel) Generate(ctx context.Context, input []*einoschema.Message, opts ...einomodel.Option) (*einoschema.Message, error) {
	stream, err := m.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	return einoschema.ConcatMessageStream(stream)
}

func (m flowAnswerChatModel) Stream(ctx context.Context, _ []*einoschema.Message, _ ...einomodel.Option) (*einoschema.StreamReader[*einoschema.Message], error) {
	state, err := currentGraphState(ctx)
	if err != nil {
		return nil, err
	}
	chunks, errs := m.flow.Answer.Stream(ctx, AnswerInput{
		Message: state.Request.Message,
		Intent:  state.Intent.Intent,
		Results: state.Results,
		History: state.Request.History,
	})
	stream, writer := einoschema.Pipe[*einoschema.Message](0)
	go func() {
		defer writer.Close()
		for chunks != nil || errs != nil {
			select {
			case <-ctx.Done():
				writer.Send(nil, ctx.Err())
				return
			case chunk, ok := <-chunks:
				if !ok {
					chunks = nil
					continue
				}
				if chunk != "" {
					writer.Send(einoschema.AssistantMessage(chunk, nil), nil)
				}
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				if err != nil {
					writer.Send(nil, err)
					return
				}
			}
		}
	}()
	return stream, nil
}
