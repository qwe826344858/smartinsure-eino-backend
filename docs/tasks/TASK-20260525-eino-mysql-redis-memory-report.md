# TASK-20260525-eino-mysql-redis-memory 收尾报告

状态：已完成
完成时间：2026-05-25

## 任务结果

已按 `docs/06-后端MySQL会话与短期记忆需求文档.md` 完成 Go Eino 后端 MySQL 会话与 Redis 短期记忆阶段开发。

核心能力：

1. MySQL 持久化 `chat_sessions`、`chat_messages`。
2. Redis 使用 `message_ids` List + `messages` Hash 缓存当前 session 最近 N 条消息。
3. 新增会话 API：current session、新建 session、会话列表、会话消息。
4. `/api/chat` 支持 `anonymous_id`、`chat_session_id`，兼容旧 `sessionId`。
5. `/api/chat` 支持 user message 落库、短期历史加载、SSE assistant 聚合落库。
6. Lite Flow 和 Eino Graph 均支持 `Request.History` 短期历史注入。
7. 匿名用户只能访问最新 session；访问旧 session 返回 409。

## 主要变更

| 模块 | 说明 |
|---|---|
| `internal/memory/mysqlstore` | MySQL store、建表、会话/消息 CRUD、匿名最新 session 校验 |
| `internal/memory/rediscache` | Redis cache、Lua 原子追加/裁剪、删除最后一条、回源重建接口 |
| `internal/api` | 会话 API、`/api/chat` 会话集成、SSE 聚合保存、错误码映射 |
| `internal/agent/chatflow` | `Request.History`、`ChatMessage`、Lite/Graph 传递历史 |
| `internal/service/answer` | 回答 Prompt 注入“近期对话上下文” |
| `internal/service/intent` | 意图识别 Prompt 支持可选历史 |
| `internal/config` | 新增 `MYSQL_DSN`、`MEMORY_MESSAGE_LIMIT`、`MEMORY_MAX_CHARS` |
| `internal/schema` | `ChatRequest` 增加 `chat_session_id`、`anonymous_id` |

## 验证结果

已执行：

```bash
go test ./...
go test -tags eino ./...
go test -race ./internal/api ./internal/memory/mysqlstore ./internal/memory/rediscache ./internal/agent/chatflow ./internal/service/answer ./internal/service/intent
```

结果：全部通过。

Docker 临时链路验证：

```text
mysql:5.7
redis:latest
HTTP_ADDR=127.0.0.1:34569
```

验证场景：

1. `POST /api/chat/session/current` 创建匿名当前会话，返回 `chat_session_id`。
2. `/api/chat` 带 `anonymous_id`、`chat_session_id` 调用成功，SSE 返回 `delta/done`。
3. MySQL 中 `chat_sessions`、`chat_messages` 有记录。
4. Redis 中存在 `chat:session:{session_id}:message_ids`。
5. 清空 Redis 当前 session 缓存后，再次聊天能从 MySQL 回源并重建 Redis，`LLEN` 达到 4。
6. 匿名用户新建 session 后，访问旧 session 消息返回 409。

## 异常与处理

发现并修复：

Redis 被清空后，如果先把当前 user 消息写入 Redis，再读取历史，会把只有当前 user 的缓存误判为命中，导致漏掉 MySQL 里的上一轮历史。

修复策略：

```text
user message 先写 MySQL
  -> 读取 Redis 旧窗口
  -> Redis hit：用旧窗口作为 History，再追加当前 user 到 Redis
  -> Redis miss：从 MySQL 读取最近 N 条并重建 Redis，再排除当前 user 后注入 History
```

## 遗留项

1. 当前仍是短期记忆滑动窗口，不包含长期记忆、摘要压缩、RAG、冲突解决。
2. 登录用户身份当前通过 `X-User-Id` Header 模拟，后续接入真实认证后需要替换为认证上下文。
3. Redis latest anonymous session TTL 目前沿用缓存默认 TTL；如需要严格 30 天，可在后续加入独立 TTL 配置。
