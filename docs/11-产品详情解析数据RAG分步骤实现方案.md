# 产品详情解析数据 RAG 分步骤实现方案

状态：基础链路已落地，持续评估优化中
更新时间：2026-06-10
适用范围：Go Eino 后端、`product_details` 结构化商品详情库、pgvector RAG、知识检索、产品问答与对比
参考补充：已融合 2026-06-04《AI 解析数据 RAG 落地方案》的验证结论、异步入库、质量门槛和评估监控思路

实现进展：

- Step 1 Ark embedding 基础能力已完成，并通过真实 Ark endpoint smoke 验证。
- Step 2-Step 7 已完成后端基础链路：source snapshot、formatter、ingestor、离线回填命令、pgvector 检索、RAG search service、chatflow fallback 降级接入、解析成功后可选异步 RAG 入库。
- 线上异步 RAG 入库已采用固定 worker + 有界队列，避免 embedding/pgvector 抖动挤占解析链路资源。
- Step 8-Step 9 仍需结合真实数据集做回答 prompt、sources 展示、命中率和监控指标优化。

## 一、背景

当前后端已经具备两类相关能力：

1. 产品详情解析共享库：`product_details` / `product_detail_aliases` 保存 AI 解析后的 `schema.ProductDetail`，并通过 Redis + MySQL 支持跨用户复用。
2. 基础 RAG 入库链路：`cmd/ingesturls` 和 `internal/rag/ingest` 支持抓取 URL、清洗 HTML、切分文本、生成 embedding，并写入 `rag_documents` / `rag_chunks`。
3. 已验证 `/api/chat` 的 `product_detail` 链路可以返回 `detail_items`，且缓存命中时也应继续返回结构化详情，避免“已解析但前端拿不到结构化数据”。

现有缺口：

1. `product_details` 结构化数据还没有同步到 pgvector。
2. `rag/store` 只有写入接口，没有查询相似 chunk 的检索接口。
3. `chatflow.FallbackSearcher` 当前仍使用 fallback 搜索，没有优先使用 RAG 商品知识库。
4. 产品详情数据和原始页面文本还没有形成“结构化事实 + 原文依据”的组合召回。
5. 当前 `product_details` 只保存解析后的 `detail_json`，需要新增 `product_detail_sources` 保存解析时使用的原文快照。

本方案目标是分步骤实现：把已解析商品详情转为可检索知识，接入 RAG 检索，并服务于知识问答、产品详情问答、产品对比和推荐理由生成。

## 二、总体原则

```text
MySQL product_details
  -> 权威结构化事实库

Redis product_detail:v1:{product_key}
  -> 精确命中热缓存

Postgres pgvector rag_documents / rag_chunks
  -> 语义召回索引
```

原则：

1. MySQL 仍是商品解析结果的事实来源。
2. pgvector 只做语义检索索引，可重建。
3. Redis 只做热缓存和锁，不承载 RAG 事实。
4. RAG 回答必须携带来源 URL 和商品信息。
5. 商品详情 RAG 不保存用户问题、会话历史、健康告知或个人偏好。
6. 线上解析链路可以异步触发 RAG 入库，但不能阻塞 SSE 的 `detail_items`、`delta` 和 `done`。
7. 不把 `rag_documents.metadata.detail_json` 作为详情缓存主路径；完整 `detail_json` 仍从 MySQL `product_details` 读取，RAG metadata 只保存检索、过滤、追溯所需的紧凑字段。

命名决策：

旧方案使用 `namespace=product_detail`。当前方案统一使用 `namespace=product_details`，与 MySQL 表名和配置 `RAG_SEARCH_NAMESPACE` 保持一致。如果历史 pgvector 数据已经写入 `product_detail`，迁移期可以在检索侧临时同时查询两个 namespace，但新写入统一落到 `product_details`。

## 三、分阶段实现

### 阶段一：把 `product_details` 转成 RAG 文本

目标：先不改聊天链路，只实现“结构化详情 -> RAG 文本块”的转换能力。

输入：

```text
product_details.detail_json
product_details.product_key
product_details.platform
product_details.canonical_url
product_details.prompt_version
product_details.match_rate
product_details.expires_at
product_detail_sources.cleaned_text
product_detail_sources.raw_payload
product_detail_sources.content_hash
```

输出文本建议：

```text
产品名称：XX百万医疗险
平台：pingan
商品唯一键：pingan:ZP021636
来源链接：https://...

保障责任：一般医疗保险金
保额/限额：300万
是否可选：否
责任说明：保障住院医疗费用、特殊门诊费用和住院前后门急诊费用。
```

每个商品建议生成四类 chunk：

| chunk 类型 | 内容 | 用途 |
|---|---|---|
| `product_detail_summary` | 商品名称、平台、全部保障责任摘要 | 商品级召回和对比 |
| `product_duty` | 单个保障责任的名称、保额、说明、是否可选 | 精准责任问答 |
| `product_tag` | 单个高置信标签及其对应商品理由、相关保障责任 | 标签导向召回，如“医疗险、高端、家庭、性价比、个人” |
| `product_source_excerpt` | 原文清洗文本中的相关段落 | 原文追溯、合同条款依据补充 |

