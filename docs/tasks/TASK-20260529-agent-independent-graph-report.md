# SmartInsureAdvisorAgent 独立 AgentGraph 多 Agent 任务报告

任务编号：TASK-20260529-170454  
生成时间：2026-05-29 17:04:54 Asia/Shanghai  
完成时间：2026-05-29 17:13:38 Asia/Shanghai  
任务状态：已完成  
任务进度：100%

## Agent 列表

| Agent | 角色 | 负责模块 | 状态 |
|---|---|---|---|
| Team Leader | 总控与集成 | 任务拆分、AgentGraph 实现、API 接线、报告维护 | 已完成 |
| Explorer Agent Lagrange | 只读分析 | 复用边界、测试建议、风险检查点 | 已完成 |
| Verifier Agent Hume | 独立验证 | 代码审查、测试结果、验收风险 | 已完成 |

## 任务名称

08-Agent 新链路后端改动：实现独立 `SmartInsureAdvisorAgent -> AgentGraph`。

## 任务描述

上一阶段 `/api/agent/chat` 已接入 `AgentRuntime`，但 `SmartInsureAdvisorAgent` 内部仍包装 `chatflow.Runner`。本任务按最终文档要求，将新链路调整为：

```text
/api/agent/chat
  -> AgentRuntime
  -> SmartInsureAdvisorAgent
  -> 独立 AgentGraph
  -> SSE
```

旧 `/api/chat` 继续保留原有 `chatflow.Runner` 路径，作为稳定生产入口和回滚路径。

## 模块列表

| 模块 | 负责人 | 交付内容 |
|---|---|---|
| M1 复用边界分析 | Explorer Agent Lagrange | 确认可复用接口、不可继续调用的旧 workflow 边界 |
| M2 独立 AgentGraph | Team Leader | `internal/agent/smartinsureagent` 下新增 graph/state/nodes/tools，实现独立节点编排 |
| M3 API/生产接线 | Team Leader | `NewServer` 注册 SmartInsureAdvisorAgent 独立图，不再把 chatflow runner 注入 agent |
| M4 测试与验证 | Team Leader + Verifier Agent | 单元测试、API 测试、全量 `go test ./...`、独立验证 |

## 模块依赖关系

- M2 依赖现有 `chatflow` 的 SSE 事件类型和产品/来源结构，但不能调用 `chatflow.Runner.Run` 或 `chatflow.GraphFlow.Run`。
- M3 依赖 M2 的生产构造函数。
- M4 在 M2/M3 完成后执行。

## 任务执行要求

- `/api/chat` 不删除、不替换。
- `/api/agent/chat` 必须进入 `AgentRuntime -> SmartInsureAdvisorAgent -> AgentGraph`。
- `SmartInsureAdvisorAgent` 不再持有或调用 `chatflow.Runner`。
- 独立 AgentGraph 第一版复用现有 in-process 能力：意图识别、产品搜索、知识检索、产品详情、回答生成。
- SSE 事件兼容：`status/products/detail_items/delta/sources/disclaimer/done/error`。
- 继续保留 `agent_id`、`trace_id`、`requestId/request_id` 追加字段。
- 继续支持 `metadata`、`history`、`AGENT_TRACE_ENABLED`、`AGENT_MEMORY_WINDOW`。

## 任务执行步骤

- [x] 读取 `multi-agent-development` skill。
- [x] 创建本任务报告。
- [x] 启动 Explorer Agent。
- [x] 完成独立 AgentGraph 实现。
- [x] 完成 API/生产接线。
- [x] 完成测试与 smoke 验证。
- [x] 完成独立 Verifier 验证。

## 任务执行结果

已完成 M1 复用边界分析：

- Explorer Agent 确认 `AgentRuntime`、`/api/agent/chat` HTTP 壳、SSE writer、trace 字段追加可复用。
- Explorer Agent 明确禁止 `SmartInsureAdvisorAgent` 继续持有 `chatflow.Runner`、调用 `chatflow.NewProductionRunner()` 或 `runner.Run(...)`。
- Explorer Agent 明确前端 `/` 与 `/chat/agent` 应分别对应 workflow 与 agent 两条链路。

已完成 M2 独立 AgentGraph：

