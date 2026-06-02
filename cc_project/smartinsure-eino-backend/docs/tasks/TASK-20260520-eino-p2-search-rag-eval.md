# TASK-20260520-eino-p2-search-rag-eval

生成时间：2026-05-20 19:58:54 CST

## 任务状态

- 状态：完成
- 进度：100%
- 阶段：P2 - 搜索、RAG 与增强能力迁移
- 基准文档：`docs/04-Go-Eino后端迁移模块清单.md`

## 任务描述

在 P0 主链路与 P1 产品详情/治理能力完成后，继续迁移 Python 后端 P2 能力：通用搜索与 MCP 搜索、旧 MiniMax 商品搜索兼容、RAG 入库实验链路、`ingest_urls` 命令行工具，以及 replay/eval 的执行与结果对比增强。

## 迁移来源文件

| 模块 | Python 源文件 | Go 目标路径 |
|---|---|---|
| 搜索词生成 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/query_service.py` | `internal/search/query/` |
| 通用搜索编排 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/search_service.py` | `internal/search/web/` |
| MCP 搜索 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/mcp_search_client.py` | `internal/search/mcp/` |
| 旧商品搜索 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/product_search.py` | `internal/search/productlegacy/` |
| 平台解析工具 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/platform_parsers.py` | `internal/search/parsers/` |
| RAG 入库编排 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/ingestion_service.py` | `internal/rag/ingest/` |
| 通用网页抓取 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/webpage_fetcher.py` | `internal/rag/ingest/fetcher.go` |
| 文本切块 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/text_chunker.py` | `internal/rag/chunker/` |
| embedding 客户端 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/embedding_client.py` | `internal/rag/embedding/` |
| pgvector store | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/pgvector_store.py` | `internal/rag/store/pgvector/` |
| 入库脚本 | `/home/zhaoting/Agent/Agent_project/apps/backend/scripts/ingest_urls.py` | `cmd/ingesturls/` |
| replay 增强 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/replay.py` | `internal/eval/replay/` |

## Agent 列表

| Agent | 角色 | 负责模块 | 写入范围 | 状态 |
|---|---|---|---|---|
| Team Leader | 任务拆分与集成 | 任务文档、配置、命令行集成、最终收尾 | `docs/tasks/`, `docs/04-Go-Eino后端迁移模块清单.md`, `cmd/ingesturls/`, 必要测试 | 已完成 |
| Developer A / Dirac | 开发 | 通用搜索、MCP 搜索、旧商品搜索兼容 | `internal/search/query/`, `internal/search/web/`, `internal/search/mcp/`, `internal/search/productlegacy/`, `internal/search/parsers/` | 已完成 |
| Developer B / Chandrasekhar | 开发 | RAG 入库链路 | `internal/rag/chunker/`, `internal/rag/embedding/`, `internal/rag/ingest/`, `internal/rag/store/` | 已完成 |
| Developer C / Noether | 开发 | replay/eval 执行增强 | `internal/eval/replay/` | 已完成 |
| Verifier / Archimedes | 独立验证 | 代码审查、测试、集成验证 | 只读，必要时提交问题清单 | 已完成 |

## 模块依赖关系

1. `internal/search/query` 依赖 `internal/llm`、`internal/prompt`、`internal/schema`。
2. `internal/search/web` 可复用 `internal/search/fallback`，并可配置 MiniMax、外部搜索 API、MCP 后端。
3. `internal/search/productlegacy` 依赖 `internal/search/mcp` 与 `internal/search/parsers`，作为 P0 平台直连搜索的兼容兜底，不替换主链路默认平台搜索。
4. `internal/rag/ingest` 依赖 `internal/htmlcleaner`、`internal/rag/chunker`、`internal/rag/embedding`、`internal/rag/store`。
5. `cmd/ingesturls` 依赖 `internal/rag/ingest`，用于手动 URL 入库。
6. `internal/eval/replay` 在 P1 JSONL 记录/读取基础上增加 Runner、Diff、Summary，不影响线上 `/api/chat`。

## 执行步骤

1. 创建 P2 任务文档与 Agent 分工。
2. 并行开发搜索模块、RAG 入库模块、replay/eval 增强模块。
3. Team Leader 集成 `cmd/ingesturls` 与必要配置。
4. 补充单元测试与命令行参数测试。
5. 执行 `go test ./...`、`go test ./... -race`、`go test -tags eino ./...`、`go test -tags eino ./... -race`。
6. 启动独立 Verifier Agent 做只读验证。
7. 生成最终任务报告并更新迁移清单进度。

## 验收标准

1. 通用搜索模块具备 MiniMax Search、外部 API、fallback 去重过滤能力，所有外部调用可用 fake transport 测试。
2. MCP 搜索客户端具备 SSE session、JSON-RPC initialize、tools/call、结果解析能力，并有单元测试覆盖。
3. 旧商品搜索兼容模块可从搜索结果筛选商品页、抽取产品卡、解析价格和平台详情，不影响 P0 主搜索链路。
4. RAG 入库链路可执行 fetch -> clean -> chunk -> embedding -> store 的可测试编排。
5. pgvector store 提供 SQL 生成/接口抽象，单元测试不依赖真实数据库。
6. `cmd/ingesturls` 支持 `--url`、`--input-file`、`--namespace`、`--source-type`、`--metadata-json`。
7. replay/eval 支持基于记录样例执行 runner、结果 diff、summary。
8. 所有 Go 测试与 race/eino tag 验证通过。

## 执行进度

| 时间 | 进度 | 说明 |
|---|---:|---|
| 2026-05-20 19:58:54 CST | 5% | 已创建 P2 任务文档，准备启动 Developer Agent |
| 2026-05-20 20:00:00 CST | 15% | 已启动 Dirac、Chandrasekhar、Noether 三个 Developer Agent 并行开发 |
| 2026-05-20 20:03:00 CST | 30% | Noether 完成 replay/eval Runner、Diff、Suite、Summary 增强，负责范围测试通过 |
| 2026-05-20 20:12:00 CST | 50% | Dirac 完成通用搜索、MCP 搜索、旧商品搜索兼容迁移，`go test ./internal/search/...` 通过 |
| 2026-05-20 20:17:00 CST | 75% | Chandrasekhar 完成 RAG 入库链路；Team Leader 完成 `cmd/ingesturls` 与 pgx driver 集成，P2 范围测试通过 |
| 2026-05-20 20:20:00 CST | 90% | 默认、race、`-tags eino`、`-tags eino -race` 四组全量测试通过，准备启动独立验证 |
| 2026-05-20 20:23:29 CST | 100% | Archimedes 独立验证通过；低风险 `ChunkOverlap` 默认值问题已修复并通过回归测试 |

## 验证结果

| 命令 | 结果 |
|---|---|
| `go test ./...` | 通过 |
| `go test ./... -race` | 通过 |
| `go test -tags eino ./...` | 通过 |
| `go test -tags eino ./... -race` | 通过 |

## 任务执行结果

1. 已迁移 `internal/search/query`，支持 LLM 搜索词生成、schema 兜底、启发式 fallback 与年份注入。
2. 已迁移 `internal/search/web`，支持 MiniMax、外部 API、fallback、并发搜索、URL 去重与低质量过滤。
3. 已迁移 `internal/search/mcp`，覆盖 Open-WebSearch MCP SSE session、JSON-RPC 初始化与 `tools/call` 结果解析。
4. 已迁移 `internal/search/productlegacy` 与 `internal/search/parsers`，作为旧商品搜索兼容能力，不替换 P0 主链路。
5. 已迁移 RAG 入库链路：`internal/rag/chunker`、`internal/rag/embedding`、`internal/rag/ingest`、`internal/rag/store`。
6. 已新增 `cmd/ingesturls`，支持手动 URL 入库参数并串联 pgx、embedding、pgvector store 与 ingest service。
7. 已增强 `internal/eval/replay`，保留 JSONL 记录/读取并增加 Runner、Diff、Suite、Summary。
8. 已新增 `github.com/jackc/pgx/v5` 作为 `database/sql` PostgreSQL driver。

## 异常情况

无阻塞异常。

开发过程中发现并修复 1 个低风险问题：

1. `internal/rag/ingest` 的 `withDefaults` 未为 `ChunkOverlap` 套用默认配置。已修复并补充 `TestWithDefaultsAppliesChunkOverlap` 回归测试。

## 建议及解决方法

1. P2 仍以本地可测、接口兼容为主，不把真实外部搜索、真实 pgvector、真实 embedding 服务作为单元测试前置条件。
2. RAG 入库先保持 pgvector 方案，同时用 store 接口隔离，便于后续切换 Redis Vector 或其他向量库。
3. 旧商品搜索只作为兼容包迁移，不替换 P0 已完成的平台直连产品搜索。
4. 后续上线前仍需做真实 MiniMax/MCP/embedding/pgvector smoke test。
