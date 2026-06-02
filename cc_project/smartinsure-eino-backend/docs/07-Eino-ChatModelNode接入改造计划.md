# Eino ChatModelNode 接入改造计划

最后更新：2026-05-25

## 背景

当前 Go 后端已经支持两种编排模式：

| 模式 | 配置 | 当前状态 |
|---|---|---|
| 函数式编排 | `ORCHESTRATOR=lite` | 默认链路，业务可用 |
| Eino Graph 编排 | `ORCHESTRATOR=eino_graph` | 已把流程节点接入 Eino Graph，但 LLM 调用仍在 Lambda 内部 |

当前 Eino Graph 中注册的是 Lambda 节点：

```go
graph.AddLambdaNode(graphNodeIntent, compose.InvokableLambda(g.intentNode))
graph.AddLambdaNode(graphNodeAnswer, compose.InvokableLambda(g.answerNode))
graph.AddLambdaNode(graphNodeFollowup, compose.InvokableLambda(g.followupNode))
graph.AddLambdaNode(graphNodeDetail, compose.InvokableLambda(g.detailNode))
```

模型调用实际链路是：

```text
Graph Lambda 节点
  -> intent/answer/followup/productdetail service
    -> internal/llm.Client
      -> OpenAI 兼容 HTTP 接口
```

项目里已有 Eino 原生 ChatModel 工厂预留：

```text
internal/llm/eino.go
```

但该文件当前带有 `//go:build eino`，默认构建不会编译；同时 Graph 中也没有调用：

```go
graph.AddChatModelNode(...)
```

所以当前服务只是“用 Eino Graph 管流程”，还没有“用 Eino ChatModelNode 管模型节点”。

## 架构决策摘要

本次改造按以下决策执行：

1. 直接覆盖现有 `ORCHESTRATOR=eino_graph`，不新增第三种编排模式。
2. LLM 调用接入 Eino 原生 `ChatModelNode`，Graph 中必须能看到 `AddChatModelNode` 注册点。
3. 搜索能力从普通业务节点升级为 `search_tool` 工具节点，当前先走进程内实现，后续可切换到 MCP 服务。
4. 工具采用“确定性 Graph 调用”为主，不把保险推荐、产品搜索、产品详情这类强业务工具完全交给模型自由选择。
5. `lite` 保留为回滚路径，但 `eino_graph` 不再保留旧版 Lambda Graph 实现。
6. 主 Graph 只依赖 Tool 输入输出协议，不直接依赖具体平台 adapter 或 MCP 实现。

## 改造目标

本次目标是把当前 LLM 调用接入 Eino 原生 `ChatModel`，并在 Eino Graph 中显式注册模型节点，让模型调用成为可观测、可切换、可治理的 Graph 节点。

目标链路：

```text
Eino Graph
  -> Prompt 构造节点
  -> ChatModelNode
  -> 解析/后处理节点
  -> SSE 输出节点
```

必须达成：

1. `intent`、`answer`、`followup`、`product_detail` 中的 LLM 调用改为 Eino ChatModel。
2. Graph 模式下必须出现真实的 `graph.AddChatModelNode(...)` 注册点。
3. `graphNodeSearch` 改造为 `search_tool` 工具节点边界，当前进程内实现，后续支持 MCP 替换。
4. 直接覆盖现有 `ORCHESTRATOR=eino_graph` 编排实现，不再新增独立编排模式。
5. MiniMax/OpenAI 兼容 provider 仍然走现有 `configs/llm_providers.yaml` 阶段路由。
6. 保留现有 HTTP/SSE 协议、MySQL 会话、Redis 短期记忆、产品搜索结果结构。

非目标：

1. 不改前端协议。
2. 不重做产品搜索平台 adapter。
3. 不在本阶段引入新的 RAG 检索链路。
4. 不引入真实登录鉴权，继续沿用当前 `X-User-Id` 临时识别方式。

