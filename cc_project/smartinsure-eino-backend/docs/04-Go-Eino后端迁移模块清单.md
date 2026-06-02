# Go Eino 后端迁移模块清单

最后更新：2026-05-20

## 结论

当前 Python 后端不是简单的 API 层，而是一条“对话编排 + 平台商品搜索 + LLM 回答 + 产品详情 Skill + 实验性 RAG 入库”的组合链路。迁移到 Go Eino 时，建议拆成 12 个迁移域，其中首批必须迁移 8 个域，二期再处理 RAG 入库、旧搜索链路和回放工具。

代码统计口径：

| 范围 | 数量 | 说明 |
|---|---:|---|
| `apps/backend/app` Python 文件 | 42 | 当前后端业务、API、配置、服务、schema |
| `apps/backend/scripts` Python 文件 | 1 | RAG 入库脚本 |
| `apps/backend/tests/test_*.py` | 15 | 单元测试与链路测试 |
| 已接入平台 API | 3 | 小雨伞、平安、慧择 |
| 当前主编排实现 | 1 | `lite` 函数式编排；`langgraph` 仅占位 |

## 当前 Python 主流程

主入口是 `POST /api/chat`，由 FastAPI 输出 SSE：

1. `app/api/chat.py` 解析 `ChatRequest`，生成 `requestId`，把编排器事件转成 SSE。
2. `app/services/orchestrator_adapter.py` 根据 `ORCHESTRATOR` 选择编排器，目前默认走 `LiteOrchestrator`。
3. `app/services/chat_orchestrator.py` 执行主流程：
   - `action=product_detail` 或 `product_followup` 时直接进入 `ProductDetailSkill`。
   - 普通问题先并行启动意图识别和平台搜索。
   - `out_of_scope` 直接返回保险范围提示。
   - `needs_followup` 调用 LLM 生成追问。
   - 产品类意图推送 `products` 事件。
   - 使用 fallback 保险知识作为回答上下文。
   - LLM 流式生成 `delta`。
   - 最后输出 `sources`、`disclaimer`、`done`。

## 迁移模块清单

| 序号 | 迁移域 | 当前 Python 模块 | 当前职责 | Go/Eino 目标模块建议 | 优先级 |
|---:|---|---|---|---|---|
| 1 | HTTP 服务与路由 | `app/main.py`, `app/api/*.py` | FastAPI、CORS、健康检查、建议问题、Provider 状态、SSE Chat | `cmd/server`, `internal/api`, `internal/stream`；可参考 `adk/intro/http-sse-service` 的 SSE 事件映射 | P0 |
| 2 | 请求与 SSE 合约 | `app/schemas/chat.py` | `ChatRequest`、SSE payload、产品卡片、产品详情、意图/搜索/回答模型 | `internal/schema`；用 Go struct + JSON tag 固化前端兼容合约 | P0 |
| 3 | 对话编排核心 | `services/chat_orchestrator.py`, `services/orchestrator_adapter.py` | action 路由、意图判断、并行产品搜索、回答生成、事件产出 | `internal/agent/chatflow`；用 Eino Graph/Workflow 表达分支与并行节点 | P0 |
| 4 | LLM Provider 与模型调用 | `core/llm_providers.py`, `services/llm_client.py`, `config/llm_providers.yaml` | 多模型注册、阶段路由、OpenAI 兼容调用、流式调用、超时重试、过滤 `<think>` | `internal/llm`；用 Eino ChatModel 封装 MiniMax/OpenAI/DeepSeek 等 provider，保留阶段路由 | P0 |
| 5 | Prompt 模板 | `services/prompts.py` | 意图识别、回答生成、追问、产品详情提取/解读 Prompt | `internal/prompt`；常量化并加版本号，后续可迁为 YAML/模板文件 | P0 |
| 6 | 意图识别与追问 | `services/intent_service.py`, `services/answer_service.py` | LLM JSON 意图分类、追问规则修正、追问话术生成 | `internal/service/intent`, `internal/service/followup`；Eino LLM node + JSON schema 校验 | P0 |
| 7 | 平台直连产品搜索 | `services/platform_apis/*.py` | 关键词抽取、多平台并发、去重、品类过滤、年龄过滤、预算过滤、家庭预算分摊 | `internal/platform` + `internal/service/productsearch`；每个平台一个 adapter，公共过滤规则独立包 | P0 |
| 8 | 回答生成 | `services/answer_service.py`, `services/search_service.py` 的 fallback 部分 | 搜索上下文格式化、LLM 流式回答、fallback 知识源、来源输出 | `internal/service/answer`, `internal/search/fallback`；回答节点直接产出流式 `delta` | P0 |
| 9 | 产品详情 Skill | `services/product_detail_skill.py`, `services/product_detail_cache.py`, `services/html_cleaner.py` | 抓取页面、清洗、LLM 提取保障项、校验、缓存、通俗解读、追问复用缓存 | `internal/skill/productdetail`；建议做成 Eino Tool 或子 Graph，再由主 Graph 的 action router 调用 | P1 |
| 10 | 通用搜索与 MCP 搜索 | `services/search_service.py`, `services/mcp_search_client.py`, `services/product_search.py` | MiniMax 搜索、外部搜索 API、Open-WebSearch MCP、旧商品搜索链路 | `internal/search`；首批只迁 fallback，MiniMax/MCP/旧商品搜索二期兼容 | P2 |
| 11 | RAG 入库实验链路 | `services/ingestion_service.py`, `webpage_fetcher.py`, `text_chunker.py`, `embedding_client.py`, `pgvector_store.py`, `scripts/ingest_urls.py` | 网页抓取、清洗、切块、embedding、pgvector 入库；当前未接入 `/api/chat` | `internal/rag/ingest`, `internal/rag/store`；可参考 `quickstart/eino_assistant` 的 Redis RAG，或继续保留 pgvector | P2 |
| 12 | 治理、错误与验证工具 | `core/errors.py`, `core/logging.py`, `services/compliance.py`, `services/schema_validator.py`, `services/replay.py`, `tests/*` | 统一错误、请求 ID、合规词替换、schema 兜底、失败样例回放、测试 | `internal/middleware`, `internal/compliance`, `internal/eval`, `tests`；首批迁错误和 requestId，合规/回放二期增强 | P1 |

