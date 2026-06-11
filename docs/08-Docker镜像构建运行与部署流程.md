# Docker 镜像构建运行与部署流程

本文档说明如何把当前 Go 后端打包成 Docker 镜像、在本机运行验证，并部署到其他机器。

## 1. 镜像内容

项目根目录的 `Dockerfile` 使用多阶段构建：

- 构建阶段基于 `golang:1.25-alpine` 编译两个二进制：
  - `/app/server`：默认入口，启动 HTTP/SSE API。
  - `/app/ingesturls`：URL 入库命令，可通过覆盖 entrypoint 执行。
- 运行阶段基于 `alpine:3.22`，只保留二进制和 `configs/llm_providers.yaml`。
- 容器默认监听 `0.0.0.0:34567`，健康检查地址为 `/api/healthz`。

## 2. 本机构建

在项目根目录执行：

```bash
docker build -t smartinsure-eino-backend:latest .
```

如需指定版本号：

```bash
docker build -t smartinsure-eino-backend:20260605 .
```

## 3. 本机运行验证

最小验证只需要启动服务并检查健康接口，不要求配置 MySQL、Redis 或 LLM 密钥：

```bash
docker run -d \
  --name smartinsure-eino-backend \
  -p 34567:34567 \
  smartinsure-eino-backend:latest
```

检查容器状态和健康接口：

```bash
docker ps --filter name=smartinsure-eino-backend
curl -fsS http://127.0.0.1:34567/api/healthz
```

如果宿主机 `34567` 已被占用，可以只调整宿主机端口，容器内部端口仍保持 `34567`：

```bash
docker run -d \
  --name smartinsure-eino-backend \
  -p 127.0.0.1:34568:34567 \
  smartinsure-eino-backend:latest
curl -fsS http://127.0.0.1:34568/api/healthz
```

预期返回类似：

```json
{"status":"ok","service":"backend","time":"2026-06-05T00:00:00Z"}
```

查看日志：

```bash
docker logs smartinsure-eino-backend
```

停止并删除测试容器：

```bash
docker rm -f smartinsure-eino-backend
```

## 4. 生产环境变量

生产部署建议使用 env 文件，不要把密钥写入镜像或提交到仓库。

示例 `.env.production`：

```bash
APP_ENV=production
HTTP_ADDR=0.0.0.0:34567
ORCHESTRATOR=lite

LLM_PROVIDER=minimax
MINIMAX_API_KEY=replace-with-secret
MINIMAX_API_BASE=https://api.minimaxi.com/v1
LLM_TIMEOUT=30
LLM_MAX_RETRIES=1

SEARCH_API_KEY=replace-with-secret
SEARCH_API_URL=https://api.search.example.com/v1/search
SEARCH_TIMEOUT=15
SEARCH_TOP_N=10

MYSQL_DSN=smartinsure:password@tcp(mysql-host:3306)/smartinsure?parseTime=true&charset=utf8mb4,utf8
REDIS_URL=redis://redis-host:6379/0

DATABASE_URL=postgres://user:password@postgres-host:5432/smartinsure?sslmode=disable
EMBEDDING_API_KEY=replace-with-secret
EMBEDDING_API_BASE=https://api.openai.com/v1
EMBEDDING_MODEL=text-embedding-3-small
INGEST_NAMESPACE=prod
```

说明：

- `MYSQL_DSN` 为空时，会话存储相关接口不可用，但 `/api/healthz`、`/api/suggestions` 等基础接口仍可启动。
- 配置 `MYSQL_DSN` 后，服务启动时会自动创建 `chat_sessions` 和 `chat_messages` 表。
- `REDIS_URL` 用于短期记忆缓存，可不配置；不配置时走 MySQL 历史记录。
- `DATABASE_URL` 和 `EMBEDDING_*` 主要用于 `/app/ingesturls` 入库任务和 pgvector RAG 数据。
- LLM 提供商也可以改为 `openai`、`deepseek`、`qwen`、`zhipu`、`moonshot` 或 `anthropic`，并按 `configs/llm_providers.yaml` 配置对应密钥环境变量。