## 建议配置

不新增新的编排模式，直接升级现有 `eino_graph`：

```bash
ORCHESTRATOR=eino_graph
```

改造后的模式语义：

| 配置 | 语义 |
|---|---|
| `lite` | 函数式编排，继续使用自定义 `llm.Client`，作为回滚路径 |
| `eino_graph` | Eino Graph 编排，直接注册并执行 Eino ChatModelNode |

当前旧版 `eino_graph` 中“Lambda 节点内部调用 service 再调用自定义 llm.Client”的实现会被替换，不再保留为单独模式。如果新 Graph 初始化失败，应显式报错或回退到 `lite`，但不能悄悄回到旧版 Lambda Graph，避免误判 ChatModelNode 已经生效。

## 目标流程设计

### 普通对话主流程

```text
START
  -> action_router
    -> product_detail 子流程
    -> intent_prompt
      -> intent_model
      -> intent_parse
      -> intent_gate
        -> out_of_scope
        -> followup_prompt
          -> followup_model
          -> followup_emit
        -> search_tool
          -> answer_prompt
          -> answer_model
          -> answer_stream_emit
          -> finish
```

### 产品详情流程

```text
product_detail_router
  -> detail_cache_get
  -> detail_fetch_html
  -> detail_clean_html
  -> detail_extract_prompt
  -> detail_extract_model
  -> detail_extract_parse
  -> detail_validate
  -> detail_cache_set
  -> detail_answer_prompt
  -> detail_answer_model
  -> detail_answer_stream_emit
```

### 短期记忆输入位置

MySQL/Redis 短期记忆保持在 API 层准备：

```text
/api/chat
  -> prepareSession
  -> loadShortTermHistory
  -> chatflow.Request.History
  -> prompt 节点注入近期上下文
```

本次不改变记忆存储结构，只改变记忆进入 Prompt 的节点位置。

### 搜索工具节点定位

当前代码里的 `graphNodeSearch` 是为了 MVP 快速交付而放在同一个 Go 服务内的搜索节点。它现在同时承担：

```text
平台产品搜索
  -> 产品去重、年龄/预算/品类过滤
  -> 输出 products SSE

fallback 保险知识搜索
  -> 生成 answer prompt 所需上下文
  -> 输出 sources
```

从职责上看，`graphNodeSearch` 不应该继续被理解为普通业务 Lambda，而应定义为 Graph 里的 Tool 工具节点：

```text
intent_gate
  -> search_tool
    -> product search tool backend
    -> fallback search tool backend
  -> answer_prompt
```

MVP 阶段仍允许 `search_tool` 在当前进程内实现，直接调用现有：

```text
internal/service/productsearch
internal/search/fallback
```

但代码边界要按 Tool 设计，避免主 Graph 继续了解平台搜索细节。后续拆分时只替换 Tool backend：

```text
当前：
search_tool
  -> in-process productsearch/fallback

目标：
search_tool
  -> MCP client
    -> product-search MCP service
    -> fallback-search MCP service
```

因此本次 ChatModelNode 改造中，`search_tool` 不是模型节点，也不是回答节点，而是独立工具节点。它的输入输出协议需要稳定下来，便于后续迁到 MCP 服务。

## 模块拆分

### 1. Eino ChatModel 工厂

涉及文件：

| 文件 | 改造点 |
|---|---|
| `internal/llm/eino.go` | 去掉默认构建不可用的问题，作为常规构建的一部分 |
| `internal/llm/registry.go` | 保留阶段路由，新增 `EinoChatModelForStage` 的常规调用入口 |
| `internal/llm/client.go` | 保留为 legacy 链路；新 Graph 不再直接调用 |

职责：

1. 根据 `configs/llm_providers.yaml` 和环境变量加载 provider。
2. 为 `intent`、`answer`、`followup`、`detail` 分别创建 Eino `model.ToolCallingChatModel`。
3. 保留 provider model name 转换逻辑，例如 `minimax/MiniMax-Text-01` 转成实际模型名。
4. 明确 timeout、temperature、retry 的承载位置。

