# Ark Embedding 接入实施方案

状态：基础能力已实现
更新时间：2026-06-10
适用范围：`internal/rag/embedding`、`cmd/ingesturls`、后续 `cmd/ingestproductdetails`、商品详情 RAG 入库
关联文档：`docs/11-产品详情解析数据RAG分步骤实现方案.md` 的 Step 1

## 一、目标

先把 Ark embedding 作为独立基础能力接入，再继续做商品详情 RAG formatter、入库和检索。

本阶段目标：

1. 增加统一 embedding provider factory。
2. 接入 Eino 原生 Ark embedding 组件。
3. 保留现有 OpenAI-compatible `/embeddings` HTTP client。
4. 让 `cmd/ingesturls` 先复用统一 factory。
5. 为后续 `cmd/ingestproductdetails` 和 RAG search 复用同一套 embedding 能力。

非目标：

1. 不实现商品详情 chunk formatter。
2. 不实现 `ProductDetailIngestor`。
3. 不实现 pgvector 相似度查询。
4. 不接入多模态向量化 API。
5. 不在代码或文档中写入真实 API Key。

## 二、当前代码现状

当前已有能力：

1. `internal/rag/embedding/client.go` 定义了 `Embedder` 接口：

```go
type Embedder interface {
    EmbedTexts(ctx context.Context, texts []string) ([][]float64, error)
}
```

2. 当前 `embedding.Client` 是 OpenAI-compatible HTTP client，请求路径固定为：

```text
{EMBEDDING_API_BASE}/embeddings
```

3. `cmd/ingesturls/main.go` 里有本地 `newEmbedder(settings)`，总是返回 `*embedding.Client`。
4. `internal/config.Settings` 目前只有：

```text
EmbeddingModel
EmbeddingAPIKey
EmbeddingAPIBase
EmbeddingTimeout
EmbeddingBatchSize
```

当前缺口：

1. 没有 `EMBEDDING_PROVIDER`。
2. 没有 Ark adapter。
3. 没有 `EMBEDDING_REGION`、`EMBEDDING_DIMENSIONS`、`EMBEDDING_RETRY_TIMES`。
4. `cmd/ingesturls` 和后续商品详情入库无法共享 provider 选择逻辑。
5. pgvector 写入虽然使用 `embedding vector NOT NULL`，但生产索引维度仍需要固定，不能随模型或 endpoint 随意变化。

## 三、接入方式

推荐使用 Eino 原生 Ark embedding 组件：

```go
import "github.com/cloudwego/eino-ext/components/embedding/ark"

embedder, err := ark.NewEmbedder(ctx, &ark.EmbeddingConfig{
    APIKey:     cfg.APIKey,
    Model:      cfg.Model,
    BaseURL:    cfg.APIBase,
    Region:     cfg.Region,
    Timeout:    &timeout,
    RetryTimes: &retryTimes,
    Dimensions: &dimensions,
})
vectors, err := embedder.EmbedStrings(ctx, texts)
```

CloudWeGo Eino Ark 组件支持：

1. `NewEmbedder(ctx, *ark.EmbeddingConfig)`。
2. `EmbedStrings(ctx, []string)`。
3. `APIKey` 或 `AccessKey/SecretKey` 鉴权。
4. `Model` 使用 Ark 平台 endpoint ID，例如 `ep-xxxxxxx-xxxxx`。
5. `BaseURL` 默认北京区域 `https://ark.cn-beijing.volces.com/api/v3`。
6. `Region` 默认 `cn-beijing`。
7. `Timeout`、`RetryTimes`、`Dimensions`。
8. 默认文本 embedding API；如果使用 `doubao-embedding-vision-*`，需要设置 `api_type: multimodal`。

建议依赖版本：

```bash
go get github.com/cloudwego/eino-ext/components/embedding/ark@v0.1.2
```

`v0.1.2` 是当前可查到的最新模块版本，发布时间为 2026-05-27。后续升级需要单独跑 embedding 和 RAG 入库回归。

