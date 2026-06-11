package smartinsuredeep

import (
	"encoding/json"

	"smartinsure-eino-backend/internal/agent/chatflow"
	agentruntime "smartinsure-eino-backend/internal/agent/runtime"
)

// emitToolResult 将 DeepAgent 工具返回的 JSON 文本解析成前端 SSE 事件。
// 这里不直接暴露工具原始 JSON，而是按工具类型映射为 products/sources/detail_items/status。
func emitToolResult(out chan<- agentruntime.AgentEvent, toolName string, content string, req agentruntime.AgentRequest, traceID string) {
	if content == "" {
		return
	}
	switch toolName {
	case toolProductSearch:
		// 产品搜索工具返回产品卡片，映射为 products 事件，前端可直接渲染卡片。
		var payload productSearchOutput
		if json.Unmarshal([]byte(content), &payload) == nil {
			if len(payload.Products) > 0 {
				emitRuntimeEvent(out, chatflow.EventProducts, map[string]any{"items": payload.Products}, req, traceID)
			}
			emitRuntimeEvent(out, chatflow.EventStatus, formatToolStatus(toolName), req, traceID)
			return
		}
	case toolKnowledgeSearch:
		// 知识检索工具返回 RAG 产品卡片和 sources，分别映射为 products/sources 事件。
		var payload knowledgeSearchOutput
		if json.Unmarshal([]byte(content), &payload) == nil {
			if len(payload.Products) > 0 {
				emitRuntimeEvent(out, chatflow.EventProducts, map[string]any{"items": payload.Products}, req, traceID)
			}
			if len(payload.Sources) > 0 {
				emitRuntimeEvent(out, chatflow.EventSources, map[string]any{"items": payload.Sources}, req, traceID)
			}
			emitRuntimeEvent(out, chatflow.EventStatus, formatToolStatus(toolName), req, traceID)
			return
		}
	case toolProductDetail:
		// 产品详情工具返回 detail_items，映射为详情结构化事件，保持前端协议一致。
		var payload productDetailOutput
		if json.Unmarshal([]byte(content), &payload) == nil {
			if payload.DetailItems != nil {
				emitRuntimeEvent(out, chatflow.EventDetailItems, payload.DetailItems, req, traceID)
			}
			emitRuntimeEvent(out, chatflow.EventStatus, formatToolStatus(toolName), req, traceID)
			return
		}
	}
	// JSON 解析失败或未知工具时仍输出 observing 状态，避免前端长时间停留在 tool_planning。
	emitRuntimeEvent(out, chatflow.EventStatus, formatToolStatus(toolName), req, traceID)
}