## Python 源文件路径明细

以下 43 个 Python 源文件是后续 Go Eino 迁移时需要逐一检查的实现来源。路径使用绝对路径，方便直接打开对照。

### P0 主链路必迁文件

| 迁移域 | Python 文件路径 | Go 目标建议 | 备注 |
|---|---|---|---|
| HTTP 服务与路由 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/main.py` | `cmd/server`, `internal/api/router.go` | FastAPI 应用入口、CORS、异常处理、路由注册 |
| HTTP 服务与路由 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/api/chat.py` | `internal/api/chat.go`, `internal/stream/sse.go` | `/api/chat` SSE 主入口 |
| HTTP 服务与路由 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/api/healthz.py` | `internal/api/healthz.go` | 健康检查 |
| HTTP 服务与路由 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/api/suggestions.py` | `internal/api/suggestions.go` | 推荐问题硬编码列表 |
| HTTP 服务与路由 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/api/providers.py` | `internal/api/providers.go` | Provider 状态查询 |
| 请求与 SSE 合约 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/schemas/chat.py` | `internal/schema/chat.go` | 请求、SSE、产品卡、产品详情、意图、搜索、回答 DTO |
| 对话编排核心 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/chat_orchestrator.py` | `internal/agent/chatflow/graph.go` | 当前主链路核心，需要映射为 Eino Graph/Workflow |
| 对话编排核心 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/orchestrator_adapter.py` | `internal/agent/router.go` | `lite/langgraph` 适配层；Go 侧可简化为 Eino 编排入口 |
| LLM Provider 与调用 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/core/config.py` | `internal/config/config.go` | 环境变量配置 |
| LLM Provider 与调用 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/core/llm_providers.py` | `internal/llm/registry.go` | Provider 注册、阶段路由 |
| LLM Provider 与调用 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/llm_client.py` | `internal/llm/client.go` | OpenAI 兼容调用、流式、重试、`<think>` 过滤 |
| Prompt 模板 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/prompts.py` | `internal/prompt/prompts.go` | 所有 Prompt 模板与版本 |
| 意图识别与追问 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/intent_service.py` | `internal/service/intent/service.go` | LLM JSON 意图识别、追问规则 |
| 意图识别与追问 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/answer_service.py` | `internal/service/answer/service.go`, `internal/service/followup/service.go` | 回答生成、追问生成、搜索上下文格式化 |
| 平台产品搜索 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/platform_apis/__init__.py` | `internal/service/productsearch/service.go` | 多平台并发、去重、预算/年龄/品类过滤 |
| 平台产品搜索 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/platform_apis/base.py` | `internal/platform/base.go`, `internal/platform/keyword.go` | 平台接口、关键词抽取、人群/预算推断 |
| 平台产品搜索 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/platform_apis/xiaoyusan.py` | `internal/platform/xiaoyusan/client.go` | 小雨伞搜索 adapter |
| 平台产品搜索 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/platform_apis/pingan.py` | `internal/platform/pingan/client.go` | 平安搜索 adapter |
| 平台产品搜索 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/platform_apis/huize.py` | `internal/platform/huize/client.go` | 慧择搜索 adapter |
| 回答上下文 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/search_service.py` | `internal/search/fallback/service.go` | 首批至少迁 fallback 知识库和来源去重 |