- `SmartInsureAdvisorAgent` 已改为持有 `*AgentGraph`，不再持有 `chatflow.Runner`。
- 新增 `internal/agent/smartinsureagent/graph.go`，实现 `session_validate`、`memory_load`、`intent_model`、`agent_route`、`product_search_tool`、`knowledge_search_tool`、`answer_model`、`product_detail_node`、`finish`。
- AgentGraph 不调用 `chatflow.Runner.Run`、`chatflow.GraphFlow.Run` 或 `chatflow.Flow.Run`；仅复用底层能力接口和 SSE 事件 DTO。
- AgentGraph 不持有旧 workflow runner；构造时从 production/fake flow 提取底层能力接口，便于生产复用和测试注入。

已完成 M3 API/生产接线：

- `/api/chat` 保留 `s.flow.Run(...)` workflow 路径。
- `/api/agent/chat` 保留 `AgentRuntime.Run(...)` 路径，注册 `smartinsureagent.New(agentGraph)`。
- `NewServer(flow)` 中传入的旧 workflow runner 不再注入 Agent；仅当测试传入 `*chatflow.Flow` 时提取 fake detail/answer 等底层能力用于 AgentGraph 测试。

已完成部分验证：

```bash
GOCACHE=/tmp/smartinsure-go-build-cache go test ./internal/agent/smartinsureagent ./internal/api
GOCACHE=/tmp/smartinsure-go-build-cache go test ./internal/agent/... ./internal/api ./internal/config
GOCACHE=/tmp/smartinsure-go-build-cache go test ./...
npm test -- --runInBand src/lib/__tests__/api.test.ts
```

旧链路调用检查：

```bash
rg -n "chatflow\\.(Runner|NewProductionRunner|NewGraphFlow)|GraphFlow|toChatflowRequest|runner\\.Run" internal/agent/smartinsureagent internal/api -S
```

结果：`internal/agent/smartinsureagent` 无旧 runner/GraphFlow 命中；`internal/api` 仅保留 `/api/chat` workflow 所需的 `chatflow.Runner` 和 `chatflow.NewProductionRunner()`。

Smoke 验证：

- 临时启动 `HTTP_ADDR=127.0.0.1:34571`。
- `POST /api/chat` 输出 workflow SSE，不包含 `agent_id`。
- `POST /api/agent/chat` 输出 Agent SSE，包含 `agent_id=smartinsure-advisor`、`trace_id`、`requestId/request_id`。
- Smoke 结束后已关闭 34571，未影响 34567 正式服务。

## Verifier 结论

验证时间：2026-05-29 17:13 Asia/Shanghai

结论：建议验收。

风险：

- 高风险：无。
- 中风险：无。
- 低风险：`AgentGraph` 仍复用 `chatflow` 的接口/类型和 `NewGraphFromFlow` 适配层，但未调用旧 `Runner.Run` 或 `GraphFlow.Run`；若后续要求彻底领域隔离，可再拆 agent-native 类型。
- 低风险：`/api/agent/chat` 受 `AGENT_CHAT_ENABLED` 控制，默认开启；部署环境若显式设为 false 会返回 501。

Verifier 测试结果：

- `GOCACHE=/tmp/smartinsure-go-build-cache go test ./...` 通过。
- 旧链路调用检查仅命中 `internal/api/router.go` 中 legacy `/api/chat` 需要的 `chatflow.Runner` 类型和 `NewProductionRunner` 初始化；未命中 `/api/agent/chat` 调用旧 `runner.Run`、`GraphFlow.Run` 或 `toChatflowRequest`。

Verifier 验收判断：

- `/` -> `legacy_chat` -> `/api/chat` -> workflow 成立。
- `/chat/agent` -> `agent_chat` -> `/api/agent/chat` -> `AgentRuntime` -> `SmartInsureAdvisorAgent` -> 独立 `AgentGraph` 成立。

## 异常情况

- 无阻断异常。

## 建议及解决方法

- 后续若要完全去除 `chatflow` 类型依赖，可新增 agent-native DTO，并把产品/来源/事件类型从 `chatflow` 迁到独立共享包或 `schema`。
- 若要把 memory 完全节点化，可将当前 `api.conversationService` 抽到内部共享包，由 `AgentGraph` 注入 `MemoryService` 承接 session 校验、history load、assistant save。