注意点：

当前自定义 `llm.Client` 内置了重试、`<think>` 过滤、JSON 清洗。迁到 Eino 后，这些能力不能丢，需要通过以下方式补齐：

| 能力 | 目标实现 |
|---|---|
| timeout | `context.WithTimeout` 或 Eino model option |
| retry | ChatModel 包装器或节点级 retry wrapper |
| `<think>` 过滤 | 模型输出后处理节点 |
| JSON fence 清洗 | 解析节点继续复用 |
| 流式 delta 清洗 | `answer_stream_emit` 节点中逐块过滤 |

### 2. Prompt 节点拆分

涉及文件：

| 文件 | 改造点 |
|---|---|
| `internal/prompt/prompts.go` | 保留模板 |
| `internal/prompt/render.go` | 保留模板渲染 |
| `internal/service/intent/service.go` | 将 Build Prompt 能力拆出，供 Graph Prompt 节点调用 |
| `internal/service/answer/service.go` | 将 Build Prompt 能力拆出，供 Graph Prompt 节点调用 |
| `internal/service/followup/service.go` | 将 Build Prompt 能力拆出，供 Graph Prompt 节点调用 |
| `internal/skill/productdetail/*.go` | 将 extract/answer Prompt 构造拆出 |

目标：

业务 service 不再直接持有 `llm.ChatModel` 调用模型，而是只提供：

```text
BuildPrompt
ParseModelOutput
ApplyBusinessRules
```

模型执行交给 Graph 中的 `ChatModelNode`。

### 3. Eino ChatModel Graph

建议新增文件：

```text
internal/agent/chatflow/model_graph.go
internal/agent/chatflow/model_graph_nodes.go
internal/agent/chatflow/model_graph_stream.go
```

新增节点常量建议：

```go
const (
	graphNodeIntentPrompt  = "intent_prompt"
	graphNodeIntentModel   = "intent_model"
	graphNodeIntentParse   = "intent_parse"
	graphNodeIntentGate    = "intent_gate"
	graphNodeFollowupPrompt = "followup_prompt"
	graphNodeFollowupModel  = "followup_model"
	graphNodeFollowupEmit   = "followup_emit"
	graphNodeSearchTool    = "search_tool"
	graphNodeAnswerPrompt  = "answer_prompt"
	graphNodeAnswerModel   = "answer_model"
	graphNodeAnswerEmit    = "answer_stream_emit"
)
```

注册要求：

```go
graph.AddChatModelNode(graphNodeIntentModel, intentModel, compose.WithNodeName("intent_model"))
graph.AddChatModelNode(graphNodeFollowupModel, followupModel, compose.WithNodeName("followup_model"))
graph.AddChatModelNode(graphNodeAnswerModel, answerModel, compose.WithNodeName("answer_model"))
```

搜索节点注册要求：

```go
graph.AddToolsNode(...)
```

或在 Eino Tool 适配未完成时，先使用 `AddLambdaNode(graphNodeSearchTool, ...)` 承载同一份 Tool 输入输出协议。即使短期仍是 Lambda，也必须按 Tool 边界组织代码，后续才能无损替换为 MCP Tool。

产品详情子流程也需要对应模型节点：

```go
graph.AddChatModelNode(graphNodeDetailExtractModel, detailModel, compose.WithNodeName("detail_extract_model"))
graph.AddChatModelNode(graphNodeDetailAnswerModel, detailModel, compose.WithNodeName("detail_answer_model"))
```

### 4. 流式 SSE 适配

当前 SSE 输出由 `GraphFlow.Run` 返回 `<-chan Event>`：

```go
func (g *GraphFlow) Run(ctx context.Context, req Request) <-chan Event
```

新 Graph 仍需返回同样的事件协议：