tag 策略：

tag 在第一版中同时承担两种角色：

1. 作为 `product_detail_summary` 和 `product_duty` 的 metadata，用于 filter 和 rerank。
2. 作为独立的 `product_tag` chunk_type，用于承接标签导向问题。

`product_tag` 是“产品内标签 chunk”，不是跨产品全局聚合 chunk。也就是说，一个商品如果有 `医疗险`、`高端`、`家庭` 三个高置信标签，会额外生成三条 `product_tag` chunk：

```text
chunk_type=product_tag, tag_name=医疗险
chunk_type=product_tag, tag_name=高端
chunk_type=product_tag, tag_name=家庭
```

这样用户问“有没有适合家庭的高端医疗险”时，既可以通过 tag metadata 过滤，也可以通过 `product_tag` chunk 语义召回到对应商品。

推荐 tag 分层：

| tag 类型 | 示例 | 来源 |
|---|---|---|
| `insurance_tags` | `医疗险`、`重疾险`、`意外险`、`年金险` | 产品名称、保障责任关键词、平台分类 |
| `market_tags` | `高端`、`中端`、`性价比`、`少儿`、`老人` | 产品名称、价格标签、责任强度、平台标签 |
| `audience_tags` | `个人`、`家庭`、`儿童`、`老人`、`企业` | 产品名称、投保对象、用户查询上下文 |
| `duty_tags` | `外购药`、`质子重离子`、`门急诊`、`住院医疗`、`身故` | duty name 和 description |
| `quality_tags` | `高匹配`、`低匹配`、`AI解析`、`平台接口解析` | match_rate、解析来源、prompt_version |

tag 示例：

```json
{
  "tags": ["医疗险", "高端", "家庭", "外购药", "质子重离子"],
  "insurance_tags": ["医疗险"],
  "market_tags": ["高端"],
  "audience_tags": ["家庭"],
  "duty_tags": ["外购药", "质子重离子"]
}
```

chunk 内容中也建议显式写入标签，帮助 embedding 捕捉语义：

```text
产品标签：医疗险、高端、家庭
保障标签：外购药、质子重离子
```

`product_tag` chunk 示例：

```text
产品名称：XX百万医疗险
平台：pingan
标签：高端
标签分类：market_tags
适用说明：该产品包含特需部、国际部、VIP部或中高端医疗相关责任，因此归入高端医疗保障标签。
相关保障责任：一般医疗保险金、特定疾病医疗费用保险责任、质子重离子医疗。
来源链接：https://...
```

标签 chunk 只用于召回和推荐理由增强，不替代结构化字段。回答具体保障责任时仍以 `product_duty` chunk 和 `duties` 的保障责任、保额、说明为准。

为避免 chunk 爆炸，第一版限制：

1. 每个产品最多生成 6 条 `product_tag` chunk。
2. 只为高置信、可解释的标准标签生成 tag chunk。
3. 不为 tag 组合生成 chunk，例如不生成“高端+家庭”组合 chunk；组合查询通过 metadata filter 和 rerank 解决。
4. `quality_tags` 默认只进 metadata，不生成 `product_tag` chunk。

原文 chunk 策略：

`product_source_excerpt` 来自 `product_detail_sources.cleaned_text`，用于追溯更完整的原文依据。它和结构化 chunks 的关系如下：

```text
product_details.detail_json
  -> product_detail_summary / product_duty / product_tag

product_detail_sources.cleaned_text
  -> product_source_excerpt
```

第一版原文 chunk 不直接使用 raw HTML，而是使用清洗后的 `cleaned_text`。如果需要审计完整原始响应，再通过 `raw_payload` 回查。

切分规则：

1. 优先按段落、条款标题、保障责任标题切分。
2. 单个 `product_source_excerpt` 控制在 500-1200 中文字。
3. 对长原文使用现有 chunker，建议 `chunk_size=1200`、`overlap=200`。
4. metadata 必须包含 `content_hash`，用于追溯到 `product_detail_sources`。
5. 原文 chunk 只做依据补充；结构化问答仍优先使用 `product_duty`。

建议新增模块：

```text
internal/rag/productdetail
  formatter.go
  formatter_test.go
```

核心接口：

```go
type ProductDetailDocument struct {
    ProductKey string
    Detail     schema.ProductDetail
    Metadata   map[string]any
}

type FormattedChunk struct {
    Content  string
    Metadata map[string]any
}

func FormatProductDetail(detail StoredProductDetail) []FormattedChunk
```

验收标准：

1. 单个 `ProductDetail` 能生成稳定文本。
2. 每个 duty 生成独立 chunk。
3. 每个高置信标准 tag 可生成独立 `product_tag` chunk。
4. `cleaned_text` 可生成 `product_source_excerpt` chunk。
5. metadata 包含 `product_key`、`product_name`、`platform`、`source_url`、`chunk_type`、`tag_name`、`tag_category`、`source_hash`、`content_hash`、`source_type`。
6. 不包含用户问题和会话信息。

入库质量门槛：

