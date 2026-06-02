# SmartInsureAdvisorAgent Plan-Act 多 Agent 任务报告

任务编号：TASK-20260529-174611  
生成时间：2026-05-29 17:46:11 Asia/Shanghai  
任务状态：已完成  
任务进度：100%

## Agent 列表

| Agent | 角色 | 负责模块 | 状态 |
|---|---|---|---|
| Team Leader | 总控与集成 | 任务拆分、Plan-Act 实现、测试、报告维护 | 已完成 |
| Explorer Agent Mill | 只读分析 | 复用边界、最小闭环、测试建议 | 已完成 |
| Verifier Agent Locke | 独立验证 | 代码审查、测试结果、验收风险 | 已完成 |

## 任务名称

按 `11-Agent Plan-Act架构调整文档.md` 实现受控 Plan-Act SmartInsureAdvisorAgent。

## 任务描述

在保留 `/api/chat` workflow 稳定链路的前提下，将 `/api/agent/chat` 下的 `SmartInsureAdvisorAgent -> AgentGraph` 从固定节点顺序升级为受控 Plan-Act 循环：

```text
/chat/agent
  -> /api/agent/chat
  -> AgentRuntime
  -> SmartInsureAdvisorAgent
  -> Plan-Act AgentGraph
  -> action -> tool -> observation -> reasoner -> final_answer
```

## 模块列表

| 模块 | 负责人 | 交付内容 |
|---|---|---|
| M1 复用边界分析 | Explorer Agent Mill | 最小闭环方案、测试建议、workflow 隔离检查点 |
| M2 AgentState/Action | Team Leader | `state.go`、`actions.go`，action 白名单与 schema 校验 |
| M3 Reasoner/Tools | Team Leader | `planner.go`、`tools.go`，受控 reasoner 与 tool observation |
| M4 Plan-Act Graph | Team Leader | 改造 `graph.go` 为循环式 Plan-Act |
| M5 测试与验证 | Team Leader + Verifier Agent | 单元测试、API 回归、独立验证 |

## 模块依赖关系

- M4 依赖 M2/M3。
- M5 在 M2/M3/M4 后执行。
- `/api/chat` workflow 与本次 Agent 改造必须隔离。

## 任务执行要求

- `/api/chat` 不改造、不替换。
- `/api/agent/chat` 使用 Plan-Act AgentGraph。
- action 仅允许：`product_search`、`knowledge_search`、`product_detail`、`ask_followup`、`final_answer`。
- 每轮 action 必须写入 observation。
- 支持最大 iteration 限制，避免无限循环。
- thought 只进入后端 scratchpad，不发给前端 SSE。
- SSE 继续兼容 `status/products/detail_items/delta/sources/disclaimer/done/error`。

## 任务执行步骤

- [x] 读取 `multi-agent-development` skill。
- [x] 读取 `11-Agent Plan-Act架构调整文档.md`。
- [x] 创建本任务报告。
- [x] 启动 Explorer Agent。
- [x] 实现 AgentState 与 action schema。
- [x] 实现 reasoner 与 tools。
- [x] 改造 AgentGraph 为 Plan-Act 循环。
- [x] 补充测试并运行全量验证。
- [x] 启动 Verifier Agent 独立验证。

## 任务执行结果

已完成开发实现：

