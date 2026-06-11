# TASK-20260610-product-detail-rag

任务编号：TASK-20260610-142008
任务状态：已完成
任务进度：100%
任务名称：产品详情解析数据 RAG 第一阶段落地
基准文档：`docs/11-产品详情解析数据RAG分步骤实现方案.md`
创建时间：2026-06-10 14:20:08 Asia/Shanghai

## Agent 列表

| Agent | 角色 | 模块 | 状态 |
|---|---|---|---|
| Team Leader | 总控与集成 | 任务拆分、核心实现、测试、报告 | 已完成 |
| Developer Agent A / Anscombe | 只读分析 | MySQL source snapshot 与批量读取 | 已完成 |
| Developer Agent B / Maxwell | 只读分析 | ProductDetail formatter 与 tag 规则 | 已完成 |
| Developer Agent C / Hooke | 只读分析 | pgvector search 与 RAG search 接入 | 已完成 |
| Verifier Agent / Leibniz | 独立验证 | 代码审查、测试、验收风险 | 已完成 |

## 任务描述

基于 `docs/11` 实现产品详情解析数据 RAG 第一阶段能力。在已有 Ark embedding 基础能力之上，把 `product_details` 中已经持久化的结构化商品详情转换为 RAG chunks，写入 pgvector，并补齐后续检索接入所需的底层接口。

## 模块拆分

| 模块 | 交付内容 | 主要文件 | 依赖 |
|---|---|---|---|
| M1 Ark embedding 前置能力 | 已完成，作为本任务前置条件 | `internal/rag/embedding`, `configs/embedding*.yaml` | 无 |
| M2 Source Snapshot 与 MySQL Reader | `product_detail_sources` schema、source 结构、active 列表读取 | `internal/skill/productdetail` | M1 无依赖 |
| M3 ProductDetail Formatter | summary/duty/tag/source_excerpt chunks 和 metadata | `internal/rag/productdetail` | M2 source 结构 |
| M4 ProductDetail Ingestor 与命令 | 统一入库服务、`cmd/ingestproductdetails` | `internal/rag/productdetail`, `cmd/ingestproductdetails` | M2、M3、M1 |
| M5 pgvector Search | `SearchSimilarChunks` 接口和 SQL 构建 | `internal/rag/store` | M1 |
| M6 RAG Search Service | query embedding + pgvector search -> `schema.SearchResultItem` | `internal/rag/search` | M5、M1 |
| M7 Chatflow 接入 | `FallbackSearcher` 优先 RAG、失败降级 fallback | `internal/agent/chatflow` | M6 |
| M8 验证与文档 | 单元测试、集成测试、任务报告 | `docs/tasks` | 全部模块 |

## 执行要求

1. 不改变 SSE 事件名和 payload 合约。
2. MySQL `product_details` 继续作为完整 `detail_json` 事实源。
3. pgvector 只做语义索引，可重建。
4. Ark embedding 配置必须从 `configs/embedding.local.yaml` 或环境变量读取，不把密钥写入可提交文件。
5. 默认配置下现有 URL RAG `cmd/ingesturls` 行为不变。
6. RAG search 不可用时必须降级现有 fallback search。
7. 第一轮实现优先保证离线批量同步和检索底层能力，线上异步入库可在底层稳定后接入。

## 当前执行步骤

- [x] 读取 `multi-agent-development` skill。
- [x] 创建多 Agent 任务文档。
- [x] 启动 A/B/C 三个只读分析 Agent。
- [x] 实现 M2-M5 底层能力。
- [x] 实现 M6-M7 接入能力。
- [x] 启动 Verifier Agent。
- [x] 运行测试与 smoke。
- [x] 收敛 Verifier 结果。
- [x] 生成任务报告。

## 本轮实现结果

### M2 Source Snapshot 与 MySQL Reader

- `product_details` upsert 成功后同事务写入 `product_detail_sources`。
- 普通网页保存 `raw_payload=HTML`、`cleaned_text`、`content_hash`。
- 平安平台接口保存 `raw_payload=JSON`，并标记 `source_type=platform_api`、`source_format=json`。
- 新增 `ListActive` keyset 分页读取 active、未过期、match_rate 达标的数据，并按 `source_hash/content_hash` 关联 source snapshot。

### M3 Formatter

- 新增 `internal/rag/productdetail`。
- 支持四类 chunk：
  - `product_detail_summary`
  - `product_duty`
  - `product_tag`
  - `product_source_excerpt`