只有满足以下条件的数据才进入 RAG：

1. `product_name` 非空。
2. `product_key` 可生成，或 `canonical_url` 可规范化。
3. `duties` 非空。
4. AI 抽取结果通过现有校验，或 heuristic 兜底结果达到最低责任数量。
5. `match_rate` 达到阈值，建议第一版使用 `>= 0.6`。
6. `expires_at` 未过期，且数据状态为 active。
7. `source_url` 不包含 `_rag_check`、`utm_*`、临时 session 参数等噪声参数。

### 阶段二：实现从 MySQL 同步与线上异步入库到 pgvector

目标：同时支持离线批量回填和线上解析成功后的异步增量入库，把 `product_details` 中 `active` 且未过期的数据写入 `rag_documents` / `rag_chunks`。

两条入库路径：

1. 离线回填：`cmd/ingestproductdetails` 批量读取 MySQL 已解析商品，适合初始化索引、重建索引和修复历史数据。
2. 在线增量：`productdetail.Service.run` 在解析成功、MySQL 持久化成功后，异步触发 RAG 入库，适合让新解析商品尽快进入可检索知识库。

建议新增命令：

```text
cmd/ingestproductdetails
```

运行示例：

```bash
MYSQL_DSN=... \
DATABASE_URL=... \
EMBEDDING_PROVIDER=ark \
EMBEDDING_MODEL=ep-xxxxxxx-xxxxx \
EMBEDDING_API_KEY=... \
EMBEDDING_API_BASE=https://ark.cn-beijing.volces.com/api/v3 \
go run ./cmd/ingestproductdetails --namespace product_details --limit 500
```

数据流：

```text
MySQL product_details + product_detail_sources
  -> 查询 active / 未过期 / prompt_version 合格的数据
  -> FormatProductDetail
  -> FormatProductSourceExcerpts
  -> EmbedTexts / Ark EmbedStrings
  -> PgVectorStore.UpsertDocumentWithChunks
```

在线异步入库链路：

```text
product_detail 请求
  -> Redis / MySQL 详情缓存命中
    -> 直接返回 detail_items
    -> 如 RAG 索引缺失或过期，可后台补写

product_detail 请求
  -> 未命中详情缓存
  -> 抓取页面并 AI 解析
  -> 返回 detail_items
  -> 写入 product_details / product_detail_sources
  -> 异步 IngestProductDetail
  -> 继续输出通俗解读或追问回答
```

异步入库要求：

1. 入库失败只记录日志和指标，不影响前端 SSE。
2. 入库任务必须使用短超时和并发限制，避免 embedding 或 pgvector 抖动拖垮解析服务。
3. 同一个 `product_key` 重复入库时覆盖旧 chunks。
4. 如果 MySQL 持久化失败，不写 RAG，避免 pgvector 中出现没有事实源的数据。
5. 缓存命中时不强制同步入库，只在索引缺失、`source_hash` 变化或 `prompt_version` 变化时补写。

建议新增服务接口：

```go
type ProductDetailIngestor interface {
    IngestProductDetail(ctx context.Context, detail StoredProductDetail) (IngestResult, error)
}

type IngestResult struct {
    DocumentID int64
    ChunkCount int
    Skipped    bool
    Reason     string
}
```

Embedding 接入方案：

当前商品详情 RAG 的入库对象全部是文本 chunk，包括 `product_detail_summary`、`product_duty`、`product_tag` 和 `product_source_excerpt`。因此第一版只接入文本向量化，不接入多模态向量化。

推荐使用 Eino 原生 Ark embedding 组件作为主实现：

```go
import "github.com/cloudwego/eino-ext/components/embedding/ark"

embedder, err := ark.NewEmbedder(ctx, &ark.EmbeddingConfig{
    APIKey:  cfg.EmbeddingAPIKey,
    Model:   cfg.EmbeddingModel,   // Ark 控制台中的 embedding 推理接入点 ID，例如 ep-xxxx
    BaseURL: cfg.EmbeddingAPIBase, // 默认 https://ark.cn-beijing.volces.com/api/v3
    Region:  cfg.EmbeddingRegion,  // 默认 cn-beijing
    Timeout: &timeout,
})
embeddings, err := embedder.EmbedStrings(ctx, texts)
```

接入方式：

1. 在 `internal/rag/embedding` 保留现有 OpenAI-compatible HTTP client。
2. 新增 Ark adapter，内部调用 Eino Ark `EmbedStrings(ctx, []string)`，对外仍暴露当前 `EmbedTexts(ctx, []string)` 风格接口，避免影响 `cmd/ingesturls` 和后续 `cmd/ingestproductdetails`。
3. 增加 embedding factory，根据 `EMBEDDING_PROVIDER` 选择实现：
   - `ark`：使用 Eino Ark embedding。
   - `openai_compatible`：使用现有 HTTP client。
4. `EMBEDDING_MODEL` 在 Ark 模式下建议填写推理接入点 ID，而不是只填写基础模型名称；实际值以火山方舟控制台创建的 endpoint 为准。
5. `EMBEDDING_DIMENSIONS` 一旦用于生产索引后不能随意变更，必须和 pgvector 字段维度、索引维度保持一致；如果维度变化，需要重建 `rag_chunks` embedding。

