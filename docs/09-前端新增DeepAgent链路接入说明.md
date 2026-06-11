# 前端新增 `/api/agent/deep-chat` 链路接入说明

## 目标

前端新增一条 DeepAgent 实验链路，请求后端：

```text
POST /api/agent/deep-chat
```

这条链路用于验证 Eino DeepAgent。现有 `/api/agent/chat` 保持原 Plan-Act 链路不变。前端可以复用已有 `/api/agent/chat` 的请求体结构、SSE 解析逻辑和消息渲染逻辑，只需要新增一个调用入口或实验开关。

## 接口差异

| 项目 | 旧 Agent 链路 | 新 DeepAgent 链路 |
| --- | --- | --- |
| URL | `/api/agent/chat` | `/api/agent/deep-chat` |
| Method | `POST` | `POST` |
| Response | `text/event-stream` | `text/event-stream` |
| 请求体 | 相同 | 相同 |
| SSE 事件 | 相同事件集合 | 相同事件集合 |
| `agent_id` | 默认 `smartinsure-advisor` | 后端强制为 `smartinsure-deep-advisor` |

注意：调用 `/api/agent/deep-chat` 时，请求体里的 `agent_id` 即使传了也会被后端忽略，最终固定走 DeepAgent。

## 请求体

普通问答：

```json
{
  "message": "百万医疗险怎么选？",
  "requestId": "rid-front-001",
  "metadata": {
    "source": "web"
  }
}
```

带会话记忆：

```json
{
  "message": "那这类产品适合老人买吗？",
  "chat_session_id": "session_xxx",
  "anonymous_id": "anon_xxx",
  "requestId": "rid-front-002"
}
```

产品详情动作：

```json
{
  "action": "product_detail",
  "productUrl": "https://example.com/product",
  "productName": "测试产品",
  "requestId": "rid-front-003"
}
```

字段说明：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `message` | 普通问答必填 | 用户输入内容。没有 `action` 时不能为空 |
| `action` | 否 | 当前支持复用已有 `product_detail` 动作 |
| `productUrl` / `product_url` | 产品详情必填 | 产品详情页地址，两种命名都兼容 |
| `productName` / `product_name` | 否 | 产品名称，两种命名都兼容 |
| `requestId` | 否 | 前端生成的请求 ID；不传时后端会生成 |
| `chat_session_id` | 否 | 会话 ID；用于后端记忆 |
| `sessionId` | 否 | `chat_session_id` 的兼容字段 |
| `anonymous_id` | 否 | 匿名用户 ID；配合会话记忆使用 |
| `metadata` | 否 | 前端附加信息 |
| `agent_id` | 否 | DeepAgent 链路会忽略该字段 |

## SSE 事件

响应格式与现有 Agent 链路一致：

```text
event: status
data: {"stage":"reasoning","message":"DeepAgent 正在规划下一步...","agent_id":"smartinsure-deep-advisor","requestId":"rid-front-001","request_id":"rid-front-001","trace_id":"trace_xxx"}

event: delta
data: {"text":"这里是模型流式输出片段...","agent_id":"smartinsure-deep-advisor","requestId":"rid-front-001","request_id":"rid-front-001","trace_id":"trace_xxx"}

event: done
data: {"requestId":"rid-front-001","request_id":"rid-front-001","agent_id":"smartinsure-deep-advisor","trace_id":"trace_xxx"}
```

前端需要继续支持以下事件：

| 事件名 | 用途 |
| --- | --- |
| `status` | 展示阶段状态，例如规划、选择工具、调用工具 |
| `delta` | 追加助手回答文本，读取 `data.text` |
| `products` | 产品卡片列表，读取 `data.items` |
| `sources` | 知识来源列表，读取 `data.items` |
| `detail_items` | 产品详情结构化数据 |
| `disclaimer` | 免责声明文本，读取 `data.text` |
| `error` | 错误事件，读取 `data.code` 和 `data.message` |
| `done` | 流结束事件 |

`data.agent_id` 在新链路中应为：

```text
smartinsure-deep-advisor
```

`data.trace_id` 只有后端开启 trace 时才会出现，前端不要依赖它必定存在。

## 前端改动建议

新增一个 API 方法，复用现有 Agent SSE 处理函数：

```ts
const API_BASE = import.meta.env.VITE_API_BASE_URL ?? "";

export function streamDeepAgentChat(payload: AgentChatPayload, handlers: AgentSSEHandlers) {
  return streamAgentSSE(`${API_BASE}/api/agent/deep-chat`, payload, handlers);
}
```

如果当前已有类似方法：

```ts
streamAgentChat("/api/agent/chat", payload, handlers)
```

则新增：

```ts
streamDeepAgentChat("/api/agent/deep-chat", payload, handlers)
```

不要新写一套 SSE parser。DeepAgent 的返回事件已经按现有前端事件协议适配。

## 验证方式

本地后端当前可用时，前端或浏览器环境请求：

```text
http://<backend-host>:34567/api/agent/deep-chat
```

命令行 smoke test：

```bash
curl -N \
  -H 'Content-Type: application/json' \
  -X POST \
  http://127.0.0.1:34567/api/agent/deep-chat \
  -d '{"message":"百万医疗险怎么选？","requestId":"rid-deep-smoke"}'
```

验收点：

1. 能收到 `status`、`delta`、`done` 等 SSE 事件。
2. `delta.data.text` 能像旧链路一样持续追加到聊天气泡。
3. 事件里的 `agent_id` 为 `smartinsure-deep-advisor`。
4. `/api/agent/chat` 仍然保持旧链路，不受新增入口影响。
5. 如果使用会话记忆，`done` 事件里可能带回 `chat_session_id`，前端继续按旧逻辑保存即可。