官方参考：

1. Eino Ark embedding 组件：`https://www.cloudwego.io/zh/docs/eino/ecosystem_integration/embedding/embedding_ark/`
2. 火山方舟文本向量化 API：`https://www.volcengine.com/docs/82379/1302003/`
3. 火山方舟多模态向量化 API：`https://www.volcengine.com/docs/82379/1523520`

## 四、目标结构

建议保持现有 `Embedder` 接口不变，让所有 RAG 入库和检索只依赖：

```go
type Embedder interface {
    EmbedTexts(ctx context.Context, texts []string) ([][]float64, error)
}
```

新增文件：

```text
internal/rag/embedding
  factory.go
  factory_test.go
  ark_embedder.go
  ark_embedder_test.go
```

建议结构：

```go
type Provider string

const (
    ProviderOpenAICompatible Provider = "openai_compatible"
    ProviderArk              Provider = "ark"
)

type Config struct {
    Provider   Provider
    APIBase    string
    APIKey     string
    Model      string
    Region     string
    APIType    string
    Timeout    time.Duration
    BatchSize  int
    Dimensions int
    RetryTimes int
    HTTPClient *http.Client
}

func NewEmbedder(ctx context.Context, cfg Config) (Embedder, error)
```

Provider 选择规则：

```text
EMBEDDING_PROVIDER=ark
  -> Eino Ark adapter

EMBEDDING_PROVIDER=openai_compatible 或空
  -> 现有 OpenAI-compatible HTTP client
```

Ark adapter：

```go
type ArkEmbedder struct {
    inner *ark.Embedder
}

func (e *ArkEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float64, error) {
    return e.inner.EmbedStrings(ctx, texts)
}
```

OpenAI-compatible client 保持现有行为，但建议补充：

1. `Dimensions > 0` 时在请求 payload 中带上 `dimensions`。
2. 校验返回向量数量必须等于输入文本数量。
3. 校验每条向量非空。
4. 保留 batch 行为。

## 五、配置设计

新增配置字段：

```go
EmbeddingProvider   string
EmbeddingRegion     string
EmbeddingAPIType    string
EmbeddingDimensions int
EmbeddingRetryTimes int
```

环境变量：

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `EMBEDDING_PROVIDER` | `openai_compatible` | `ark` 使用 Eino Ark；`openai_compatible` 使用现有 HTTP client |
| `EMBEDDING_MODEL` | `text-embedding-3-small` | Ark 模式下建议填写 endpoint ID，例如 `ep-xxxx` |
| `EMBEDDING_API_KEY` | 空 | Ark API Key 或 OpenAI-compatible key |
| `EMBEDDING_API_BASE` | 空 | Ark 建议 `https://ark.cn-beijing.volces.com/api/v3` |
| `EMBEDDING_REGION` | `cn-beijing` | Ark region |
| `EMBEDDING_API_TYPE` | 空 | Ark 可填 `text` 或 `multimodal`；空值默认文本 API，若 `api_base` 以 `/embeddings/multimodal` 结尾则自动推断为多模态 |
| `EMBEDDING_DIMENSIONS` | `0` | `0` 表示使用模型默认维度；生产建议显式固定 |
| `EMBEDDING_RETRY_TIMES` | `2` | Ark 请求重试次数 |
| `EMBEDDING_TIMEOUT` | `30` | 单次请求超时秒数 |
| `EMBEDDING_BATCH_SIZE` | `16` | OpenAI-compatible client 批大小；Ark 第一版也可复用为调用前分批参数 |

Ark 最小配置：

```bash
EMBEDDING_PROVIDER=ark
EMBEDDING_API_KEY=...
EMBEDDING_API_BASE=https://ark.cn-beijing.volces.com/api/v3
EMBEDDING_REGION=cn-beijing
EMBEDDING_API_TYPE=text
EMBEDDING_MODEL=ep-xxxxxxx-xxxxx
EMBEDDING_DIMENSIONS=1024
```

注意：