火山文档里还有多模态向量化 API，路径是 `/embeddings/multimodal`，适合图片、视频或图文对象的向量化。当前商品详情 RAG 第一版不使用该接口，原因是我们的入库数据已经清洗成文本；后续如果要把商品页面截图、条款 PDF 图片或宣传图也入库，需要单独扩展 `EmbeddingInput` 数据结构，不能复用现在的 `EmbedTexts([]string)` 接口。

官方参考：

1. 火山方舟文本向量化 API：`https://www.volcengine.com/docs/82379/1302003/`
2. 火山方舟多模态向量化 API：`https://www.volcengine.com/docs/82379/1523520`
3. Eino Ark embedding 组件：`https://www.cloudwego.io/zh/docs/eino/ecosystem_integration/embedding/embedding_ark/`

`rag_documents` 建议：

| 字段 | 值 |
|---|---|
| `namespace` | `product_details` |
| `source_type` | `product_detail` |
| `source_url` | `productdetail://{product_key}` 或 canonical URL |
| `title` | product_name |
| `cleaned_text` | 商品详情格式化全文 |
| `metadata` | 商品级 metadata |

`rag_documents.metadata` 不建议写入完整 `detail_json`。原因是完整结构化详情已经保存在 MySQL `product_details.detail_json`，pgvector metadata 只需要承担过滤、展示和追溯职责。建议只保存：

```json
{
  "product_key": "pingan:ZP021636",
  "product_name": "XX百万医疗险",
  "platform": "pingan",
  "canonical_url": "https://...",
  "normalized_url_hash": "sha256",
  "duty_count": 13,
  "required_duty_count": 7,
  "optional_duty_count": 6,
  "match_rate": 0.92,
  "prompt_version": "detail_extract_v1",
  "source_hash": "cleaned-text-sha256",
  "indexed_at": "2026-06-10T00:00:00Z"
}
```

如果需要从 RAG 结果回查完整结构化详情，应使用 metadata 中的 `product_key` 或 `normalized_url_hash` 回查 MySQL，而不是从 pgvector metadata 还原。

`rag_chunks.metadata` 建议：

```json
{
  "namespace": "product_details",
  "source_type": "product_detail",
  "product_key": "pingan:ZP021636",
  "product_name": "XX百万医疗险",
  "platform": "pingan",
  "source_url": "https://...",
  "chunk_type": "product_tag",
  "tag_name": "高端",
  "tag_category": "market_tags",
  "duty_name": "外购药保险金",
  "coverage": "100万",
  "is_optional": true,
  "source_hash": "cleaned-text-sha256",
  "content_hash": "cleaned-text-sha256",
  "tags": ["医疗险", "高端", "家庭", "外购药"],
  "insurance_tags": ["医疗险"],
  "market_tags": ["高端"],
  "audience_tags": ["家庭"],
  "duty_tags": ["外购药"],
  "match_rate": 0.92,
  "prompt_version": "detail_extract_v1"
}
```

需要新增 MySQL 查询能力：

```go
ListActive(ctx, params ListProductDetailParams) ([]StoredProductDetail, error)
```

或单独建同步 reader，避免污染现有在线 repository。

验收标准：

1. 可批量同步 MySQL 商品详情到 pgvector。
2. 同一个 `product_key` 重复同步可覆盖旧 chunks。
3. 结构化 chunks 和原文 chunks 能通过 `content_hash` 关联同一份原文快照。
4. 低质量、过期、disabled 商品不入索引。
5. 命令输出成功数、跳过数、失败数。

### 阶段三：为 pgvector 增加查询接口

目标：在 `internal/rag/store` 增加相似度检索能力，供知识检索使用。

当前 `Store` 只有：

```go
UpsertDocumentWithChunks(ctx, input)
```

建议新增只读接口：

```go
type SearchQuery struct {
    Namespace string
    Vector    []float64
    TopK      int
    Filters   map[string]any
}

type SearchResult struct {
    Content  string
    Score    float64
    Metadata map[string]any
    SourceURL string
    Title    string
}

type SearchStore interface {
    SearchSimilarChunks(ctx context.Context, query SearchQuery) ([]SearchResult, error)
}
```

SQL 方向：

```sql
SELECT
  c.content,
  c.metadata,
  d.source_url,
  d.title,
  c.embedding <=> $vector AS distance
FROM rag_chunks c
JOIN rag_documents d ON d.id = c.document_id
WHERE d.namespace = $namespace
ORDER BY c.embedding <=> $vector
LIMIT $topK;
```

如需要 metadata 过滤：

```sql
AND c.metadata @> '{"source_type":"product_detail"}'::jsonb
```

tag 过滤示例：

```sql
AND c.metadata @> '{"insurance_tags":["医疗险"]}'::jsonb
AND c.metadata @> '{"audience_tags":["家庭"]}'::jsonb
```

第一版建议只做精确 tag filter。后续如果需要“性价比”和“价格便宜”、“高端”和“中高端医疗”这类近义标签匹配，再增加 tag 归一化词典。