- tag 采用固定规则词表，不调用 LLM。
- metadata 包含 `product_key`、`product_name`、`platform`、`canonical_url`、`chunk_type`、`tag_name`、`tag_category`、`source_hash`、`content_hash` 等检索/追溯字段。

### M4 Ingestor 与命令

- 新增 `ProductDetail` RAG ingestor，统一执行 `Format -> EmbedTexts -> UpsertDocumentWithChunks`。
- 新增 `cmd/ingestproductdetails`，支持离线批量回填 `product_details` 到 pgvector。
- 命令参数包括 `--namespace`、`--platform`、`--prompt-version`、`--min-match-rate`、`--require-source`、`--source-chunk-size`、`--source-chunk-overlap`、`--max-tag-chunks`。

### M5-M6 检索能力

- `internal/rag/store` 新增只读 `SearchStore`，不破坏原有写入 `Store` 接口。
- `PgVectorStore.SearchSimilarChunks` 支持 namespace、query vector、TopK 和 JSONB metadata filter。
- 新增 `internal/rag/search`，封装 query embedding + pgvector search + `schema.SearchResultItem` 映射。

### M7 Chatflow 接入

- 新增配置：
  - `RAG_SEARCH_ENABLED`
  - `RAG_SEARCH_NAMESPACE`
  - `RAG_SEARCH_TOP_K`
  - `RAG_SEARCH_MIN_SCORE`
  - `RAG_SEARCH_TIMEOUT`
  - `PRODUCT_DETAIL_RAG_ENABLED`
  - `PRODUCT_DETAIL_RAG_NAMESPACE`
  - `PRODUCT_DETAIL_RAG_MIN_MATCH_RATE`
  - `PRODUCT_DETAIL_RAG_ASYNC_TIMEOUT`
  - `PRODUCT_DETAIL_RAG_ASYNC_WORKERS`
  - `PRODUCT_DETAIL_RAG_QUEUE_SIZE`
- `chatflow.Fallback` 在 RAG search 启用且初始化成功时优先使用 pgvector，失败或空结果降级原 fallback。
- `productdetail.Service` 在 MySQL upsert 成功后可异步触发 RAG 入库，不阻塞 SSE。
- 线上异步入库使用有界队列和固定 worker，避免每次解析成功直接创建 embedding/pgvector goroutine。
- 同一 RAG 配置下复用 pgvector DB pool、embedding client、RAG search service 和产品详情 RAG ingestor。

## Verifier 结果收敛

| 发现 | 处理结果 |
|---|---|
| P1：线上异步 RAG 入库没有 worker/队列限流 | 已新增 `AsyncProductDetailRAGIngestor`，支持 `workers/queue_size/timeout`，队列满时拒绝入队并记录日志 |
| P2：产品详情 RAG 配置项未完整落地 | 已新增 `PRODUCT_DETAIL_RAG_MIN_MATCH_RATE`、`PRODUCT_DETAIL_RAG_ASYNC_TIMEOUT`、`PRODUCT_DETAIL_RAG_ASYNC_WORKERS`、`PRODUCT_DETAIL_RAG_QUEUE_SIZE` |
| P2：RAG pgvector 连接/embedding client 生命周期未复用 | 已新增生产 RAG deps 缓存，同配置复用 DB pool、embedding、search 和 async ingestor |
| P3：非 duty tag 的 `related_duties` 为空 | 已为产品级 tag 补默认相关责任，增强 tag chunk 可解释性 |
| P3：source snapshot `source_url` 可能带跟踪参数 | 已让普通网页 source URL 使用规范化 URL，并补 `_rag_check/session/session_id/anonymous_id` 过滤 |

## 验证命令

```bash
go test ./internal/skill/productdetail ./internal/rag/productdetail ./cmd/ingestproductdetails
go test ./internal/rag/store ./internal/rag/search
go test ./internal/config ./internal/agent/chatflow
go test ./...
go test -tags eino ./...
go test -count=1 ./...
```

全部通过。

## 异常情况

暂无。

## 备注

本任务前置 Ark embedding 已通过真实 smoke：

```text
ARK_EMBEDDING_SMOKE=1 EMBEDDING_CONFIG_PATH=configs/embedding.local.yaml go test -run TestArkEmbeddingSmoke -count=1 -v ./internal/rag/embedding
```
