# Repository Guidelines

## Project Structure & Module Organization

This is a Go 1.25 backend module for the SmartInsure insurance advisor. Entry points live in `cmd/server` for the HTTP/SSE API and `cmd/ingesturls` for URL ingestion into pgvector-backed RAG storage. Core code is under `internal/`: `api` owns routes, session handlers, and SSE response wiring; `agent/chatflow` is the baseline orchestration flow; `agent/runtime` provides the agent registry, request protocol, and trace metadata; `agent/smartinsureagent` implements the Plan-Act advisor; `agent/smartinsuredeep` adapts Eino DeepAgent.

Domain services live in `internal/service` (`intent`, `answer`, `followup`, `productsearch`). Retrieval and ingestion live in `internal/search` and `internal/rag`. State lives in `internal/memory/mysqlstore` and `internal/memory/rediscache`. Product parsing and extraction live in `internal/platform`, `internal/search/parsers`, and `internal/skill/productdetail`. Shared contracts are in `internal/schema`, `internal/stream`, `internal/errors`, `internal/compliance`, `internal/config`, and `internal/llm`. Provider routing is configured in `configs/llm_providers.yaml`; operational docs and task reports are in `docs/`; smoke tooling is in `scripts/`. Tests sit beside source as `*_test.go`.

## Build, Test, and Development Commands

- `go test ./...`: run the full unit test suite.
- `go test -count=1 ./...`: rerun cache- or timing-sensitive tests without test cache.
- `go test -race ./internal/agent/... ./internal/api ./internal/memory/...`: check concurrency-sensitive agent, API, and memory paths.
- `go test -tags eino ./...`: compatibility check for Eino-tagged invocations; current code has no active `//go:build eino` files, so do not rely on this tag to enable hidden source.
- `HTTP_ADDR=127.0.0.1:34567 ORCHESTRATOR=lite go run ./cmd/server`: start the local API server with the default flow.
- `HTTP_ADDR=127.0.0.1:34567 ORCHESTRATOR=eino_graph go run ./cmd/server`: start the same API with the Eino Graph chatflow runner.
- `python3 scripts/smoke_user_flow.py --start-server`: launch a temporary server and exercise health, suggestions, baseline chat, product detail, and product follow-up over `/api/chat`.
- `DATABASE_URL=... EMBEDDING_API_KEY=... EMBEDDING_API_BASE=... go run ./cmd/ingesturls --url https://example.com --namespace default`: ingest pages into pgvector-backed RAG storage.
- `docker build -t smartinsure-eino-backend .`: build the production image containing both `server` and `ingesturls`.
- `scripts/restart_backend_container_with_binary.sh`: rebuild `./cmd/server`, mount the binary into the Docker container, restart `smartinsure-eino-backend`, and wait for `/api/healthz`; defaults are `HOST_PORT=34567`, `CONTAINER_PORT=34567`, `IMAGE=smartinsure-eino-backend:latest`, and `CONTAINER_NAME=smartinsure-eino-backend`. Pass overrides as env vars, and pass `LLM_API_KEY=...` explicitly when the container relies on the global LLM key because the script does not copy it from the old container.

## API and Agent Notes

The server exposes `GET /api/healthz`, `GET /api/suggestions`, `GET /api/providers`, session endpoints under `/api/chat/session*`, legacy SSE chat at `POST /api/chat`, Plan-Act AgentRuntime chat at `POST /api/agent/chat`, and Eino DeepAgent chat at `POST /api/agent/deep-chat`. Chat responses are Server-Sent Events using the event names `status`, `delta`, `products`, `sources`, `detail_items`, `disclaimer`, `error`, and `done`; keep these names and payload fields stable unless coordinating a frontend change.

`/api/agent/chat` defaults to agent id `smartinsure-advisor` and can be overridden by `agent_id` or `AGENT_DEFAULT_ID`. `/api/agent/deep-chat` ignores request `agent_id` and always runs `smartinsure-deep-advisor`. AgentRuntime injects `requestId`, `request_id`, `agent_id`, and, when `AGENT_TRACE_ENABLED=true`, `trace_id` into SSE data. `action=product_detail` and `action=product_followup` are direct product-detail flows; normal messages go through intent, search/tool execution, answer streaming, sources, disclaimer, and done.

Conversation persistence is enabled only when `MYSQL_DSN` is configured. Redis is an optional short-term cache controlled by `REDIS_URL`; failed Redis setup must not prevent MySQL-backed sessions. Session identity comes from `X-User-Id` or `anonymous_id`; anonymous users may only access their latest session. Requests without a session id or identity should continue to work as stateless chat.

## Configuration

Use `internal/config.Load` as the source of truth for environment variables. LLM provider routing is stage-based (`intent`, `query`, `answer`, `followup`, `detail`, `agent_planner`, `deep_agent`) and is defined in `configs/llm_providers.yaml`. Provider-specific keys such as `MINIMAX_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `QWEN_API_KEY`, `ZHIPU_API_KEY`, `MOONSHOT_API_KEY`, and `ANTHROPIC_API_KEY` may be overridden globally with `LLM_API_KEY`; likewise `LLM_API_BASE` and `LLM_MODEL` override provider defaults.

Important runtime variables include `ORCHESTRATOR`, `AGENT_CHAT_ENABLED`, `AGENT_MODE`, `AGENT_MAX_ITERATIONS`, `AGENT_TOOL_TIMEOUT`, `AGENT_ACTION_REPAIR_ENABLED`, `AGENT_MEMORY_WINDOW`, `AGENT_SCRATCHPAD_MAX_CHARS`, `AGENT_OBSERVATION_MAX_CHARS`, `SEARCH_API_KEY`, `SEARCH_API_URL`, `MCP_SEARCH_URL`, `MCP_SEARCH_ENGINES`, `MYSQL_DSN`, `REDIS_URL`, `DATABASE_URL`, `EMBEDDING_*`, and `INGEST_*`. Do not commit secrets; keep defaults generic and inject credentials through the environment.

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt` on changed Go files and keep imports clean. Package names should be short, lowercase, and match their directory purpose. Exported identifiers use `PascalCase`; unexported identifiers use `camelCase`. Prefer existing interfaces, config structs, and adapter patterns before introducing new abstractions.

Preserve existing localized user-facing messages where they are already Chinese. Do not rename SSE events, JSON fields, agent ids, action names, or environment variables casually; these are frontend and deployment contracts. Keep compliance sanitization in answer streams, validate agent action inputs on the backend, and avoid exposing planner scratchpad or raw chain-of-thought through API payloads.

## Testing Guidelines

Use the standard `testing` package with focused, table-driven tests where practical. Name tests `TestXxx` and place them beside the code under test. Mock network, LLM, Redis, MySQL, pgvector, and external product pages with fakes or local test servers unless the test is explicitly integration-oriented.

When changing routes or SSE payloads, update `internal/api` tests and smoke expectations. When changing agent planning, action validation, or tools, cover `internal/agent/runtime`, `internal/agent/smartinsureagent`, and `internal/agent/smartinsuredeep` as appropriate. For memory changes, test both MySQL store behavior and Redis key/script behavior. For ingestion or vector storage changes, keep SQL-building tests independent from a live database where possible.

## Commit & Pull Request Guidelines

This workspace does not include usable `.git` history, so no project-specific convention can be inferred. Use concise imperative subjects such as `Add Redis memory cache tests` and keep unrelated changes separate. PRs should include a summary, test commands run, linked issue or task doc, and notes for API, SSE event, environment variable, schema, configuration, storage, or deployment changes. Include sample requests/responses when behavior changes.