产品追问过滤示例：

```sql
AND c.metadata @> '{"product_key":"huize:104006:108504"}'::jsonb
```

检索策略：

1. 产品详情追问：优先按 `product_key` 或 `normalized_url_hash` 精确过滤，只查当前产品的 chunks。
2. 产品对比：分别按多个 `product_key` 过滤召回，再在回答层按责任维度对齐。
3. 通用保险问题：查询 `namespace=product_details`，必要时混合 fallback 知识库。
4. 标签导向推荐：先用 tag metadata 缩小范围，再用向量相似度和 rerank 排序。
5. 精确产品问题中，如果 MySQL 能直接查到结构化详情，应先用 MySQL 详情；RAG 作为语义补充，不替代事实源。

验收标准：

1. 能按 query embedding 返回 topK chunks。
2. 返回结果包含 content、source_url、title、metadata、score。
3. 支持 namespace 过滤。
4. 单元测试覆盖 SQL 构建，不依赖 live Postgres。

### 阶段四：实现 RAG Knowledge Search 服务

目标：把“用户 query -> embedding -> pgvector search -> SearchResultItem”封装成服务，替换或增强现有 fallback knowledge search。

建议新增：

```text
internal/rag/search
  service.go
  service_test.go
```

接口：

```go
type Service struct {
    embedder embedding.Client
    store    ragstore.SearchStore
}

func (s *Service) Search(ctx context.Context, query string, opts Options) ([]schema.SearchResultItem, error)
```

输出映射到现有 `schema.SearchResultItem`：

```go
Title   = product_name 或 document title
URL     = source_url
Site    = platform 或 "SmartInsure RAG"
Snippet = chunk content 摘要
```

接入点：

```text
internal/agent/chatflow/production.go
  flow.Fallback = ragSearchAdapter if configured
  fallbackAdapter as fallback
```

推荐策略：

```text
如果 DATABASE_URL + EMBEDDING 配置完整：
  使用 RAG search
  RAG search 失败时降级 fallback.NewService
否则：
  保持现有 fallback
```

Agent 使用方式：

第一版不新增 Planner 可见的复杂动作，优先复用现有动作：

1. `product_detail`：负责首次解析产品详情，并在成功持久化后触发异步 RAG 入库。
2. `product_followup`：优先使用当前产品的 MySQL 结构化详情，必要时按 `product_key` 检索 RAG chunks 补充原文依据。
3. `knowledge_search`：负责从 RAG 检索已入库的产品责任、标签和原文片段；RAG 不可用时降级 fallback。
4. `final_answer`：基于检索上下文生成回答。

后续如果需要更细的内部 tool，可以新增但不暴露给 Planner：

```text
product_detail_cache_lookup
product_detail_rag_ingest
product_detail_rag_search
```

验收标准：

1. RAG 检索可返回 `SearchResultItem`。
2. RAG 不可用时不影响主聊天链路。
3. `/api/chat` sources 事件仍使用现有 `sources` payload。
4. Plan-Act `knowledge_search` 和 DeepAgent `knowledge_search` 自动复用该检索能力。

### 阶段五：接入回答生成上下文

目标：让回答模型基于 RAG chunks 作答。

当前 `answer.Service` 已接收 `searchResults []schema.SearchResultItem`，并通过 `FormatSearchContext` 放入 prompt。因此第一版不需要大改 answer，只需要让 `Fallback.Search` 返回 RAG 结果。

回答上下文建议：

```text
[1] XX百万医疗险 - 外购药保险金
来源：https://...
内容：保障责任：外购药保险金；保额/限额：100万；是否可选：是；说明：报销院外特定药品费用。
```

需要强化 prompt 的点：

1. 明确只能基于搜索结果和已知保险常识回答。
2. 商品保障责任必须以来源条款或解析结果为准。
3. 不确定时说明“公开信息中未检索到”。
4. 当 `coverage=详见条款` 时，只能说明该责任存在，不能推断精确赔付比例、免赔额或限额。
5. 等待期、除外责任、续保条件、赔付比例等高风险细节必须提示用户以条款和投保页面为准。

验收标准：

1. 用户问“外购药能报吗”时能召回 duty chunk。
2. 回答中能引用商品名称和来源。
3. sources 事件返回对应商品 URL。
4. 不改变 SSE 事件名。

### 阶段六：支持产品对比和推荐理由增强

目标：让 RAG 支持多商品责任对比，以及产品推荐后的理由生成。

推荐做法：

1. 产品搜索返回卡片后，收集 product URLs。
2. 通过 product_key 或 URL hash 精确查 `product_details`。
3. 对未精确命中的商品，再用 RAG query 做语义补充。
4. 将结构化 duty 数据传给 answer prompt。

第一版可以先不改产品卡片 payload，只增强回答内容。

适用问题：

```text
这两个产品哪个外购药更好？
平安这款和慧择这款一般医疗保额有什么区别？
推荐的产品为什么适合我？
```

验收标准：

