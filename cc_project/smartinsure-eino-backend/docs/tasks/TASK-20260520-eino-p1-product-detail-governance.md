# TASK-20260520-eino-p1-product-detail-governance

生成时间：2026-05-20 18:04:54 CST

## 任务状态

- 状态：完成
- 进度：100%
- 阶段：P1 - 产品详情 Skill 与治理能力迁移
- 基准文档：`docs/04-Go-Eino后端迁移模块清单.md`

## 任务描述

在已完成 P0 Go Eino 后端主链路的基础上，继续迁移 Python 后端 P1 能力：产品详情 Skill、HTML 清洗、详情缓存、基础治理能力、schema 兜底校验与回放接口，并把 `product_detail` / `product_followup` action 接入 `/api/chat` SSE 主流程。

## 迁移来源文件

| 模块 | Python 源文件 | Go 目标路径 |
|---|---|---|
| 产品详情 Skill | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/product_detail_skill.py` | `internal/skill/productdetail/` |
| 产品详情缓存 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/product_detail_cache.py` | `internal/skill/productdetail/cache.go` |
| HTML 清洗 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/html_cleaner.py` | `internal/htmlcleaner/` |
| 合规替换 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/compliance.py` | `internal/compliance/` |
| schema 兜底 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/schema_validator.py` | `internal/schema/validator.go` |
| 回放接口 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/replay.py` | `internal/eval/replay/` |

## Agent 列表

| Agent | 角色 | 负责模块 | 写入范围 | 状态 |
|---|---|---|---|---|
| Team Leader | 任务拆分与集成 | 任务文档、集成、最终收尾 | `docs/tasks/`, `internal/agent/chatflow/`, 必要测试 | 已完成 |
| Developer A / Beauvoir | 开发 | HTML 清洗、产品详情 Skill、TTL 缓存 | `internal/htmlcleaner/`, `internal/skill/productdetail/` | 已完成 |
| Developer B / Socrates | 开发 | 合规、schema validator、回放接口 | `internal/compliance/`, `internal/schema/validator.go`, `internal/eval/replay/` | 已完成 |
| Verifier / Nash | 独立验证 | 代码审查、测试、集成验证 | 只读，必要时提交问题清单 | 已完成 |

## 模块依赖关系

1. `internal/htmlcleaner` 是 `internal/skill/productdetail` 的前置依赖。
2. `internal/skill/productdetail` 依赖 `internal/llm`、`internal/prompt`、`internal/schema`。
3. `internal/agent/chatflow` 依赖产品详情 Skill 的运行接口，用于 action 路由。
4. `internal/compliance` 与 `internal/schema/validator.go` 可独立实现，后续可被各 LLM 节点复用。
5. `internal/eval/replay` 先提供本地 JSONL 记录/读取接口，完整 prompt 回放可留给 P2 增强。

## 执行步骤

1. 创建 P1 任务文档与 Agent 分工。
2. 并行开发 HTML 清洗/产品详情 Skill 与治理模块。
3. Team Leader 集成 `/api/chat` 的 `product_detail` / `product_followup` action。
4. 补充单元测试与 SSE action 测试。
5. 执行 `go test ./...`、`go test ./... -race`、`go test -tags eino ./...`、`go test -tags eino ./... -race`。
6. 启动独立 Verifier Agent 做只读验证。
7. 生成最终任务报告。

## 验收标准

1. `product_detail` action 不再返回 `NOT_IMPLEMENTED`。
2. 产品详情页可执行：抓取、清洗、LLM/兜底提取、校验、缓存、`detail_items` SSE 输出、流式解读。
3. `product_followup` 可复用详情缓存回答追问；无缓存时给出明确提示。
4. 合规模块具备敏感表达检测与替换能力，并有单元测试覆盖。
5. schema validator 对 intent/query 输出具备兜底校验，并有单元测试覆盖。
6. replay 包具备本地样例记录、列表、读取能力，并有单元测试覆盖。
7. 所有 Go 测试与 race/eino tag 验证通过。

## 执行进度

| 时间 | 进度 | 说明 |
|---|---:|---|
| 2026-05-20 18:04:54 CST | 5% | 已创建任务文档，准备启动 Developer Agent |
| 2026-05-20 18:06:00 CST | 15% | 已启动 Beauvoir 与 Socrates 两个 Developer Agent 并行开发 |
| 2026-05-20 18:10:00 CST | 25% | Team Leader 已完成 `chatflow.DetailRunner` action 接入抽象，等待产品详情实现注入 |
| 2026-05-20 18:14:00 CST | 45% | Socrates 完成合规、schema validator、replay 接口迁移，负责范围测试通过 |
| 2026-05-20 18:22:00 CST | 70% | Beauvoir 完成 HTML 清洗与产品详情 Skill；Team Leader 已完成 production 注入、合规替换与 action 集成测试 |
| 2026-05-20 18:25:00 CST | 85% | 默认、race、`-tags eino`、`-tags eino -race` 四组测试通过，准备启动独立验证 |
| 2026-05-20 18:26:52 CST | 100% | Nash 独立验证通过，未发现阻塞问题；已完成 P1 收尾 |

## 验证结果

| 命令 | 结果 |
|---|---|
| `go test ./...` | 通过 |
| `go test ./... -race` | 通过 |
| `go test -tags eino ./...` | 通过 |
| `go test -tags eino ./... -race` | 通过 |

## 任务执行结果

1. 已迁移 `internal/htmlcleaner`，覆盖噪声标签删除、内嵌 JSON 中文文本提取、中文字符统计与截断。
2. 已迁移 `internal/skill/productdetail`，覆盖产品详情抓取、清洗、LLM/启发式提取、校验、TTL 缓存、`detail_items` 输出、解读与追问降级。
3. 已在 production chatflow 中接入 `product_detail` / `product_followup` action，生产 `/api/chat` 不再返回 P0 的 `NOT_IMPLEMENTED`。
4. 已迁移 `internal/compliance` 并接入生产回答、追问、详情 delta 的基础合规替换。
5. 已迁移 `internal/schema/validator.go` 并让 intent 服务复用 schema 兜底校验。
6. 已迁移 `internal/eval/replay` 的 P1 本地 JSONL 记录、列表、读取能力。
7. 已修复 requestId middleware 未透传 `http.Flusher` 导致 SSE 在包装后失败的问题，并补充 API action SSE 测试。

## 异常情况

无阻塞异常。

低风险项：

1. Go 版 HTML 清洗使用标准库正则方案，畸形 HTML 与 Python BeautifulSoup 行为可能有差异。
2. 产品详情尚未对真实平台页面和真实 LLM key 做端到端联调。
3. 产品详情缓存当前为进程内 TTL，多副本一致性后续仍需 Redis 或共享缓存。
4. replay 本阶段只实现 JSONL 记录、列表、读取，完整 LLM 回放执行留给 P2。

## 建议及解决方法

1. Redis 缓存本阶段只预留接口与内存 TTL 实现，多副本一致性留给后续部署增强。
2. LLM 未配置时产品详情 Skill 必须降级为可测试的启发式提取/提示，避免本地测试依赖外部服务。