### P1 产品详情与治理文件

| 迁移域 | Python 文件路径 | Go 目标建议 | 备注 |
|---|---|---|---|
| 产品详情 Skill | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/product_detail_skill.py` | `internal/skill/productdetail/flow.go` | 抓取、提取、校验、解读主链路 |
| 产品详情 Skill | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/product_detail_cache.py` | `internal/skill/productdetail/cache.go` | URL 级 TTL 缓存；Go 侧建议预留 Redis 实现 |
| 产品详情 Skill | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/html_cleaner.py` | `internal/htmlcleaner/cleaner.go` | HTML 清洗、内嵌 JSON 提取、中文字符截断 |
| 治理与错误 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/core/errors.py` | `internal/errors/errors.go` | 统一错误码与 HTTP 响应 |
| 治理与错误 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/core/logging.py` | `internal/middleware/request_id.go` | requestId 中间件 |
| 治理与错误 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/compliance.py` | `internal/compliance/validator.go` | 合规敏感词检测与替换 |
| 治理与错误 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/schema_validator.py` | `internal/schema/validator.go` | LLM JSON 输出兜底校验 |
| 治理与评测 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/replay.py` | `internal/eval/replay.go` | 失败样例记录与回放，P1 可先设计接口、P2 完整迁移 |

### P2 搜索、RAG 与兼容文件

| 迁移域 | Python 文件路径 | Go 目标建议 | 备注 |
|---|---|---|---|
| 旧搜索链路 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/query_service.py` | `internal/search/query.go` | 搜索词生成服务；当前主链路已跳过，作为兼容迁移 |
| 旧搜索链路 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/product_search.py` | `internal/search/product_legacy.go` | MiniMax 商品页搜索旧链路，非当前主路径 |
| 旧搜索链路 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/mcp_search_client.py` | `internal/search/mcp/client.go` | Open-WebSearch MCP 客户端 |
| 旧搜索链路 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/platform_parsers.py` | `internal/search/parsers.go` | 平台搜索结果解析工具，需确认是否仍被引用 |
| RAG 入库 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/ingestion_service.py` | `internal/rag/ingest/service.go` | 网页入库编排 |
| RAG 入库 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/webpage_fetcher.py` | `internal/rag/ingest/fetcher.go` | 通用网页抓取 |
| RAG 入库 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/text_chunker.py` | `internal/rag/chunker/chunker.go` | 中文文本切块 |
| RAG 入库 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/embedding_client.py` | `internal/rag/embedding/client.go` | OpenAI 兼容 embedding |
| RAG 入库 | `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/pgvector_store.py` | `internal/rag/store/pgvector.go` | PostgreSQL + pgvector 入库 |
| RAG 入库 | `/home/zhaoting/Agent/Agent_project/apps/backend/scripts/ingest_urls.py` | `cmd/ingesturls/main.go` | 手动 URL 入库脚本 |

### 包初始化文件

这些文件本身业务逻辑很少或为空，但迁移时要确认 Go 包边界与导出关系是否覆盖它们的组织作用。

| Python 文件路径 | Go 处理建议 |
|---|---|
| `/home/zhaoting/Agent/Agent_project/apps/backend/app/__init__.py` | 无需等价逻辑，确认 Go module/package 根即可 |
| `/home/zhaoting/Agent/Agent_project/apps/backend/app/api/__init__.py` | 无需等价逻辑，路由包由 `internal/api` 承担 |
| `/home/zhaoting/Agent/Agent_project/apps/backend/app/core/__init__.py` | 无需等价逻辑，核心包由 `internal/config`、`internal/errors`、`internal/llm` 承担 |
| `/home/zhaoting/Agent/Agent_project/apps/backend/app/schemas/__init__.py` | 无需等价逻辑，DTO 由 `internal/schema` 承担 |
| `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/__init__.py` | 无需等价逻辑，服务包按领域拆分 |
| `/home/zhaoting/Agent/Agent_project/apps/backend/app/services/platform_apis/__init__.py` | 有业务逻辑，已在 P0 平台产品搜索中列出，需要迁移 |

### 非 Python 但必须同步迁移的配置