1. 多商品对比能输出责任维度差异。
2. 不把没有检索到的责任编造出来。
3. 仍保留免责声明。

## 四、建议实施顺序

### Step 1：接入 Ark embedding API 基础能力

先新增 embedding provider factory，并接入 Eino Ark embedding。该步骤先于商品详情入库，因为向量维度、模型 endpoint、batch 行为和 pgvector schema 必须提前固定。

详细实施方案见：`docs/12-Ark-Embedding接入实施方案.md`。

优先级：最高。

原因：后续 formatter、ingestor、批量同步和检索都依赖统一的 `EmbedTexts(ctx, []string)` 能力。先把 Ark embedding 跑通，可以尽早确认模型 endpoint、输出维度、超时、错误处理和现有 OpenAI-compatible client 的 fallback 边界。

落地内容：

1. 在 `internal/rag/embedding` 新增 provider factory。
2. 新增 Eino Ark adapter，内部调用 Ark `EmbedStrings(ctx, []string)`。
3. 保留现有 OpenAI-compatible HTTP client，作为 `openai_compatible` provider。
4. 让 `cmd/ingesturls` 先复用 factory，避免后续 `cmd/ingestproductdetails` 再重复接入。
5. 启动或命令执行时校验 `EMBEDDING_PROVIDER`、`EMBEDDING_MODEL`、`EMBEDDING_API_BASE`、`EMBEDDING_DIMENSIONS`。

Ark 模式的最小配置：

```bash
EMBEDDING_PROVIDER=ark
EMBEDDING_API_KEY=...
EMBEDDING_API_BASE=https://ark.cn-beijing.volces.com/api/v3
EMBEDDING_REGION=cn-beijing
EMBEDDING_MODEL=ep-xxxxxxx-xxxxx
EMBEDDING_DIMENSIONS=1024
```

`EMBEDDING_DIMENSIONS` 是否配置取决于 Ark endpoint 支持的维度和 pgvector schema。只要生产库已经建好固定维度，就必须以数据库维度为准。

验收标准：

1. `EMBEDDING_PROVIDER=ark` 时使用 Eino Ark embedding。
2. `EMBEDDING_PROVIDER=openai_compatible` 或为空时保持现有 HTTP client 行为。
3. `cmd/ingesturls` 可以通过 factory 创建 embedder。
4. 单元测试覆盖 provider 选择、缺失配置、维度配置和 fallback 行为。
5. 有真实 Ark key 时可用一条文本 smoke 验证输出维度。

### Step 2：格式化结构化商品详情和 tag

新增 formatter，把 `ProductDetail` 转成 summary chunk + duty chunks + tag chunks，并把 `product_detail_sources.cleaned_text` 转成 source excerpt chunks；同时为每个 chunk 生成标准化 metadata。

优先级：最高。

原因：这是后续同步、embedding、检索的共同输入。

tag 生成规则优先使用确定性规则：

1. 产品名称包含“医疗”“百万医疗”“中高端医疗” -> `医疗险`。
2. 产品名称或责任包含“重疾”“重大疾病” -> `重疾险` 或 `重大疾病` duty tag。
3. 产品名称、平台标签或责任强度包含“高端”“特需”“国际部”“VIP” -> `高端`。
4. 产品名称或保障对象包含“家庭”“全家”“亲子” -> `家庭`。
5. 产品名称或责任描述包含“少儿”“儿童” -> `儿童`。
6. 责任名称包含“外购药”“特药” -> `外购药`。
7. 责任名称包含“质子重离子” -> `质子重离子`。

不建议第一版用 LLM 生成 tag，避免标签不稳定。LLM tag 可以作为后续增强，但必须落到固定枚举词表。

### Step 3：新增 `ProductDetailIngestor` 和 `ingestproductdetails`

新增统一入库服务，并提供 `cmd/ingestproductdetails` 把 MySQL 中已解析商品同步到 pgvector。

优先级：最高。

原因：线上异步入库和离线回填都应复用同一套格式化、embedding 和 upsert 逻辑，否则两条链路容易产生不一致的 chunk。

同步命令必须通过 Step 1 的统一 embedding factory 创建 embedder：

```text
EMBEDDING_PROVIDER=ark
  -> Eino Ark EmbedStrings

EMBEDDING_PROVIDER=openai_compatible 或空
  -> 现有 OpenAI-compatible HTTP embeddings client
```

### Step 4：解析成功后异步触发 RAG 入库

在 `productdetail.Service.run` 中，解析成功并写入 MySQL 后异步调用 `ProductDetailIngestor`。

优先级：高。

原因：离线回填解决历史数据，异步入库解决新数据实时沉淀。该步骤必须保证入库失败不影响 `detail_items` 返回。

线上增量配置：

```bash
PRODUCT_DETAIL_RAG_ENABLED=false
PRODUCT_DETAIL_RAG_NAMESPACE=product_details
PRODUCT_DETAIL_RAG_MIN_MATCH_RATE=0.6
PRODUCT_DETAIL_RAG_ASYNC_TIMEOUT=30
PRODUCT_DETAIL_RAG_ASYNC_WORKERS=2
PRODUCT_DETAIL_RAG_QUEUE_SIZE=100
```