```text
status
products
delta
sources
disclaimer
done
error
```

`answer_model` 和 `detail_answer_model` 必须使用 Eino ChatModel 的 stream 能力。计划中优先采用：

```go
runnable.Stream(ctx, input)
```

或在 Graph 内使用可流式节点，把 `*schema.StreamReader[*schema.Message]` 转成当前项目的 `EventDelta`。

需要重点验证：

1. MiniMax 兼容接口在 Eino OpenAI ChatModel 中是否稳定支持 stream。
2. Eino 输出 chunk 的 `Content` 是否与当前 SSE delta 拆分粒度一致。
3. `<think>` 跨 chunk 过滤是否仍然正确。
4. 流式失败时是否能降级输出当前 fallback 文案。

### 5. Search Tool 与 MCP 边界

涉及文件：

| 文件 | 改造点 |
|---|---|
| `internal/agent/chatflow/graph.go` | 将 `graphNodeSearch` 概念升级为 `search_tool` |
| `internal/service/productsearch/*` | 作为进程内 Tool backend 保留 |
| `internal/search/fallback/*` | 作为进程内 Tool backend 保留 |
| `internal/tool/search/*` | 建议新增，定义 Tool 请求、响应和 in-process 实现 |
| `internal/search/mcp/*` | 后续用于 MCP client，对接独立搜索服务 |

建议 Tool 输入：

```go
type SearchToolInput struct {
	Message string
	Intent  string
	History []ChatMessage
}
```

建议 Tool 输出：

```go
type SearchToolOutput struct {
	Products []ProductCard
	Results  []SearchResultItem
	Sources  []SourceItem
}
```

节点职责：

1. `search_tool` 负责调用工具 backend，不直接拼回答 Prompt。
2. `search_tool` 可以输出 `products` SSE，但产品卡片结构必须来自 Tool 输出。
3. `answer_prompt` 只消费 `SearchToolOutput.Results` 和短期记忆。
4. `finish` 只消费 `SearchToolOutput.Sources`。
5. 主 Graph 不关心工具 backend 是进程内实现还是 MCP 服务。

MCP 化目标：

```text
internal/tool/search
  -> 当前阶段：InProcessSearchTool
  -> 后续阶段：MCPSearchTool
```

这样后续拆出独立 MCP 服务时，主 Graph 只替换 Tool 实现，不改变节点拓扑。

### 6. 工具编排规划

本项目建议采用当前主流的“确定性 Graph 编排 + 标准 Tool 抽象 + 可替换 Tool backend”模式，而不是把所有工具选择都交给模型自由决定。

原因：

1. 保险推荐、产品搜索、产品详情解析属于强业务链路，需要稳定、可验证、可回归。
2. 产品搜索必须在特定意图下执行，不能依赖模型是否生成 tool call。
3. 工具输出会影响前端 `products/detail_items/sources` 事件，需要保持结构化协议稳定。
4. 后续拆成 MCP 服务时，主 Graph 应只依赖 Tool contract，不依赖具体服务位置。

整体编排分三层：

```text
Chat Graph 编排层
  -> 决定什么时候调用哪些工具
  -> 管理分支、SSE 顺序、模型节点、收口事件

Tool Registry / Tool Adapter 层
  -> 统一工具名称、入参、出参、超时、重试、日志、错误降级
  -> 同时适配 in-process、Eino ToolsNode、MCP client

Tool Backend 层
  -> 当前阶段：当前进程内 productsearch/fallback/productdetail
  -> 后续阶段：独立 MCP service
```

#### 工具清单