| 文件路径 | Go 目标建议 | 备注 |
|---|---|---|
| `/home/zhaoting/Agent/Agent_project/apps/backend/app/config/llm_providers.yaml` | `configs/llm_providers.yaml` 或 `internal/config/llm_providers.yaml` | Provider 注册、默认模型、阶段路由 |

### 验证参考文件

这些测试和调试脚本不属于线上业务模块，但建议作为 Go 迁移验收用例来源。

| 类型 | Python 文件路径 |
|---|---|
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_chat.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_healthz.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_suggestions.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_llm_services.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_keyword_extraction.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_family_budget.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_company_extraction.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_html_cleaner.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_product_detail_cache.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_product_detail_skill.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_text_chunker.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_pgvector_store.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_mcp_search.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_replay.py` |
| 单元/链路测试 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/test_schemas_v2.py` |
| 调试/性能参考 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/benchmark_endpoints.py` |
| 调试/性能参考 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/perf_baseline.py` |
| 调试/性能参考 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/debug_baidu.py` |
| 调试/性能参考 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/debug_mcp_engines.py` |
| 调试/性能参考 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/debug_platform_api.py` |
| 调试/性能参考 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/debug_price.py` |
| 调试/性能参考 | `/home/zhaoting/Agent/Agent_project/apps/backend/tests/debug_xys_cookie.py` |

## Go Eino 目标结构建议

建议新建 Go 后端目录，例如 `apps/backend-go`：

```text
apps/backend-go/
  cmd/server/                 # HTTP/SSE 服务入口
  internal/api/               # /api 路由层
  internal/stream/            # SSE event writer 与事件类型
  internal/schema/            # ChatRequest、ProductCard、ProductDetail 等 DTO
  internal/config/            # env + provider yaml 读取
  internal/llm/               # Eino ChatModel provider 注册与阶段路由
  internal/prompt/            # Prompt 模板与版本
  internal/agent/chatflow/    # 主 Eino Graph/Workflow
  internal/service/intent/    # 意图识别
  internal/service/answer/    # 回答生成
  internal/service/followup/  # 追问生成
  internal/service/productsearch/
  internal/platform/
    xiaoyusan/
    pingan/
    huize/
  internal/skill/productdetail/
  internal/search/fallback/
  internal/rag/               # 二期：入库、检索、向量库
  internal/compliance/        # 二期：合规审查
  tests/
```

## Eino 编排映射

首批主链路可以设计为一个 `ChatFlow` Graph：

| Python 阶段 | Eino 节点/结构 | 输出事件 |
|---|---|---|
| action 路由 | `ActionRouter` 条件分支 | `status`, `detail_items`, `delta` |
| 意图识别 | `IntentNode`，LLM JSON 输出 | 内部状态 |
| 平台搜索 | `ProductSearchNode`，与意图识别并行 | `products` |
| 超范围/追问判断 | `IntentGate` 条件分支 | `delta`, `done` |
| fallback 搜索上下文 | `FallbackSearchNode` | 内部状态、`sources` |
| 回答生成 | `AnswerNode`，ChatModel stream | `status`, `delta` |
| 结尾 | `FinishNode` | `sources`, `disclaimer`, `done` |

产品详情建议做成独立子图或 Tool：

```text
ProductDetailFlow:
  CacheGet
    -> FetchPage
    -> CleanHTML
    -> ExtractDutiesByLLM
    -> ValidateExtraction
    -> CacheSet
    -> ExplainByLLMStream
```

如果使用 ADK Runner，则可参考 `quickstart/chatwitheino` 的 Runner、SSE、会话与 interrupt/resume 设计；如果只迁当前后端能力，直接使用 Eino Graph + Hertz/标准 HTTP SSE 会更轻。

## 可复用的 eino-examples 位置

| 示例 | 可复用点 | 迁移用途 |
|---|---|---|
| `/home/zhaoting/Agent/eino-examples/quickstart/chatwitheino` | ADK Agent、Runner、SSE、JSONL session、审批中断、A2UI | 如果后续需要会话持久化、人机审批、前端事件协议，可作为服务壳参考 |
| `/home/zhaoting/Agent/eino-examples/quickstart/chatwitheino/rag/rag.go` | Workflow 文档问答、并行 chunk 评分、综合回答 | 可改造成产品详情页局部 RAG 或条款问答 |
| `/home/zhaoting/Agent/eino-examples/quickstart/eino_assistant` | RedisRetriever、索引图、ReAct Agent | 可作为 RAG 二期的 Go 实现参考 |
| `/home/zhaoting/Agent/eino-examples/adk/intro/http-sse-service` | HTTP SSE 事件转换 | 可参考 SSE event writer 与 agent event 映射 |
| `/home/zhaoting/Agent/eino-examples/adk/common/tool/graphtool` | 将 Graph/Chain/Workflow 包装成 Tool | 可把 `ProductDetailFlow` 作为主 Agent 可调用工具 |