实现要求：

1. 默认关闭，未配置 pgvector 或 embedding 时不影响产品详情解析。
2. 使用固定 worker 和有界队列执行 embedding + pgvector 写入。
3. 队列满或入库失败只记录日志，不影响当前 SSE。
4. 同一运行时配置下复用 pgvector DB pool、embedding client 和 RAG service。
5. `source_url` 使用规范化产品 URL，避免 `_rag_check`、`utm_*`、session 类参数进入 RAG metadata。

### Step 5：扩展 pgvector 查询接口

为 `rag/store` 增加 `SearchSimilarChunks`。

优先级：高。

原因：现有 RAG 只有写入，没有读取。

### Step 6：实现 RAG search service

封装 query embedding + pgvector search + SearchResultItem 映射。

优先级：高。

原因：这是接入 chatflow 的最小服务单元。

### Step 7：接入 `chatflow.Fallback`

在 `NewProduction()` 中优先使用 RAG search，失败降级 fallback。

优先级：中。

原因：可以最小改动接入 `/api/chat`、Plan-Act、DeepAgent。

检索开关配置：

```bash
RAG_SEARCH_ENABLED=true
RAG_SEARCH_NAMESPACE=product_details
RAG_SEARCH_TOP_K=5
RAG_SEARCH_MIN_SCORE=0
RAG_SEARCH_TIMEOUT=5
```

### Step 8：优化回答 prompt 和来源展示

让商品详情 RAG 的 snippet 更适合回答，sources 更准确。

优先级：中。

原因：影响最终回答质量，但可以在基础链路跑通后迭代。

### Step 9：评估与监控

建立固定测试集和基础指标，验证 RAG 的命中率、相关性、耗时和失败率。

优先级：中。

原因：商品详情 RAG 容易出现“能召回但召回错商品”或“召回对但回答过度推断”的问题，需要在接入后持续观察。

建议测试问题集：

1. 外购药能不能报？
2. 特药责任是不是可选？
3. 有没有重疾关爱金？
4. 免赔额和赔付比例公开信息里有没有？
5. 哪些责任是可选责任？
6. 适合家庭的高端医疗险有哪些？

固定验证样本：

可复用 2026-06-04 验证过的产品作为第一批 regression case：

```text
产品：复星联合星相守2号长期医疗保险（个人版）
平台：huize
责任数量：13
必选责任：7
可选责任：6
重点责任：住院期间外购药品及外购医疗器械费用医疗保险金、恶性肿瘤特定药品费用医疗保险金、重大疾病关爱保险金、重大疾病住院拓展特需医疗保险金
```

该样本适合验证：

1. `product_duty` 是否能按单个责任召回。
2. `is_optional` metadata 是否能区分必选和可选责任。
3. `外购药`、`特药`、`重疾险`、`高端` 等 tag 是否能稳定生成。
4. 当额度为“详见条款”时，回答是否避免推断具体金额或赔付比例。

建议指标：

1. `product_detail_rag_ingest_total`
2. `product_detail_rag_ingest_failed_total`
3. `product_detail_rag_ingest_duration_seconds`
4. `product_detail_rag_search_total`
5. `product_detail_rag_search_hit_total`
6. `product_detail_rag_search_latency_seconds`
7. `product_detail_rag_fallback_total`

### Step 10：对比和推荐增强

在产品推荐和 comparison 场景中加入结构化详情精确查询。

优先级：低到中。

原因：价值高，但涉及更多业务编排，适合第二轮迭代。

## 五、配置建议

RAG 相关依赖以配置文件为主，环境变量仅作为覆盖手段。

默认配置文件：

```text
configs/rag.yaml
```

本地私有配置文件：

```text
configs/rag.local.yaml
```

启动时通过 `RAG_CONFIG_PATH=configs/rag.local.yaml` 指定。重启脚本会挂载本地 `configs/` 目录到容器 `/app/configs`，并过滤旧容器里的 `MYSQL_DSN`、`REDIS_URL`、`DATABASE_URL`、`EMBEDDING_*`、`PRODUCT_DETAIL_RAG_*`、`RAG_SEARCH_*`，避免这些依赖继续从旧环境变量读取。

`configs/rag.local.yaml` 示例：

```yaml
mysql_dsn: "user:password@tcp(mysql-host:3306)/smartinsure?parseTime=true&charset=utf8mb4,utf8"
redis_url: "redis://:password@redis-host:6379/0"
database_url: "postgres://user:password@postgres-host:5432/smartinsure?sslmode=disable"
embedding_config_path: configs/embedding.local.yaml

product_detail_rag_enabled: true
product_detail_rag_namespace: product_details
product_detail_rag_min_match_rate: 0.6
product_detail_rag_async_timeout: 30
product_detail_rag_async_workers: 2
product_detail_rag_queue_size: 100

rag_search_enabled: true
rag_search_namespace: product_details
rag_search_top_k: 5
rag_search_min_score: 0
rag_search_timeout: 5
```

配置字段说明：

