# Repository Guidelines

## Project Structure & Module Organization

This is a Go 1.25 backend module. Entry points live in `cmd/server` for the HTTP/SSE API and `cmd/ingesturls` for URL ingestion. Core code is under `internal/`: `api` owns routes, `agent/chatflow` owns orchestration, `service` contains workflows, `search` and `rag` cover retrieval, `memory` covers MySQL/Redis state, and `skill/productdetail` handles extraction. Configuration lives in `internal/config` and `configs/llm_providers.yaml`. Tests sit beside source as `*_test.go`. Operational notes are in `docs/`; smoke tooling is in `scripts/`.

## Build, Test, and Development Commands

- `go test ./...`: run the full unit test suite.
- `go test -race ./internal/agent/chatflow ./internal/api ./internal/memory/...`: check concurrency-sensitive areas.
- `go test -tags eino ./...`: validate Eino-tagged paths when touching graph or LLM integration code.
- `HTTP_ADDR=127.0.0.1:34567 ORCHESTRATOR=lite go run ./cmd/server`: start the local API server.
- `python3 scripts/smoke_user_flow.py --start-server`: launch a temporary server and exercise the main user flow.
- `DATABASE_URL=... LLM_API_KEY=... go run ./cmd/ingesturls --url https://example.com`: ingest pages into pgvector-backed RAG storage.

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt` on changed Go files and keep imports clean. Package names should be short, lowercase, and match their directory purpose. Exported identifiers use `PascalCase`; unexported identifiers use `camelCase`. Prefer existing interfaces or config structs before introducing new abstractions. Preserve existing localized user-facing messages where they are already Chinese.

## Testing Guidelines

Use the standard `testing` package with focused, table-driven tests where practical. Name tests `TestXxx` and place them beside the code under test. Mock network, LLM, Redis, MySQL, and pgvector interactions with fakes or local test servers unless the test is explicitly integration-oriented. For cache- or timing-sensitive work, run `go test -count=1 ./...`.

## Commit & Pull Request Guidelines

This workspace does not include `.git` history, so no project-specific convention can be inferred. Use concise imperative subjects such as `Add Redis memory cache tests` and keep unrelated changes separate. PRs should include a summary, test commands run, linked issue or task doc, and notes for API, SSE event, environment variable, schema, or configuration changes. Include sample requests/responses when behavior changes.

## Security & Configuration Tips

Do not commit secrets. Configure credentials and endpoints through environment variables such as `LLM_API_KEY`, `EMBEDDING_API_KEY`, `DATABASE_URL`, `MYSQL_DSN`, and `REDIS_URL`. Keep provider defaults in `configs/llm_providers.yaml` generic and override sensitive values through the environment.