1. Ark 模式下 `EMBEDDING_MODEL` 推荐填写 Ark 控制台创建的推理接入点 ID。
2. `EMBEDDING_DIMENSIONS` 必须和 pgvector 存量数据、索引维度保持一致。
3. 如果维度从 1024 改为 2048，已有向量不可混用，需要重建 `rag_chunks`。
4. 第一版只支持 API Key。AK/SK 可作为后续增强。
5. Ark 模式不复用 LLM provider 的 key/base fallback，必须显式配置 Ark 的 `api_key`、`api_base`、`model`。
6. `api_base` 推荐填写根地址 `https://ark.cn-beijing.volces.com/api/v3`。如果误填成 `/embeddings` 或 `/embeddings/multimodal` 具体接口路径，当前实现会自动裁剪为根地址。
7. YAML 必须使用英文冒号，例如 `dimensions: 1024`，不能写成中文冒号 `dimensions：1024`。

## 六、改造步骤

### Step 1：增加依赖

执行：

```bash
go get github.com/cloudwego/eino-ext/components/embedding/ark@v0.1.2
go mod tidy
```

验收：

```bash
go test ./internal/rag/embedding
```

### Step 2：扩展配置

修改：

```text
internal/config/config.go
internal/config/config_test.go
```

新增字段：

```go
EmbeddingProvider   string
EmbeddingRegion     string
EmbeddingDimensions int
EmbeddingRetryTimes int
```

默认值：

```text
EMBEDDING_PROVIDER=openai_compatible
EMBEDDING_REGION=cn-beijing
EMBEDDING_DIMENSIONS=0
EMBEDDING_RETRY_TIMES=2
```

验收：

1. 不设置新环境变量时，现有 URL 入库行为不变。
2. 设置 `EMBEDDING_PROVIDER=ark` 后，配置能正确读取。
3. 非法 int 使用默认值。

### Step 3：实现 provider factory

新增：

```text
internal/rag/embedding/factory.go
```

核心逻辑：

```go
func NewEmbedder(ctx context.Context, cfg Config) (Embedder, error) {
    switch normalizeProvider(cfg.Provider) {
    case ProviderArk:
        return NewArkEmbedder(ctx, cfg)
    case ProviderOpenAICompatible:
        return NewClient(cfg), nil
    default:
        return nil, fmt.Errorf("unsupported embedding provider: %s", cfg.Provider)
    }
}
```

验收：

1. `ark` 返回 Ark adapter。
2. 空 provider 返回 OpenAI-compatible client。
3. 未知 provider 返回明确错误。

### Step 4：实现 Ark adapter

新增：

```text
internal/rag/embedding/ark_embedder.go
```

实现要点：

1. 校验 `APIKey`、`Model` 非空。
2. `APIBase` 为空时可交给 Ark 默认值，生产建议显式配置。
3. `Region` 为空时使用 `cn-beijing`。
4. `Timeout` 使用 `time.Duration(settings.EmbeddingTimeout) * time.Second`。
5. `RetryTimes > 0` 时传给 Ark。
6. `Dimensions > 0` 时传给 Ark。
7. `EmbedTexts` 对空输入返回空结果。
8. 校验输出数量等于输入数量。
9. 支持 `api_type: text` 和 `api_type: multimodal`。
10. 如果 `api_base` 以 `/embeddings/multimodal` 结尾，自动推断 `api_type=multimodal`，并把 base URL 规范化为根地址。

错误处理：

```text
API key 缺失      -> embedding ark api key is empty
model 缺失        -> embedding ark model is empty
返回数量不一致    -> embedding response count mismatch
返回空向量        -> embedding at position N is empty
```

### Step 5：调整 OpenAI-compatible client

修改：

```text
internal/rag/embedding/client.go
```

建议：

1. `Config` 增加 `Dimensions` 字段。
2. `Dimensions > 0` 时，payload 增加：

```json
{
  "dimensions": 1024
}
```

3. 保持原有 batch、index ordering 和错误处理逻辑。