| 工具名 | 当前实现 | 目标形态 | 是否让模型自由调用 | 说明 |
|---|---|---|---|---|
| `product_search` | `internal/service/productsearch` | in-process Tool，后续 MCP | 否，由 Graph 决定 | 产品推荐/查询/对比时必须执行 |
| `knowledge_search` | `internal/search/fallback` | in-process Tool，后续 MCP | 否，由 Graph 决定 | 给回答模型提供知识上下文和 sources |
| `search_tool` | 组合工具 | composite Tool | 否，由 Graph 决定 | 并行调用 `product_search` 和 `knowledge_search`，输出统一结果 |
| `product_detail` | `internal/skill/productdetail` | 子 Graph Tool，后续可 MCP | 否，由 action router 决定 | 产品详情页抓取、清洗、结构化提取和解读 |
| `compliance_check` | `internal/compliance` | Guardrail Tool/Node | 否，由 Graph 固定调用 | 输出前合规清洗，不暴露给模型自由选择 |
| `memory_context` | API 层 MySQL/Redis | 基础设施，不作为 LLM Tool | 否 | 短期记忆由 API 层注入 Prompt，不让模型直接读写 |

#### 推荐节点拓扑

普通推荐/查询/对比链路：

```text
intent_gate
  -> search_tool
    -> product_search       并行
    -> knowledge_search     并行
    -> search_result_merge
  -> answer_prompt
  -> answer_model
  -> answer_stream_emit
  -> compliance_check
  -> finish
```

产品详情链路：

```text
action_router
  -> product_detail_tool
    -> detail_cache_get
    -> detail_fetch_html
    -> detail_clean_html
    -> detail_extract_prompt
    -> detail_extract_model
    -> detail_extract_parse
    -> detail_validate
    -> detail_cache_set
    -> detail_answer_prompt
    -> detail_answer_model
  -> detail_stream_emit
```

缺槽追问链路：

```text
intent_gate
  -> followup_prompt
  -> followup_model
  -> compliance_check
  -> followup_emit
```

超范围链路：

```text
intent_gate
  -> out_of_scope
  -> compliance_check
  -> done
```

#### Tool Registry 设计

建议新增：

```text
internal/tool/
  registry.go
  contract.go
  middleware.go
  search/
    tool.go
    inprocess.go
    mcp.go
  productdetail/
    tool.go
    inprocess.go
    mcp.go
```

核心接口建议：

```go
type ToolBackend[I any, O any] interface {
	Name() string
	Invoke(ctx context.Context, input I) (O, error)
}

type ToolRuntimeConfig struct {
	Timeout    time.Duration
	MaxRetries int
	Backend    string // inprocess 或 mcp
}
```

每个工具至少要有：

1. 稳定工具名。
2. 明确 JSON 入参和出参。
3. 超时配置。
4. 错误码和降级结果。
5. 执行日志和耗时指标。
6. 单测和端到端验证样例。

#### Eino ToolsNode 使用策略

Eino 的标准工具链路是：

```text
ChatModelNode
  -> branch 判断是否有 ToolCalls
  -> ToolsNode
  -> ChatModelNode
```

这种模式适合开放式 Agent，让模型自主选择工具。但当前保险主链路更适合确定性编排：

```text
intent_gate
  -> search_tool
  -> answer_model
```

因此本项目分两种使用方式：

| 方式 | 使用场景 | 当前是否采用 |
|---|---|---|
| Deterministic Tool Node | 搜索、详情、合规这类强业务工具 | 采用 |
| Agentic ToolsNode | 未来复杂多步任务，由模型决定是否调用工具 | 预留 |

实现上可以先用 `AddLambdaNode(graphNodeSearchTool, ...)` 调用 Tool Registry，保证结构和边界正确。后续如果需要模型 tool-calling，再把同一批工具通过 Eino `tool.BaseTool` 适配给 `ToolsNode`：

```text
同一个 Tool Backend
  -> Deterministic Graph Tool Adapter
  -> Eino BaseTool Adapter
  -> MCP Client Adapter
```

#### 并行与降级策略

`search_tool` 内部建议并行执行：

```text
product_search
knowledge_search
```

合并规则：

