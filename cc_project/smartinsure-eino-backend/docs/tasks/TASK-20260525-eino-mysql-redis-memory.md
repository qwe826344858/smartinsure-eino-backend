# TASK-20260525-eino-mysql-redis-memory

状态：已完成
任务编号：TASK-20260525-eino-mysql-redis-memory
创建时间：2026-05-25
任务名称：Go Eino 后端 MySQL 会话与 Redis 短期记忆阶段开发

## 任务描述

根据 `docs/06-后端MySQL会话与短期记忆需求文档.md`，实现 MySQL 会话/消息持久化、Redis 最近 N 条短期记忆缓存、会话 API、`/api/chat` 落库与短期历史注入，并确保 Lite 编排和 Eino Graph 编排行为一致。

## Agent 列表

| Agent | 角色 | 负责模块 | 状态 | 进度 |
|---|---|---|---|---|
| Team Leader | 协调/集成 | 任务拆分、API 集成、最终验证 | 完成 | 100% |
| Developer Agent A | 开发 | MySQL session/message store | 完成 | 100% |
| Developer Agent B | 开发 | Redis short-term memory cache | 完成 | 100% |
| Developer Agent C | 开发 | chatflow/prompt history injection | 完成 | 100% |
| Verifier Agent | 独立验证 | 代码审查、单测、链路验证 | 完成 | 100% |

## 模块拆分

| 模块 | 路径范围 | 内容 |
|---|---|---|
| MySQL 持久层 | `internal/memory/mysqlstore`、`internal/config` | MySQL DSN、建表、会话/消息 CRUD、匿名最新 session |
| Redis 缓存层 | `internal/memory/rediscache` | `message_ids` List + `messages` Hash、Lua 原子追加/裁剪、回源重建 |
| 历史注入 | `internal/agent/chatflow`、`internal/service/answer`、`internal/service/intent` | `Request.History`、Prompt 历史上下文、Lite/Graph 一致 |
| API 集成 | `internal/api` | 会话接口、`/api/chat` 保存消息、读取历史、SSE 聚合 assistant |
| 验证报告 | `docs/tasks` | 执行结果、异常、验证命令、遗留问题 |

## 模块依赖关系

1. API 集成依赖 MySQL 持久层、Redis 缓存层、chatflow 历史注入。
2. Redis 缓存层可独立开发，MySQL 回源由 API/memory service 串接。
3. Lite Flow 和 Eino Graph 必须复用同一 `chatflow.Request.History`。
4. MySQL 是最终可信数据源，Redis 只做热缓存，可丢失并回源重建。

## 执行要求

1. `MYSQL_DSN` 使用 MySQL 专用连接串，不复用 `DATABASE_URL`。
2. `REDIS_URL` 使用现有配置。
3. 登录用户临时从 `X-User-Id` Header 获取；无 Header 时按 `anonymous_id` 匿名处理。
4. 匿名用户只允许返回和访问最新 session；旧 session 返回 409。
5. assistant 流式期间不写 Redis 正式消息；SSE 完成后 MySQL 写入成功再追加 Redis。
6. 原 `/api/chat` 不带 session 的调用保持兼容。

## 计划验证

1. `go test ./...`
2. `go test -tags eino ./...`
3. 使用 `mysql:5.7` 和 `redis:latest` 进行本地集成验证。
4. 验证 Redis 命中、Redis 清空后 MySQL 回源、匿名旧 session 409。

## 执行进度

| 时间 | 进度 | 说明 |
|---|---:|---|
| 2026-05-25 | 10% | 已完成任务拆分，三个 Developer Agent 并行启动 |
| 2026-05-25 | 40% | MySQL store、Redis cache、chatflow history injection 并行实现完成 |
| 2026-05-25 | 75% | API 会话端点、`/api/chat` 落库、Redis/MySQL 回源和 SSE 聚合集成完成 |
| 2026-05-25 | 95% | Docker MySQL/Redis 临时环境链路验证通过 |
| 2026-05-25 | 100% | 全量测试、Eino tag 测试、race 重点包测试通过，收尾报告已生成 |

## 异常情况

未发现阻断异常。开发中发现 Redis 被清空后如果先追加当前 user 消息再读取缓存，会导致误判缓存命中并漏掉 MySQL 历史；已调整为 user 先落 MySQL，读取 Redis 旧窗口，缓存 miss 时 MySQL 回源并重建，缓存 hit 后再追加当前 user。

## 建议及解决方法

后续如要进入长期记忆阶段，再扩展摘要压缩、结构化长期记忆抽取、向量/关键词混合召回和冲突处理；本阶段只实现短期记忆第一、二阶段。