这样即使未来某些 OpenAI-compatible provider 支持指定维度，也能复用同一配置。

### Step 6：让 `cmd/ingesturls` 使用 factory

修改：

```text
cmd/ingesturls/main.go
```

当前：

```go
func newEmbedder(settings config.Settings) (*embedding.Client, error)
```

改为：

```go
func newEmbedder(ctx context.Context, settings config.Settings) (embedding.Embedder, error)
```

并调用：

```go
return embedding.NewEmbedder(ctx, embedding.Config{
    Provider:   embedding.Provider(settings.EmbeddingProvider),
    APIBase:    apiBase,
    APIKey:     apiKey,
    Model:      settings.EmbeddingModel,
    Region:     settings.EmbeddingRegion,
    APIType:    settings.EmbeddingAPIType,
    Timeout:    time.Duration(settings.EmbeddingTimeout) * time.Second,
    BatchSize:  settings.EmbeddingBatchSize,
    Dimensions: settings.EmbeddingDimensions,
    RetryTimes: settings.EmbeddingRetryTimes,
})
```

保留当前 `apiBase/apiKey` fallback 逻辑，避免破坏已有 `cmd/ingesturls` 使用方式。

### Step 7：可选 smoke 测试

第一版不要把真实 Ark 调用放进默认单元测试。建议新增显式开启的 smoke：

```bash
ARK_EMBEDDING_SMOKE=1 \
EMBEDDING_PROVIDER=ark \
EMBEDDING_API_KEY=... \
EMBEDDING_API_BASE=https://ark.cn-beijing.volces.com/api/v3 \
EMBEDDING_REGION=cn-beijing \
EMBEDDING_API_TYPE=text \
EMBEDDING_MODEL=ep-xxxxxxx-xxxxx \
EMBEDDING_DIMENSIONS=1024 \
go test -run TestArkEmbeddingSmoke ./internal/rag/embedding
```

smoke 验收：

1. 输入一条中文短文本。
2. 返回一条向量。
3. `len(vector) == EMBEDDING_DIMENSIONS`，如果未配置维度则只校验非空。
4. 不打印 API Key。

## 七、测试范围

必须覆盖：

1. 配置默认值。
2. `EMBEDDING_PROVIDER=ark` 配置读取。
3. factory provider 选择。
4. 未知 provider 错误。
5. Ark adapter 缺 key、缺 model 的错误。
6. OpenAI-compatible payload 在 `Dimensions > 0` 时包含 `dimensions`。
7. OpenAI-compatible 旧行为不变。
8. `cmd/ingesturls` 可以拿到 `embedding.Embedder` 接口，而不是具体 `*embedding.Client`。

推荐命令：

```bash
go test ./internal/config ./internal/rag/embedding ./cmd/ingesturls
go test ./...
go test -tags eino ./...
```

## 八、上线与回滚

上线前确认：

1. Ark endpoint 已创建。
2. `EMBEDDING_MODEL` 使用 endpoint ID。
3. `EMBEDDING_DIMENSIONS` 与 pgvector 预期一致。
4. `cmd/ingesturls` 在 `openai_compatible` 模式仍可运行。
5. Ark smoke 通过。

回滚方式：

```bash
EMBEDDING_PROVIDER=openai_compatible
```

或直接清空 `EMBEDDING_PROVIDER`，走默认 OpenAI-compatible client。

如果已经用 Ark 新维度写入了 pgvector，再回滚到其他维度模型时，需要清理或重建 `rag_chunks`，不能混用不同维度向量。

## 九、风险与处理