1. `product_search` 成功：输出 `products`。
2. `knowledge_search` 成功：写入 `Results/Sources`。
3. `product_search` 失败但 `knowledge_search` 成功：不输出产品卡片，继续生成知识回答。
4. `knowledge_search` 失败但 `product_search` 成功：仍输出产品卡片，回答 prompt 中标记“无搜索结果”。
5. 两者都失败：走回答降级文案或错误事件。

推荐默认超时：

| 工具 | 超时 | 重试 | 缓存 |
|---|---:|---:|---|
| `product_search` | 3s | 1 | 可按 query 短 TTL 缓存 |
| `knowledge_search` | 1s | 0 | 内存或静态 fallback |
| `product_detail` fetch | 8s | 1 | URL 级缓存 |
| `product_detail` extract | 20s | 0 | URL 级缓存 |
| `compliance_check` | 200ms | 0 | 不需要 |

#### MCP 拆分路径

MVP 阶段：

```text
Chat Graph
  -> Tool Registry
    -> InProcessSearchTool
```

MCP 阶段：

```text
Chat Graph
  -> Tool Registry
    -> MCPSearchTool
      -> product-search MCP service
      -> fallback-search MCP service
```

MCP 服务建议独立负责：

1. 平台 adapter。
2. 平台认证、cookie、限流。
3. 搜索结果去重、过滤、排序。
4. fallback 知识检索。
5. 工具级缓存和熔断。

主后端继续负责：

1. 会话和短期记忆。
2. 意图识别。
3. 工具调用编排。
4. 回答生成。
5. SSE 协议和前端合同。

#### 可观测与治理

每次工具调用建议记录：

```text
request_id
chat_session_id
tool_name
backend
input_hash
duration_ms
status
error_code
result_count
cache_hit
```

不记录完整用户隐私内容，不记录 API Key、cookie 或完整 HTML。

工具输出进入模型前，需要经过：

```text
normalize
  -> truncate
  -> source attach
  -> prompt format
```

这样可以避免工具返回过长内容导致 prompt 膨胀，也方便后续做模型切换和评分对比。

### 7. 生产装配切换

涉及文件：

| 文件 | 改造点 |
|---|---|
| `internal/agent/chatflow/production.go` | 保留现有 `OrchestratorEinoGraph`，将其实现替换为 ChatModelNode Graph |
| `internal/config/config.go` | 不一定需要新增字段，复用 `ORCHESTRATOR` |
| `cmd/server/main.go` | 无需改动 |

建议装配逻辑：

```go
switch strings.ToLower(settings.Orchestrator) {
case "eino", "graph", "eino_graph":
	return NewGraphFlow(...) // 改造后 NewGraphFlow 内部注册 ChatModelNode
default:
	return NewProduction()
}
```

`eino_graph` 初始化失败时，建议直接返回错误或明确 fallback 到 `lite`。不能 fallback 到旧版 Lambda Graph，因为本次改造目标就是覆盖它。

## 分阶段计划

### 阶段 A：基础接入与编译闭环

目标：让 Eino ChatModel 和 Tool contract 在默认构建中可用，为 Graph 覆盖做准备。

任务：

1. 去除或调整 `internal/llm/eino.go` 的 build tag。
2. 确认 `github.com/cloudwego/eino-ext/components/model/openai` 已是直接依赖。
3. 为 `intent`、`answer`、`followup`、`detail` 创建 Eino ChatModel 工厂测试。
4. 增加 provider 缺 key、缺 base、缺 model 的错误测试。
5. 新增 `internal/tool` 基础包，定义 Tool 名称、输入输出、运行配置和错误结构。
6. 新增 `SearchToolInput/SearchToolOutput`，先不改变现有搜索实现。

验收：

```bash
go test ./internal/llm ./internal/config ./internal/tool/...
go test ./...
```

### 阶段 B：Intent / Followup 改造成 ChatModelNode

目标：先迁非流式模型调用，降低风险。

