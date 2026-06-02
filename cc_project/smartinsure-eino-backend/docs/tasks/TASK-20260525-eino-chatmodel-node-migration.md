# TASK-20260525-eino-chatmodel-node-migration

创建时间：2026-05-25 16:59:36 Asia/Shanghai

任务状态：已完成

任务进度：100%

## 任务名称

Eino ChatModelNode 接入与 Search Tool 边界改造

## 任务描述

依据 `docs/07-Eino-ChatModelNode接入改造计划.md` 执行下一个阶段开发。本阶段先覆盖阶段 A-C：

1. 让 Eino ChatModel 工厂在默认构建中可用。
2. 新增 Tool contract 与 `search_tool` 输入输出边界。
3. 将 `ORCHESTRATOR=eino_graph` 的普通聊天主链路升级为 ChatModelNode Graph。
4. `intent/followup/answer` 必须出现真实 `AddChatModelNode` 注册点。
5. `search_tool` 先使用进程内 backend 调用现有 productsearch/fallback，后续可替换 MCP。

本阶段暂不迁移 `product_detail` 模型节点，作为阶段 D 遗留项单独执行。

## Agent 列表

| Agent | 角色 | 负责模块 | 状态 |
|---|---|---|---|
| Team Leader | 主协调与集成 | 任务拆分、文档更新、集成验证 | 进行中 |
| Developer Agent A | LLM 工厂 | Eino ChatModel 默认构建、stage factory、测试 | 已完成 |
| Developer Agent B | Tool 边界 | `internal/tool`、`search_tool`、进程内 backend、测试 | 已完成 |
| Developer Agent C | Graph 编排 | `eino_graph` 覆盖为 ChatModelNode 主链路 | 已完成，待集成修正 |
| Verifier Agent | 独立验证 | 代码审查、测试、链路验证、报告 | 已完成 |

## 模块列表

| 模块 | 范围 | 输出物 | 状态 |
|---|---|---|---|
| A. Eino ChatModel 工厂 | `internal/llm` | 默认构建可用的 Eino ChatModel factory | 已完成 |
| B. Search Tool contract | `internal/tool` | `ToolBackend`、`SearchToolInput`、`SearchToolOutput`、in-process backend | 已完成 |
| C. ChatModelNode Graph | `internal/agent/chatflow` | `intent/followup/answer` ChatModelNode 注册与流式输出 | 已集成 |
| D. 集成测试 | 相关测试包 | 单测、race、curl 验证 | 已完成 |

## 模块依赖关系

```text
A. Eino ChatModel 工厂
  -> C. ChatModelNode Graph

B. Search Tool contract
  -> C. ChatModelNode Graph

C. ChatModelNode Graph
  -> D. 集成测试
```

## 执行流程

1. Team Leader 创建任务文档并确认基线测试。
2. Developer Agent A/B/C 并行开发各自模块。
3. Team Leader 集成并解决交叉依赖。
4. Verifier Agent 独立验证。
5. Team Leader 生成收尾报告。

## 执行要求

1. 不新增 `ORCHESTRATOR=eino_chatmodel_graph`。
2. 直接覆盖现有 `ORCHESTRATOR=eino_graph`。
3. 保留 `ORCHESTRATOR=lite` 可用。
4. `search_tool` 必须有稳定输入输出结构。
5. 主 Graph 不直接依赖具体平台 adapter。
6. 不提交真实 API Key。
7. 每个阶段更新本文档任务状态。

## 当前进度

| 时间 | 进度 | 说明 |
|---|---:|---|
| 2026-05-25 16:59:36 | 5% | 已创建任务文档，完成初始范围拆分 |
| 2026-05-25 17:01:20 | 15% | 已启动 Developer Agent A/B/C 并行开发 |
| 2026-05-25 17:03:30 | 20% | 基线测试通过，Developer Agent A/B/C 仍在开发中 |
| 2026-05-25 17:06:50 | 35% | Developer Agent A 完成 LLM 工厂改造，`go test ./internal/llm` 通过 |
| 2026-05-25 17:11:30 | 50% | Developer Agent B 完成 Tool 边界改造，`go test ./internal/tool/...` 通过 |
| 2026-05-25 17:17:10 | 65% | Developer Agent C 完成 Graph 初稿，开始集成真实 Eino factory 与统一 Tool contract |
| 2026-05-25 17:22:30 | 75% | 已完成集成修正，核心包测试通过，开始全量验证 |
| 2026-05-25 17:27:30 | 100% | 全量测试、race、`-tags eino` 和临时 `eino_graph` SSE smoke 均通过 |

## 验证计划

```bash
go test ./internal/llm ./internal/tool/... ./internal/agent/chatflow ./internal/service/intent ./internal/service/answer
go test ./...
go test -race ./internal/agent/chatflow ./internal/llm ./internal/tool/...
rg "AddChatModelNode" internal/agent/chatflow
rg "SearchToolInput|SearchToolOutput|ToolBackend" internal
```

## 执行结果

已完成阶段 A-C：

1. `internal/llm/eino.go` 已进入默认构建，支持按 stage 创建 Eino `ToolCallingChatModel`。
2. 新增 `internal/tool` 与 `internal/tool/search`，定义 `ToolBackend`、`SearchToolInput`、`SearchToolOutput` 和 `InProcessSearchTool`。
3. `ORCHESTRATOR=eino_graph` 已覆盖为 ChatModelNode Graph，存在真实 `AddChatModelNode` 注册点：
   - `intent_model`
   - `followup_model`
   - `answer_model`
4. `search_tool` 已接入统一 Tool contract，主 Graph 消费 Tool 输出，不直接依赖平台 adapter。
5. 生产装配优先注入真实 Eino ChatModel；配置缺失时明确使用 Graph fallback adapter，保留 `lite` 回滚路径。

验证通过：

```bash
go test ./internal/agent/chatflow ./internal/tool/... ./internal/llm
go test ./...
go test -race ./internal/agent/chatflow ./internal/llm ./internal/tool/...
go test -tags eino ./...
ORCHESTRATOR=eino_graph HTTP_ADDR=127.0.0.1:34570 /tmp/smartinsure-eino-backend-server-current
```

## 异常情况

1. 目标目录不是 Git 仓库，无法用 `git status` 做变更快照。
2. 本阶段未迁移 `product_detail` 的 ChatModelNode，仍保留原 DetailRunner，按 07 文档阶段 D 后续执行。

## 建议及解决方法

1. 下一阶段继续迁移 `product_detail` 的 `detail_extract_model` 与 `detail_answer_model`。
2. 若要让当前 34567 运行新链路，需要用新二进制重启服务，并设置 `ORCHESTRATOR=eino_graph`。