## 迁移顺序建议

## 当前迁移执行进度

| 阶段 | 状态 | 任务/报告 |
|---|---|---|
| P0：主链路迁移 | 已完成 | `docs/tasks/TASK-20260520-eino-p0-migration.md`、`docs/tasks/TASK-20260520-eino-p0-migration-report.md` |
| P1：产品详情与治理能力 | 已完成 | `docs/tasks/TASK-20260520-eino-p1-product-detail-governance.md`、`docs/tasks/TASK-20260520-eino-p1-product-detail-governance-report.md` |
| P2：搜索、RAG 与增强能力 | 已完成 | `docs/tasks/TASK-20260520-eino-p2-search-rag-eval.md`、`docs/tasks/TASK-20260520-eino-p2-search-rag-eval-report.md` |

### P0：保证前端不感知后端语言变化

1. 建 Go HTTP 服务，复刻 `/api/healthz`、`/api/suggestions`、`/api/providers`、`POST /api/chat`。
2. 固化 `ChatRequest`、`ProductCard`、`ProductDetail`、SSE 事件 JSON 字段，保持前端兼容。
3. 实现 LLM provider 注册和阶段路由，先支持当前默认 MiniMax OpenAI-compatible 配置。
4. 实现 `ChatFlow` 主图：意图识别、平台搜索、fallback 上下文、流式回答、sources/disclaimer/done。
5. 迁移三家平台直连搜索与公共过滤规则。

### P1：迁产品详情与治理能力

1. 迁移 `ProductDetailSkill`：抓取、HTML 清洗、LLM 提取、校验、缓存、流式解读。
2. 替换内存缓存为可选 Redis 缓存，避免 Go 服务多副本时缓存不一致。
3. 迁移统一错误、requestId middleware、基础合规替换。
4. 建立 Go 单元测试和端到端 SSE 测试，对齐现有 Python 测试样例。

### P2：迁实验链路与增强能力

1. 迁移 RAG 入库链路，确认继续使用 pgvector 还是切换 Redis vector。
2. 迁移 MiniMax/MCP 通用搜索能力，作为 fallback search 的可配置后端。
3. 迁移 replay/eval，用于 Prompt 迭代回归。
4. 评估是否引入 ADK session、interrupt/resume、A2UI 等能力。

## 首批不建议迁移的内容

| 模块 | 原因 | 处理建议 |
|---|---|---|
| `ProductSearchService` 旧 MiniMax 商品页搜索 | 当前主编排已改为 `platform_apis.search_all_platforms()`，旧链路不是主路径 | 暂列 P2，除非平台 API 不稳定再作为兜底 |
| `LangGraphOrchestrator` | 当前只是占位实现 | Go 迁移后直接用 Eino Graph，不需要等价迁占位 |
| RAG 入库到主对话 | 当前 docs 标记仍在探索，且未接入 `/api/chat` | 先迁入库能力或检索能力，不要阻塞主链路切换 |
| replay 失败样例框架 | 对线上用户链路无直接依赖 | P2 迁入评测工具链 |

## 风险点

1. 平台 API 迁移风险最高，尤其是小雨伞、平安、慧择的请求参数、cookie、反爬和字段解析，需要逐个平台做 golden test。
2. SSE 合约必须完全兼容前端，事件名和 JSON 字段不能随意改名。
3. LLM JSON 输出在 Go 侧要加强 schema 校验和降级策略，否则意图识别失败会放大为整条链路失败。
4. 当前产品详情缓存是进程内内存，Go 多实例部署前需要决定是否迁到 Redis。
5. RAG 入库当前仍是探索状态，首批迁移不应把它列为上线阻塞项。

## 验收口径

首批 Go Eino 后端达到以下条件，才算完成主链路迁移：

1. `/api/chat` SSE 事件顺序与 Python 当前实现兼容。
2. 推荐类问题能返回 `products`，并继续输出流式 `delta`。
3. 知识解释类问题不返回产品卡片，但能返回流式回答和来源。
4. `product_detail` 和 `product_followup` 能返回 `detail_items` 与后续解读。
5. 现有关键测试用例在 Go 侧有等价覆盖：聊天接口、意图识别、平台搜索过滤、产品详情提取校验、SSE 错误事件。