| 风险 | 影响 | 处理 |
|---|---|---|
| endpoint ID 配错 | embedding 请求失败 | 启动或命令执行时校验 `model` 非空，smoke 验证真实 endpoint |
| 维度不一致 | pgvector 写入失败或检索不可比 | 固定 `EMBEDDING_DIMENSIONS`，维度变化时重建索引 |
| 默认 provider 变化影响现有入库 | URL RAG 回归 | 默认保持 `openai_compatible` |
| Ark 依赖升级引入 API 变化 | 构建失败或运行错误 | 固定 `v0.1.2`，升级单独评估 |
| 真实 API 调用进单元测试 | CI 不稳定、泄露 key 风险 | 真实调用只放显式 smoke，不进默认测试 |
| 多模态和文本 API 混用 | 输入结构和结果不一致 | 用 `api_type` 显式区分；`doubao-embedding-vision-*` 使用 `multimodal` |

## 十、验收标准

1. 新增 Ark 依赖后项目可构建。
2. 默认配置下现有 `cmd/ingesturls` 行为不变。
3. `EMBEDDING_PROVIDER=ark` 时使用 Eino Ark adapter。
4. `EMBEDDING_PROVIDER=openai_compatible` 或空值时使用现有 HTTP client。
5. 配置读取、factory 选择和错误处理有单元测试。
6. 可选 Ark smoke 能返回非空向量，并校验维度。
7. `docs/11` 后续商品详情 RAG 入库可直接复用该 embedding factory。

## 十一、本次实现结果

已实现：

1. `internal/config` 支持读取 `configs/embedding.yaml`，并允许环境变量覆盖。
2. 新增 `EMBEDDING_CONFIG_PATH`，可指向本地私有配置文件，例如 `configs/embedding.local.yaml`。
3. `internal/rag/embedding` 新增 provider factory。
4. `internal/rag/embedding` 新增 Eino Ark adapter。
5. Ark adapter 支持 `api_type: text` 和 `api_type: multimodal`。
6. Ark adapter 支持把 `/embeddings`、`/embeddings/multimodal` endpoint path 规范化为根 `api_base`。
7. OpenAI-compatible client 支持 `dimensions` payload。
8. `cmd/ingesturls` 改为通过统一 factory 创建 embedder。
9. Ark 模式不复用 LLM provider fallback，避免误用其他模型供应商 key。
10. 新增 `configs/embedding.yaml` 安全默认配置。
11. 新增 `configs/embedding.ark.example.yaml` Ark 文本配置模板。
12. 新增 `configs/embedding.ark-vision.example.yaml` Ark 多模态配置模板。

仍需你提供的真实参数：

1. `provider`：填写 `ark`。
2. `model`：Ark 控制台创建的 embedding endpoint ID，例如 `ep-xxxxxxx-xxxxx`。如果直接填写基础模型名返回 `InvalidEndpointOrModel.NotFound`，需要改成 endpoint ID 或确认该 API Key 对该模型有权限。
3. `api_key`：火山方舟 API Key。
4. `api_base`：通常是 `https://ark.cn-beijing.volces.com/api/v3`。
5. `region`：通常是 `cn-beijing`。
6. `api_type`：文本模型填 `text`；`doubao-embedding-vision-*` 填 `multimodal`。
7. `dimensions`：向量维度，例如 `1024`；必须和后续 pgvector 数据维度保持一致。
8. `timeout`：建议先用 `30` 秒。
9. `batch_size`：建议先用 `16`。
10. `retry_times`：建议先用 `2`。

你提供的参数应规范化为：

```yaml
provider: ark
model: doubao-embedding-vision-241215
api_key: ""
api_base: https://ark.cn-beijing.volces.com/api/v3
region: cn-beijing
api_type: multimodal
timeout: 30
batch_size: 16
dimensions: 1024
retry_times: 2
```

说明：`api_key` 请填到本地私有配置或环境变量中，不建议写入可提交文件。

推荐保存方式：

```bash
cp configs/embedding.ark.example.yaml configs/embedding.local.yaml
```

然后填写真实值，并运行时指定：

```bash
EMBEDDING_CONFIG_PATH=configs/embedding.local.yaml go run ./cmd/ingesturls --url https://example.com
```

如需直接使用默认路径，也可以把真实值写入 `configs/embedding.yaml`。生产环境更推荐使用环境变量或 `configs/embedding.local.yaml`，避免把密钥写入可提交文件。