任务：

1. 新建 `ModelGraphFlow` 骨架。
2. 注册 `intent_prompt -> intent_model -> intent_parse`。
3. 注册 `followup_prompt -> followup_model -> followup_emit`。
4. 复用现有 `schema.ValidateIntent`、`ApplyFollowupRules`。
5. 保证短期记忆仍能进入 intent prompt。

验收：

1. `rg "AddChatModelNode" internal/agent/chatflow` 能看到 `intent_model`、`followup_model`。
2. 缺槽追问、超范围问题、普通推荐意图测试通过。
3. SSE 事件顺序与旧链路一致。

### 阶段 C：Answer 流式模型节点改造

目标：搜索改为 Tool 边界，回答生成必须由 `answer_model` ChatModelNode stream 输出。

任务：

1. 将当前 `graphNodeSearch` 改造成 `search_tool` 工具节点边界。
2. 实现 `internal/tool/search/InProcessSearchTool`，内部并行调用现有 productsearch/fallback。
3. 主 Graph 只消费 `SearchToolOutput.Products/Results/Sources`。
4. 注册 `search_tool -> answer_prompt -> answer_model -> answer_stream_emit`。
5. 将 Eino stream chunk 转换成 `EventDelta`。
6. 保留 `products` 先输出、`sources/disclaimer/done` 后输出的顺序。
7. 补齐 `<think>` 跨 chunk 过滤。
8. 保留生成失败 fallback 文案。

验收：

1. `/api/chat` 对“百万医疗险怎么选？”返回 `products` 和流式 `delta`。
2. `done` 事件中仍带 `chat_session_id`。
3. Redis/MySQL 仍能保存用户消息和助手消息。
4. 主 Graph 只依赖 `SearchToolInput/SearchToolOutput`，不直接依赖具体平台搜索实现。
5. `search_tool` 单测覆盖产品搜索失败、知识搜索失败、双失败三种降级。
6. 人格评分脚本结果不低于当前版本基线。

### 阶段 D：Product Detail 模型节点改造

目标：产品详情提取和详情回答也接入 Eino ChatModelNode。

任务：

1. 拆分 HTML 抓取、清洗、缓存逻辑，保留为 Lambda 节点。
2. 注册 `detail_extract_model` 处理保障责任结构化提取。
3. 注册 `detail_answer_model` 处理详情解读和追问回答。
4. 保留页面缓存与详情追问复用逻辑。

验收：

1. `action=product_detail` 能输出 `detail_items`、`delta`、`done`。
2. `action=product_followup` 能复用缓存详情。
3. 产品详情相关单测通过。

### 阶段 E：覆盖切换与回归验证

目标：`eino_graph` 被 ChatModelNode Graph 覆盖，`lite` 作为回滚路径，验证报告可追踪。

任务：

1. 保留 `lite`。
2. 覆盖现有 `eino_graph`，不新增独立编排模式。
3. 增加启动日志，明确当前编排模式和模型运行时。
4. 新增验证报告到 `docs/tasks/`。
5. 执行单测、race、curl 链路、评分脚本。

验收命令：

```bash
go test ./...
go test -race ./internal/agent/chatflow ./internal/service/intent ./internal/service/answer ./internal/skill/productdetail
ORCHESTRATOR=eino_graph HTTP_ADDR=0.0.0.0:34567 go run ./cmd/server
```

验证 curl：

```bash
curl -X POST http://127.0.0.1:34567/api/chat/session/current \
  -H 'Content-Type: application/json' \
  -d '{"anonymous_id":"anon_eval_001"}'

curl -N -X POST http://127.0.0.1:34567/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"anonymous_id":"anon_eval_001","chat_session_id":"chat_xxx","message":"百万医疗险怎么选？"}'
```

## 需要重点保留的现有行为