| 配置字段 | 默认值 | 说明 |
|---|---|---|
| `mysql_dsn` | 空 | 读取和写入 `product_details`、`product_detail_sources` |
| `redis_url` | `redis://localhost:6379/0` | 产品详情热缓存和锁 |
| `database_url` | 空 | pgvector Postgres |
| `embedding_config_path` | `configs/embedding.yaml` | embedding 配置文件路径 |
| `product_detail_rag_enabled` | `false` | 是否在产品详情解析成功后异步入库 RAG |
| `product_detail_rag_namespace` | `product_details` | 商品详情 RAG 写入 namespace |
| `product_detail_rag_min_match_rate` | `0.6` | 允许入库的最低解析匹配率 |
| `product_detail_rag_async_timeout` | `30` | 线上异步入库单次超时秒数 |
| `product_detail_rag_async_workers` | `2` | 线上异步入库并发数 |
| `product_detail_rag_queue_size` | `100` | 线上异步入库队列长度 |
| `rag_search_enabled` | `true` | 是否启用 RAG knowledge search |
| `rag_search_namespace` | `product_details` | 默认查询 namespace |
| `rag_search_top_k` | `5` | 默认召回 chunk 数 |
| `rag_search_min_score` | `0` | 最低相似度阈值，第一版可不启用 |
| `rag_search_timeout` | `5` | RAG 检索超时秒数 |

建议补充 embedding 配置：

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `EMBEDDING_PROVIDER` | `openai_compatible` | `ark` 使用 Eino Ark；`openai_compatible` 使用现有 HTTP client |
| `EMBEDDING_REGION` | `cn-beijing` | Ark region |
| `EMBEDDING_API_TYPE` | 空 | Ark 可填 `text` 或 `multimodal`；`doubao-embedding-vision-*` 使用 `multimodal` |
| `EMBEDDING_DIMENSIONS` | 空 | 输出向量维度；配置后必须与 pgvector schema 一致 |
| `EMBEDDING_RETRY_TIMES` | 空 | Ark client 重试次数，第一版可不暴露或只在 Ark adapter 内部使用 |

## 六、风险与处理

| 风险 | 影响 | 处理 |
|---|---|---|
| 解析结果错误进入 RAG | 回答放大错误 | 只同步 active、未过期、match_rate 合格数据 |
| 商品条款变化 | 旧索引误导回答 | 根据 `expires_at`、`source_hash`、`prompt_version` 重建索引 |
| RAG 无结果 | 回答质量下降 | 降级现有 fallback search |
| 语义召回误命中其他商品 | 对比和问答混乱 | metadata 中带 product_key，产品详情场景优先精确过滤 |
| 异步入库堆积 | 解析服务资源被挤占 | 设置 worker 数、队列长度、单次超时；满队列时跳过并记录指标 |
| namespace 分裂 | 新旧数据检索不完整 | 新写入统一 `product_details`，历史 `product_detail` 通过一次性迁移或临时双 namespace 查询处理 |
| pgvector metadata 过大 | 写入慢、索引膨胀 | 不写完整 `detail_json`，只保存检索过滤字段，完整详情回查 MySQL |
| embedding 维度变更 | pgvector 写入失败或新旧向量不可比 | 固定 `EMBEDDING_DIMENSIONS`，维度变化时重建索引 |
| Ark endpoint 配错 | 入库失败 | 启动时校验 provider、model、api_base、dimension，并在同步命令输出失败原因 |
| pgvector 查询慢 | 接口延迟升高 | 控制 topK、namespace、metadata filter，后续加 ivfflat/hnsw index |
| sources 不清晰 | 用户无法核验 | SearchResultItem URL 使用 canonical_url，Snippet 标明 duty_name |

## 七、第一版推荐范围

第一版建议只做：

1. embedding provider factory，并接入 Eino Ark embedding。
2. 让 `cmd/ingesturls` 先复用统一 embedding factory。
3. `ProductDetail` -> RAG chunks formatter，并生成 `product_detail_summary`、`product_duty`、`product_tag` 三类 chunk。
4. `product_detail_sources.cleaned_text` -> `product_source_excerpt` chunks。
5. `ProductDetailIngestor` 统一入库服务。
6. `cmd/ingestproductdetails` 批量同步。
7. 解析成功后异步触发 RAG 入库，失败不影响 SSE。
8. pgvector 相似 chunk 查询。
9. RAG search service。
10. `chatflow.Fallback` 优先 RAG、失败降级 fallback。
11. 基础指标和测试问题集。
12. 单元测试覆盖 Ark provider、formatter、ingestor、embedding factory、SQL 构建、service 映射、降级。

暂不做：

1. 产品卡片字段改造。
2. 后台定时同步任务。
3. RAG 与 web search 混合排序。
4. 用户画像或长期记忆。
5. 人工审核后台。
6. 从 pgvector metadata 还原完整 `ProductDetail`。

这样能先验证最关键闭环：

```text
已解析商品详情
  -> MySQL 持久化
  -> 异步或批量同步到 pgvector
  -> 用户问题语义召回
  -> answer prompt 使用 RAG 结果
  -> sources 返回商品来源
```
