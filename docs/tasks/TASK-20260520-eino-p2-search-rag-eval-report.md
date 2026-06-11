# TASK-20260520-eino-p2-search-rag-eval 报告

生成时间：2026-05-20 20:23:29 CST

## 任务结论

- 状态：完成
- 进度：100%
- 阶段：P2 - 搜索、RAG 与增强能力迁移
- 基准文档：`docs/04-Go-Eino后端迁移模块清单.md`
- 任务文档：`docs/tasks/TASK-20260520-eino-p2-search-rag-eval.md`

## Agent 执行结果

| Agent | 模块 | 结果 |
|---|---|---|
| Team Leader | 任务拆分、`cmd/ingesturls`、集成、报告 | 完成 |
| Developer A / Dirac | 通用搜索、MCP 搜索、旧商品搜索兼容 | 完成 |
| Developer B / Chandrasekhar | RAG 入库链路 | 完成 |
| Developer C / Noether | replay/eval 执行增强 | 完成 |
| Verifier / Archimedes | 独立只读验证 | 通过，未发现阻塞问题 |

## 已完成模块

| 模块 | Go 路径 | 结果 |
|---|---|---|
| 搜索词生成 | `internal/search/query/` | LLM 生成、schema 兜底、启发式 fallback、年份注入 |
| 通用搜索 | `internal/search/web/` | MiniMax、外部 API、fallback、并发、去重、低质量过滤 |
| MCP 搜索 | `internal/search/mcp/` | SSE session、JSON-RPC initialize、initialized、`tools/call`、结果解析 |
| 旧商品搜索 | `internal/search/productlegacy/` | 旧 MiniMax 商品搜索兼容，不替换 P0 主产品搜索 |
| 平台解析 | `internal/search/parsers/` | 价格、公司、标签与平台详情解析 |
| RAG 切块 | `internal/rag/chunker/` | 段落优先切块、长段落定长切分、overlap |
| embedding | `internal/rag/embedding/` | OpenAI-compatible `/embeddings` batch client |
| RAG 入库编排 | `internal/rag/ingest/` | fetch -> clean -> chunk -> embedding -> store |
| pgvector store | `internal/rag/store/` | Store 接口、SQL 构建、vector literal、pgvector upsert |
| 入库命令 | `cmd/ingesturls/` | 支持 `--url`、`--input-file`、`--namespace`、`--source-type`、`--metadata-json` |
| replay/eval | `internal/eval/replay/` | Runner、Diff、Suite、Summary |

## 关键变更路径

- `cmd/ingesturls/main.go`
- `cmd/ingesturls/main_test.go`
- `internal/search/query/service.go`
- `internal/search/web/service.go`
- `internal/search/web/backends.go`
- `internal/search/mcp/client.go`
- `internal/search/mcp/parse.go`
- `internal/search/productlegacy/service.go`
- `internal/search/productlegacy/platforms.go`
- `internal/search/parsers/parsers.go`
- `internal/rag/chunker/chunker.go`
- `internal/rag/embedding/client.go`
- `internal/rag/ingest/fetcher.go`
- `internal/rag/ingest/service.go`
- `internal/rag/store/store.go`
- `internal/rag/store/pgvector.go`
- `internal/eval/replay/runner.go`
- `go.mod`
- `go.sum`

## 验证结果

| 命令 | 结果 |
|---|---|
| `go test ./...` | 通过 |
| `go test ./... -race` | 通过 |
| `go test -tags eino ./...` | 通过 |
| `go test -tags eino ./... -race` | 通过 |

P2 范围补充验证：

| 命令 | 结果 |
|---|---|
| `go test -count=1 ./cmd/ingesturls ./internal/search/... ./internal/rag/... ./internal/eval/replay` | 通过 |

## 异常情况

无阻塞异常。

开发过程中发现并修复 1 个低风险问题：

- `internal/rag/ingest` 的 `withDefaults` 未为 `ChunkOverlap` 套用默认配置。已修复并补充 `TestWithDefaultsAppliesChunkOverlap` 回归测试。

## 残余风险

1. 真实 MiniMax/MCP/embedding/pgvector 连接未做集成 smoke test，当前测试使用 fake transport、`httptest`、fake fetcher/embedder/store 与 SQL 构建校验。
2. `internal/search/web` 以库级可注入 backend 为主，生产链路未默认切换到新 web search factory；P0 主链路仍使用已完成的平台直连搜索与 fallback。
3. 旧商品搜索依赖平台 URL 白名单，平台改版后需要更新 pattern。
4. pgvector store 的真实数据库事务与扩展安装仍需在部署环境验证。

## 后续建议

1. 上线前做一次真实外部服务 smoke test：MiniMax Search、Open-WebSearch MCP、embedding、PostgreSQL + pgvector。
2. 如果需要把通用 web search 接入 `/api/chat`，再单独开一个小任务配置 `search` backend factory，避免影响已稳定的 P0 主链路。
3. 为 RAG 入库准备小批量真实 URL golden fixtures，并记录 replay/eval 用例用于 Prompt 回归。