| 行为 | 说明 |
|---|---|
| SSE 协议 | 前端依赖 `status/products/delta/sources/disclaimer/done` |
| 产品卡片先输出 | 搜索结果应早于回答正文 |
| Search Tool 边界 | 主 Graph 只依赖工具输入输出协议，便于后续迁 MCP |
| Tool 可替换性 | 同一个工具 contract 支持 in-process 和 MCP backend |
| 短期记忆 | 最近 N 条消息仍由 Redis 优先、MySQL 回源 |
| 匿名用户会话规则 | 匿名用户只返回最新 session |
| Provider 阶段路由 | `intent/answer/followup/detail` 可配置不同模型 |
| `<think>` 过滤 | 兼容 DeepSeek 等模型输出 |
| 错误降级 | 回答失败时仍输出可读 fallback |

## 风险与处理

| 风险 | 影响 | 处理方案 |
|---|---|---|
| Eino OpenAI ChatModel 与 MiniMax stream 兼容性不一致 | 回答流式失败 | 先做最小 stream 验证；必要时实现 Eino ChatModel wrapper |
| Eino Graph 类型流转复杂 | 节点拆分成本增加 | 先迁非流式 intent/followup，再迁 answer stream |
| 重试和 `<think>` 过滤从旧 client 迁移遗漏 | 稳定性或输出质量下降 | 独立实现模型 wrapper 或后处理节点，并补单测 |
| Graph 中 event emission 与 stream 生命周期冲突 | SSE done 顺序异常 | 用端到端 curl 和单测固定事件顺序 |
| Search Tool 边界设计不清 | 后续 MCP 拆分仍需重构主 Graph | 本阶段先定义 `SearchToolInput/SearchToolOutput`，主 Graph 禁止依赖平台 adapter 细节 |
| 产品详情链路同时涉及抓取、缓存、LLM | 改造面大 | 放到阶段 D，普通 chat 稳定后再迁 |

## 验收标准

代码层面：

1. 项目中存在真实注册点：

```bash
rg "AddChatModelNode" internal/agent/chatflow
```

2. 项目中存在稳定工具边界：

```bash
rg "SearchToolInput|SearchToolOutput|ToolBackend" internal
```

3. `eino_graph` 模式下，`intent`、`answer`、`followup`、`detail` 不再直接调用自定义 `llm.Client`。
4. 主 Graph 不直接依赖具体平台搜索实现。
5. legacy `lite` 模式仍可运行。

功能层面：

1. `/api/chat/session/current` 正常返回 `chat_session_id`。
2. `/api/chat` 正常输出产品卡片、回答正文、来源、免责声明和 done。
3. 产品详情与详情追问正常。
4. Redis 清空后仍能从 MySQL 回源恢复短期记忆。

质量层面：

1. `go test ./...` 通过。
2. 核心包 race 测试通过。
3. 评分脚本平均分不低于当前版本。
4. 平均响应耗时不明显劣化；若劣化，需要记录原因和优化项。

## 建议输出物

实施完成后需要产出：

```text
docs/tasks/TASK-YYYYMMDD-eino-chatmodel-node-migration.md
docs/tasks/TASK-YYYYMMDD-eino-chatmodel-node-migration-report.md
```

报告中至少包含：

1. 改动文件清单。
2. 覆盖后的 Graph 节点拓扑。
3. Tool Registry 与 `search_tool` 输入输出协议。
4. `lite` 与 `eino_graph` 的切换方式。
5. 测试命令和结果。
6. curl 验证输出摘要。
7. 性能和评分对比。
8. 遗留问题。

## 待确认事项

请确认以下策略后再进入实现：

1. 第一轮是否先迁 `intent/followup/answer`，产品详情放第二轮；还是要求一次性把 `product_detail` 也迁完。
2. `eino_graph` 初始化失败时，是直接启动失败，还是明确 fallback 到 `lite`。
3. 当前默认模式是否继续保持 `lite`，等验证报告通过后再切到覆盖后的 `eino_graph`。