使用 env 文件启动：

```bash
docker run -d \
  --name smartinsure-eino-backend \
  --restart unless-stopped \
  --env-file .env.production \
  -p 34567:34567 \
  smartinsure-eino-backend:latest
```

## 5. 执行 URL 入库任务

同一个镜像内包含 `ingesturls` 命令。先保证 `DATABASE_URL` 指向已安装 pgvector 扩展的 PostgreSQL，并配置可用的 Embedding API。

单 URL 入库：

```bash
docker run --rm \
  --env-file .env.production \
  --entrypoint /app/ingesturls \
  smartinsure-eino-backend:latest \
  --url https://example.com
```

批量入库：

```bash
docker run --rm \
  --env-file .env.production \
  -v "$PWD/urls.txt:/app/urls.txt:ro" \
  --entrypoint /app/ingesturls \
  smartinsure-eino-backend:latest \
  --input-file /app/urls.txt \
  --namespace prod
```

`ingesturls` 会在执行时自动创建 `rag_documents` 和 `rag_chunks` 表；PostgreSQL 侧需要允许 `CREATE EXTENSION IF NOT EXISTS vector`。

## 6. 部署到其他机器

方式一：通过镜像仓库发布。

```bash
docker tag smartinsure-eino-backend:latest registry.example.com/smartinsure/smartinsure-eino-backend:20260605
docker push registry.example.com/smartinsure/smartinsure-eino-backend:20260605
```

目标机器拉取并运行：

```bash
docker pull registry.example.com/smartinsure/smartinsure-eino-backend:20260605
docker run -d \
  --name smartinsure-eino-backend \
  --restart unless-stopped \
  --env-file .env.production \
  -p 34567:34567 \
  registry.example.com/smartinsure/smartinsure-eino-backend:20260605
```

方式二：离线导出和导入镜像。

```bash
docker save smartinsure-eino-backend:latest -o smartinsure-eino-backend_latest.tar
scp smartinsure-eino-backend_latest.tar user@target-host:/tmp/
```

目标机器执行：

```bash
docker load -i /tmp/smartinsure-eino-backend_latest.tar
docker run -d \
  --name smartinsure-eino-backend \
  --restart unless-stopped \
  --env-file .env.production \
  -p 34567:34567 \
  smartinsure-eino-backend:latest
```

## 7. 升级流程

```bash
docker build -t smartinsure-eino-backend:20260605 .
docker stop smartinsure-eino-backend
docker rm smartinsure-eino-backend
docker run -d \
  --name smartinsure-eino-backend \
  --restart unless-stopped \
  --env-file .env.production \
  -p 34567:34567 \
  smartinsure-eino-backend:20260605
curl -fsS http://127.0.0.1:34567/api/healthz
```

## 8. 常见问题

- 容器启动后健康检查失败：确认容器内 `HTTP_ADDR` 是否仍为 `0.0.0.0:34567`；如果改了内部端口，需要同步调整端口映射和 Dockerfile 健康检查。
- 调用聊天接口返回 LLM 密钥为空：确认 `LLM_PROVIDER` 与对应密钥环境变量匹配，例如 `LLM_PROVIDER=minimax` 时配置 `MINIMAX_API_KEY`，或统一配置 `LLM_API_KEY`。
- 会话接口返回“会话存储未配置”：配置 `MYSQL_DSN`，并确认目标 MySQL 可从容器网络访问。
- Redis 连接失败：该缓存会自动降级，不影响服务启动；需要短期记忆缓存时再检查 `REDIS_URL` 和网络。
- RAG 入库失败：确认 `DATABASE_URL`、`EMBEDDING_API_KEY`、`EMBEDDING_API_BASE` 可用，并确认 PostgreSQL 用户有创建 pgvector 扩展和建表权限。