- `internal/agent/smartinsureagent/state.go`：新增 `AgentState/AgentStep/AgentObservation`，承载 plan、steps、observations、iteration。
- `internal/agent/smartinsureagent/actions.go`：实现 action 白名单与 schema 校验，限制为 `product_search/knowledge_search/product_detail/ask_followup/final_answer`。
- `internal/agent/smartinsureagent/planner.go`：实现生产可选 LLM JSON planner、JSON repair、fallback heuristic reasoner 与 `deterministic_graph` 回滚模式。
- `internal/agent/smartinsureagent/tools.go`：封装 product/knowledge/detail 工具 observation。
- `internal/agent/smartinsureagent/graph.go`：改造为 Plan-Act 主循环，支持 `reasoning -> tool_running -> observing -> replan/final_answer`、最大 iteration、连续无效 action fallback、tool timeout、thought 不对前端输出。
- `internal/api/router.go`：保持 `/api/chat` 走 legacy workflow，生产 `/api/agent/chat` 使用 `NewProductionGraph`；测试/注入 flow 仍使用确定性 graph。
- `configs/llm_providers.yaml`：新增 `agent_planner` stage 路由。
- `internal/config/config.go`：新增 `AGENT_MODE/AGENT_MAX_ITERATIONS/AGENT_TOOL_TIMEOUT/AGENT_ACTION_REPAIR_ENABLED/AGENT_SCRATCHPAD_MAX_CHARS/AGENT_OBSERVATION_MAX_CHARS`。
- 34567 后端进程已切换到新构建。

验证命令：

```bash
GOCACHE=/tmp/smartinsure-go-build-cache go test -count=1 ./internal/agent/smartinsureagent ./internal/api ./internal/config
GOCACHE=/tmp/smartinsure-go-build-cache go test -count=1 ./...
```

验证结果：全部通过。

Verifier Agent 首轮发现并已修复：

- `product_detail` 未受 `AGENT_TOOL_TIMEOUT` 控制：已为详情工具统一加 `context.WithTimeout`。
- `product_detail` 缺少有效 observation：已收集 `detail_items/delta` 统计写入 observation；按钮直达继续结束，planner 选择的 `product_detail` 会进入 `observing` 并回到下一轮 reasoner。
- `final_answer.answer_brief` 未参与最终回答：已将 `answer_brief/products/steps observation` 注入 `AnswerInput.Message`，且不注入 thought。
- `action_input` schema 偏宽：已拒绝额外字段、非字符串、显式 `null`；`final_answer.answer_text` 仅允许内部 `Trusted` reasoner，模型 JSON planner 不能直接使用。
- 测试补充：`TestAgentGraphProductDetailUsesToolTimeoutAndRecordsObservation`、`TestAgentGraphPlannerProductDetailReturnsObservationBeforeFinalAnswer`、`TestAgentGraphMaxIterationsFallbacks`、`TestChatRouteDoesNotEmitAgentTraceFields`。

Verifier Agent 复核：

- 首轮：发现 `product_detail` timeout/observation 与 `answer_brief` 注入问题。
- 二轮：确认 `product_detail` timeout/observation、planner detail replan、`answer_brief/products/steps observation` 注入已生效。
- 三轮：确认 `answer_text` 仅内部可信 reasoner 可用；指出显式 `null` 边角后已修复。

接口冒烟：

- `http://127.0.0.1:34567/api/chat`：保持 legacy workflow，SSE 不包含 `agent_id/trace_id`。
- `http://127.0.0.1:34567/api/agent/chat`：输出 `reasoning/tool_running/observing/answering`，包含 `agent_id/trace_id`，最终 `sources/disclaimer/done` 正常，未发现 thought 泄露。

二次 Agent 能力增强：

- 重复工具调用防护：同一工具和同一输入再次出现时不再执行，写入 observation 并要求 reasoner 重新规划。
- 工具失败换路：heuristic reasoner 使用 `hasAttemptedAction`，产品搜索失败后不再重试同一 action，会转入知识检索或最终回答。
- 泛化推荐追问：对“给我推荐保险”这类缺少险种、年龄、预算和偏好的请求，先 `ask_followup`，避免无上下文推荐。
- Planner 提示增强：明确要求模型不要重复调用已有工具输入，工具失败后应换工具或 final answer。
- 测试补充：重复 action observation、工具失败后换路、泛化推荐追问。
- 34567 已再次切换到增强后的新构建。

## 异常情况

暂无阻塞异常。后端项目目录当前不是 Git 仓库，无法用 `git status` 输出变更清单。

## 建议及解决方法

后续若要继续增强 Agent 能力，可在保持 action 白名单的前提下扩展 planner prompt 的评估字段与最终回答结构化合规过滤；当前本任务范围已完成。
