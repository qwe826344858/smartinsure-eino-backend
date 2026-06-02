# TASK-20260525-eino-chatmodel-node-migration-report

生成时间：2026-05-25 17:27:30 Asia/Shanghai

任务状态：已完成

## 任务摘要

依据 `docs/07-Eino-ChatModelNode接入改造计划.md` 完成阶段 A-C：

1. Eino ChatModel 工厂默认构建可用。
2. Search Tool contract 与进程内 backend 落地。
3. `ORCHESTRATOR=eino_graph` 主聊天链路覆盖为 ChatModelNode Graph。
4. 保留 `lite` 回滚路径。

本阶段未迁移 `product_detail` 模型节点，按计划留到阶段 D。

## Agent 执行结果

| Agent | 模块 | 结果 |
|---|---|---|
| Developer Agent A | LLM 工厂 | 完成，`go test ./internal/llm` 通过 |
| Developer Agent B | Tool 边界 | 完成，`go test ./internal/tool/...` 通过 |
| Developer Agent C | Graph 编排 | 完成初稿，Team Leader 集成修正后测试通过 |
| Team Leader | 集成与验证 | 完成统一 Tool contract、生产装配、全量验证 |

## 改动文件清单

| 文件 | 说明 |
|---|---|
| `internal/llm/eino.go` | 移除 build tag，默认构建支持 Eino ChatModel factory |
| `internal/llm/eino_test.go` | 增加 stage factory 与字段缺失测试 |
| `internal/tool/contract.go` | 新增通用 Tool contract、运行配置、错误结构 |
| `internal/tool/search/tool.go` | 新增 `SearchToolInput`、`SearchToolOutput` 与搜索接口 |
| `internal/tool/search/inprocess.go` | 新增进程内 `search_tool` backend |
| `internal/tool/search/inprocess_test.go` | 覆盖产品搜索失败、知识搜索失败、双失败降级 |
| `internal/agent/chatflow/graph.go` | `eino_graph` 覆盖为 ChatModelNode Graph |
| `internal/agent/chatflow/model_graph_models.go` | 新增 Graph fallback ChatModel adapter |
| `internal/agent/chatflow/model_graph_nodes.go` | 新增 prompt/parse/search/emit 节点 |
| `internal/agent/chatflow/search_tool.go` | 接入统一 `internal/tool/search` contract |
| `internal/agent/chatflow/production.go` | 生产装配优先注入真实 Eino ChatModel |
| `internal/agent/chatflow/graph_test.go` | 增加 ChatModelNode 主链路测试 |
| `docs/tasks/TASK-20260525-eino-chatmodel-node-migration.md` | 任务进度文档 |

## 覆盖后的 Graph 拓扑

普通主链路：

```text
START
  -> action_router
    -> product_detail
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

真实 ChatModelNode 注册点：

```go
graph.AddChatModelNode(graphNodeIntentModel, ...)
graph.AddChatModelNode(graphNodeFollowupModel, ...)
graph.AddChatModelNode(graphNodeAnswerModel, ...)
```

## Tool Registry 与 Search Tool 协议

新增稳定边界：

```text
internal/tool
internal/tool/search
```

核心类型：

```go
ToolBackend[I, O]
ToolRuntimeConfig
SearchToolInput
SearchToolOutput
InProcessSearchTool
```

当前 `search_tool` backend：

```text
search_tool
  -> product_search
  -> knowledge_search
  -> merge Products/Results/Sources
```

后续 MCP 化时只替换 backend，不改主 Graph 拓扑。

## 切换方式

现有模式：

```bash
ORCHESTRATOR=lite
ORCHESTRATOR=eino_graph
```

`eino_graph` 优先使用真实 Eino ChatModel factory。若 provider 配置不可用，会明确记录日志并使用 Graph fallback adapter；不会回到旧版 Lambda Graph。

## 验证结果

已通过：

```bash
go test ./internal/agent/chatflow ./internal/tool/... ./internal/llm
go test ./...
go test -race ./internal/agent/chatflow ./internal/llm ./internal/tool/...
go test -tags eino ./...
go build -o /tmp/smartinsure-eino-backend-server-current ./cmd/server
```

临时服务 smoke：

```bash
HTTP_ADDR=127.0.0.1:34570 ORCHESTRATOR=eino_graph /tmp/smartinsure-eino-backend-server-current
```

验证请求：

```bash
curl -N -X POST http://127.0.0.1:34570/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"message":"百万医疗险怎么选？"}'
```

返回顺序：

```text
status(analyzing)
status(searching)
products
status(answering)
delta
sources
disclaimer
done
```

## 异常与处理

| 问题 | 处理 |
|---|---|
| 目标目录不是 Git 仓库，无法用 `git status` 做快照 | 通过改动文件清单和测试命令记录 |
| Developer Agent C 初稿定义了本地 SearchTool contract | Team Leader 已统一到 `internal/tool/search` |
| 当前临时 smoke 未注入 LLM key 时使用 Graph fallback adapter | 生产环境配置 provider key 后会优先注入真实 Eino ChatModel |

## 遗留项

1. `product_detail` 仍未迁到 ChatModelNode，下一阶段处理。
2. `search_tool` 目前为 in-process backend，MCP backend 后续单独实现。
3. 当前 34567 运行中的服务未在本报告中重启；要启用新链路需重启并设置 `ORCHESTRATOR=eino_graph`。
