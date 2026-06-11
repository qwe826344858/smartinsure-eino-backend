# 后端 MySQL 会话与 Redis 短期记忆需求文档

状态：方案设计中
适用范围：Go Eino 后端、聊天会话、消息存储、短期记忆上下文注入

## 一、背景

前端将引入 `anonymous_id` 与 `chat_session_id` 两个标识，用于让用户刷新页面后恢复上一次聊天，并让后端知道每条消息属于哪个会话。

后端本阶段需要使用 MySQL 存储会话和消息，并使用 Redis 作为当前对话过程中的短期记忆执行缓存。MySQL 是最终可信数据源，Redis 用于避免每轮对话都回表查询最近几轮上下文。

## 二、阶段范围

本次短期记忆只做到第一、二阶段：

| 阶段 | 名称 | 是否实施 | 说明 |
|---|---|---|---|
| 第一阶段 | 会话与消息落库、历史恢复 | 是 | MySQL 保存 session/message，前端可恢复上次聊天 |
| 第二阶段 | 短期上下文注入 | 是 | 后端优先从 Redis 读取最近 N 条消息，缓存未命中时从 MySQL 回源并重建缓存 |
| 第三阶段 | 会话摘要、用户偏好沉淀 | 否 | 暂不做自动摘要、用户画像、长期偏好 |
| 第四阶段 | 长期记忆/RAG/实体追踪 | 否 | 暂不做向量检索、实体状态表、跨会话长期记忆 |

本阶段的核心目标是：同一个 `chat_session_id` 内能恢复历史、能理解最近上下文，不解决跨会话长期记忆问题。

## 三、短期记忆问题与当前解法

参考 Agent Memory 设计中的短期记忆问题，当前阶段主要面对三类风险：

| 问题 | 风险 | 当前解法 |
|---|---|---|
| 对话历史不断增长 | Token 消耗升高，最终超出上下文窗口 | Redis 只保留最近 N 条消息，注入前再按字符数预算裁剪 |
| 每轮都查完整历史 | MySQL 压力增加，接口延迟升高 | Redis 作为当前 session 的热缓存，MySQL 只做持久层和缓存回源 |
| 历史消息噪音变多 | LLM 被无关上下文干扰 | 当前阶段仅使用滑动窗口，不做长期召回，控制上下文规模 |

因此本阶段采用的是“滑动窗口短期记忆”：

```text
最近 N 条完整消息
  + Redis 热缓存
  + MySQL 持久化兜底
  + 字符数预算裁剪
```

暂不采用文章中提到的摘要压缩、结构化长期记忆抽取、向量检索、冲突解决。后续如果最近 N 条仍然过长，再升级为分层压缩：

```text
最近 5 轮完整消息
  + 更早 15 轮摘要
  + 结构化长期记忆
```

## 四、身份与会话关系

### 4.1 登录用户

```text
user_id 1 -> N chat_session_id
chat_session_id 1 -> N chat_messages
```

登录用户可以拥有多个会话。后端可以向登录用户返回会话列表，前端后续可做多会话切换。

### 4.2 匿名用户

```text
anonymous_id 1 -> N chat_session_id
但接口永远只返回最新 1 个 chat_session_id
```

匿名用户规则：

1. 匿名用户可在 MySQL 中保留多条历史 session。
2. 对前端查询时，匿名用户只允许看到 `updated_at` 最新的一条 session。
3. 匿名用户即使传入旧 `chat_session_id`，后端也必须判断它是否为该匿名用户最新 session。
4. 如果不是最新 session，后端不返回旧消息。
5. 未来用户登录后，可以将匿名 session 合并到 `user_id` 下。

## 五、MySQL 表设计

### 5.1 chat_sessions

```sql
CREATE TABLE chat_sessions (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  user_id VARCHAR(64) NULL,
  anonymous_id VARCHAR(64) NULL,
  title VARCHAR(128) NULL,
  status VARCHAR(20) NOT NULL DEFAULT 'active',
  metadata JSON NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  last_message_at DATETIME(3) NULL,
  KEY idx_chat_sessions_user_updated (user_id, updated_at),
  KEY idx_chat_sessions_anon_updated (anonymous_id, updated_at),
  KEY idx_chat_sessions_last_message (last_message_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

字段说明：

| 字段 | 说明 |
|---|---|
| `id` | 会话 ID，对外返回为 `chat_session_id` |
| `user_id` | 登录用户 ID，匿名用户为空 |
| `anonymous_id` | 匿名用户 ID，登录用户可为空或保留绑定来源 |
| `title` | 会话标题，默认可取首条用户消息前 20 字 |
| `status` | `active` / `deleted` |
| `metadata` | 当前会话临时状态，如最近产品 URL、最近险种 |
| `last_message_at` | 最近一条消息时间，用于排序 |

约束要求：

1. `user_id` 与 `anonymous_id` 至少有一个不为空。
2. 登录态优先使用 `user_id` 做归属判断。
3. 匿名态使用 `anonymous_id` 做弱归属判断。

### 5.2 chat_messages

```sql
CREATE TABLE chat_messages (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  session_id VARCHAR(64) NOT NULL,
  role VARCHAR(20) NOT NULL,
  content TEXT NOT NULL,
  metadata JSON NULL,
  created_at DATETIME(3) NOT NULL,
  KEY idx_chat_messages_session_created (session_id, created_at),
  CONSTRAINT fk_chat_messages_session
    FOREIGN KEY (session_id) REFERENCES chat_sessions(id)
    ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

字段说明：

| 字段 | 说明 |
|---|---|
| `id` | 消息 ID |
| `session_id` | 关联 `chat_sessions.id` |
| `role` | `user` / `assistant` / `system` |
| `content` | 消息文本 |
| `metadata` | 产品卡片、来源、免责声明、错误信息等结构化数据 |
| `created_at` | 消息创建时间 |

## 六、Redis 执行缓存设计

### 6.1 分层原则

```text
MySQL：最终持久层，保存完整 session/message
Redis：执行缓存层，保存当前活跃 session 的最近 N 条消息和轻量会话状态
```

设计原则：

1. MySQL 仍是唯一可信存储，Redis 数据可丢失、可重建。
2. `/api/chat` 读取短期记忆时优先查 Redis。
3. Redis 未命中、过期或不可用时，从 MySQL 查询最近 N 条消息并回填 Redis。
4. 用户消息和 assistant 消息必须先保证 MySQL 写入，再更新 Redis。
5. Redis 故障不应导致聊天主链路不可用，应降级为 MySQL 查询。

### 6.2 Redis Key 设计

| Key | 类型 | 示例 | 说明 |
|---|---|---|---|
| `chat:session:{session_id}:message_ids` | List | `chat:session:chat_xxx:message_ids` | 当前 session 最近 N 条消息 ID，按时间正序排列 |
| `chat:session:{session_id}:messages` | Hash | `chat:session:chat_xxx:messages` | 消息详情，field 为 `message_id`，value 为消息 JSON |
| `chat:session:{session_id}:state` | String/Hash | `chat:session:chat_xxx:state` | 当前 session 的轻量状态，如最近产品 |
| `chat:anon:{anonymous_id}:latest_session` | String | `chat:anon:anon_xxx:latest_session` | 匿名用户最新 session |
| `chat:session:{session_id}:owner` | Hash | `chat:session:chat_xxx:owner` | session 归属缓存，包含 `user_id` / `anonymous_id` |

消息 ID List 示例：

```text
chat:session:chat_xxx:message_ids
  -> msg_1
  -> msg_2
  -> msg_3
```

消息 Hash 示例：

```json
{
  "msg_1": "{\"id\":\"msg_1\",\"role\":\"user\",\"content\":\"百万医疗险怎么选？\",\"metadata\":{},\"created_at\":\"2026-05-22T10:01:00+08:00\"}",
  "msg_2": "{\"id\":\"msg_2\",\"role\":\"assistant\",\"content\":\"选择百万医疗险时建议重点看续保条件。\",\"metadata\":{},\"created_at\":\"2026-05-22T10:01:03+08:00\"}"
}
```

采用 `List + Hash` 的原因：

1. List 只负责顺序和窗口裁剪。
2. Hash 负责按 `message_id` 定位消息详情。
3. 删除最后一条时可以先校验最后一个 `message_id`，再删除对应 Hash 字段。
4. 后续如果某条消息需要补充 `metadata`，可以只更新 Hash 中该条消息。

### 6.3 写入策略

用户消息：

```text
收到 /api/chat
  -> 校验 session 归属
  -> 写入 MySQL chat_messages
  -> HSET chat:session:{session_id}:messages {message_id} {message_json}
  -> RPUSH chat:session:{session_id}:message_ids {message_id}
  -> 裁剪 message_ids 到最近 N 条，并删除被裁剪 message_id 对应的 Hash 字段
  -> EXPIRE chat:session:{session_id}:message_ids
  -> EXPIRE chat:session:{session_id}:messages
```

assistant 消息：

```text
SSE 完成
  -> 聚合 assistant 完整回复和 metadata
  -> 写入 MySQL chat_messages
  -> HSET chat:session:{session_id}:messages {message_id} {message_json}
  -> RPUSH chat:session:{session_id}:message_ids {message_id}
  -> 裁剪 message_ids 到最近 N 条，并删除被裁剪 message_id 对应的 Hash 字段
  -> EXPIRE chat:session:{session_id}:message_ids
  -> EXPIRE chat:session:{session_id}:messages
```

推荐用 Lua 脚本把 `HSET + RPUSH + 裁剪 + HDEL + EXPIRE` 做成一个原子操作，避免并发请求时 List 和 Hash 不一致。

追加消息并裁剪的逻辑：

```text
1. HSET messages message_id message_json
2. RPUSH message_ids message_id
3. 如果 LLEN(message_ids) > N：
   - LPOP 最早的 message_id
   - HDEL messages old_message_id
4. 对 message_ids 和 messages 设置相同 TTL
```

assistant 流式期间不要写入 Redis 正式消息。只有 SSE 完成、assistant 完整内容写入 MySQL 成功后，才追加到 Redis。这样正常情况下不需要删除半截 assistant 消息。

### 6.4 删除最后一条消息

删除最后一条只用于异常恢复场景，例如某条消息已经写入 Redis，但后续流程确认需要回滚缓存。生产实现不应直接盲删最后一条，而是必须带上预期的 `message_id` 做校验。

原子删除逻辑：

```text
1. LINDEX message_ids -1 获取最后一个 message_id
2. 如果 last_message_id == expected_message_id：
   - RPOP message_ids
   - HDEL messages expected_message_id
   - 返回删除成功
3. 如果不相等，说明有并发新消息进入，不删除
```

对应 Lua 伪代码：

```lua
local idsKey = KEYS[1]
local messagesKey = KEYS[2]
local expectedID = ARGV[1]

local lastID = redis.call("LINDEX", idsKey, -1)
if lastID == expectedID then
  redis.call("RPOP", idsKey)
  redis.call("HDEL", messagesKey, expectedID)
  return 1
end

return 0
```

### 6.5 读取策略

短期记忆读取流程：

```text
memory service 加载历史
  -> LRANGE chat:session:{session_id}:message_ids 0 -1
  -> HMGET chat:session:{session_id}:messages {message_ids...}
  -> 命中且消息完整：直接使用 Redis 结果
  -> 未命中或为空：查询 MySQL 最近 N 条
  -> 将 MySQL 结果按时间正序回填 Redis 的 message_ids 和 messages
  -> 执行字符数裁剪
  -> 注入 Agent / Eino 编排
```

MySQL 回源查询：

```sql
SELECT id, role, content, metadata, created_at
FROM chat_messages
WHERE session_id = ?
ORDER BY created_at DESC
LIMIT ?;
```

Go 代码中需要将 MySQL 倒序结果反转为正序后再写入 Redis 和传入 LLM。Redis 回填也应使用同一个原子追加脚本，确保 List 与 Hash 一致。

### 6.6 TTL 建议

| Key | TTL | 说明 |
|---|---|---|
| `chat:session:{session_id}:message_ids` | 7 天滑动过期 | 活跃会话最近消息 ID |
| `chat:session:{session_id}:messages` | 7 天滑动过期 | 活跃会话短期记忆缓存 |
| `chat:session:{session_id}:state` | 7 天滑动过期 | 最近产品、最近意图等轻量状态 |
| `chat:anon:{anonymous_id}:latest_session` | 30 天滑动过期 | 匿名用户最新 session 快速定位 |
| `chat:session:{session_id}:owner` | 30 天滑动过期 | 归属校验缓存 |

TTL 过期不影响历史恢复，因为 MySQL 中保留完整消息。

### 6.7 Redis 降级规则

| 场景 | 处理 |
|---|---|
| Redis 读取失败 | 记录日志，降级查询 MySQL 最近 N 条 |
| Redis 写入失败 | 记录日志，不影响 MySQL 写入后的主链路 |
| Redis 数据为空 | MySQL 回源并重建缓存 |
| Redis message_ids 和 messages 不一致 | 删除两类 key，MySQL 回源并重建缓存 |
| Redis 数据解析失败 | 删除 `message_ids` 和 `messages`，MySQL 回源并重建缓存 |
| Redis latest session 缺失 | MySQL 查询匿名用户最新 session，再回填 Redis |

Redis 只能提升性能，不能成为唯一消息来源。

## 七、接口设计

### 7.1 创建或获取当前会话

```http
POST /api/chat/session/current
Content-Type: application/json
```

匿名用户请求：

```json
{
  "anonymous_id": "anon_xxx"
}
```

登录用户请求：

```json
{
  "anonymous_id": "anon_xxx"
}
```

登录用户的 `user_id` 不应依赖请求体，必须从认证上下文获取。

响应：

```json
{
  "chat_session_id": "chat_xxx",
  "title": "新会话",
  "created_at": "2026-05-22T10:00:00+08:00",
  "updated_at": "2026-05-22T10:00:00+08:00"
}
```

处理规则：

1. 登录用户：返回该用户最近活跃 session；如不存在则创建。
2. 匿名用户：返回该 `anonymous_id` 最近活跃 session；如不存在则创建。
3. 创建 session 时，`title` 默认为 `新会话`，首条用户消息写入后再更新标题。

### 7.2 新建会话

```http
POST /api/chat/sessions
Content-Type: application/json
```

请求：

```json
{
  "anonymous_id": "anon_xxx"
}
```

规则：

1. 登录用户可以创建多个 session。
2. 匿名用户也可以创建新的 session，但后续接口只返回最新 session。
3. 新 session 创建后，应成为该匿名用户的当前最新 session。

### 7.3 获取会话列表

```http
GET /api/chat/sessions?anonymous_id=anon_xxx
```

登录用户响应：

```json
{
  "sessions": [
    {
      "chat_session_id": "chat_1",
      "title": "百万医疗险咨询",
      "updated_at": "2026-05-22T10:10:00+08:00",
      "last_message_at": "2026-05-22T10:09:58+08:00"
    },
    {
      "chat_session_id": "chat_2",
      "title": "重疾险咨询",
      "updated_at": "2026-05-21T19:00:00+08:00",
      "last_message_at": "2026-05-21T18:59:40+08:00"
    }
  ]
}
```

匿名用户响应：

```json
{
  "sessions": [
    {
      "chat_session_id": "chat_latest",
      "title": "最近一次咨询",
      "updated_at": "2026-05-22T10:10:00+08:00",
      "last_message_at": "2026-05-22T10:09:58+08:00"
    }
  ]
}
```

匿名用户永远只返回最新一条 session。

### 7.4 获取会话消息

```http
GET /api/chat/sessions/{chat_session_id}/messages?anonymous_id=anon_xxx&limit=50
```

响应：

```json
{
  "chat_session_id": "chat_xxx",
  "messages": [
    {
      "id": "msg_1",
      "role": "user",
      "content": "百万医疗险怎么选？",
      "created_at": "2026-05-22T10:01:00+08:00"
    },
    {
      "id": "msg_2",
      "role": "assistant",
      "content": "选择百万医疗险时，建议重点看保障责任、免赔额、续保条件和健康告知。",
      "created_at": "2026-05-22T10:01:03+08:00",
      "metadata": {
        "products": [],
        "sources": [],
        "disclaimer": ""
      }
    }
  ]
}
```

归属校验：

1. 登录用户：只能读取 `session.user_id == current_user_id` 的消息。
2. 匿名用户：只能读取 `session.anonymous_id == anonymous_id` 的消息。
3. 匿名用户还必须满足：该 session 是此 `anonymous_id` 最新的 session。

### 7.5 聊天接口

```http
POST /api/chat
Content-Type: application/json
```

请求：

```json
{
  "anonymous_id": "anon_xxx",
  "chat_session_id": "chat_xxx",
  "message": "百万医疗险怎么选？"
}
```

响应仍使用 SSE。

处理顺序：

```text
1. 解析身份
2. 校验 chat_session_id 归属
3. 如果匿名用户传入的不是最新 session，拒绝或切换到最新 session
4. 保存 user message 到 MySQL，并同步追加到 Redis 最近消息缓存
5. 优先从 Redis 加载该 session 最近 N 条消息，缓存未命中时从 MySQL 回源
6. 组装短期记忆上下文
7. 调用 Eino / Lite 编排
8. SSE 流式返回 status / products / delta / done
9. 聚合 assistant 完整回复
10. 保存 assistant message 和 metadata 到 MySQL，并同步追加到 Redis 最近消息缓存
11. 更新 chat_sessions.title / updated_at / last_message_at / metadata
```

建议匿名旧 session 的处理策略：

| 策略 | 说明 | 建议 |
|---|---|---|
| 拒绝请求 | 返回 409，要求前端重新获取 current session | 推荐 |
| 自动切换 | 后端忽略旧 session，写入最新 session | 不推荐，容易让前端状态混乱 |

## 八、短期记忆设计

### 8.1 第一阶段：会话历史落库恢复

目标：

1. 用户消息实时写入 MySQL。
2. assistant 消息在 SSE 完成后写入 MySQL。
3. 页面刷新后，前端可以通过 `chat_session_id` 拉取历史消息。
4. 后端会话列表可以返回登录用户的多个 session。
5. 匿名用户只返回最新 session。

第一阶段不要求 LLM 理解历史上下文，只保证历史可查、可恢复。

### 8.2 第二阶段：最近上下文注入

目标：

1. `/api/chat` 执行 Agent 前，优先从 Redis 读取当前 session 最近 N 条消息。
2. Redis 未命中时，从 MySQL 读取最近 N 条消息并回填 Redis。
3. 将最近消息注入到意图识别、问题改写、回答生成链路中。
4. 支持用户追问，例如：

```text
用户：百万医疗险怎么选？
助手：...
用户：那刚才推荐的第一个适合我爸吗？
```

后端需要能把第二句和同一 session 内的上一轮推荐关联起来。

建议默认窗口：

| 参数 | 默认值 | 说明 |
|---|---|---|
| `memory_message_limit` | 20 | 最近 20 条消息 |
| `memory_max_chars` | 12000 | 注入 LLM 的最大字符数 |
| `memory_role_scope` | user + assistant | 注入用户和助手消息 |

裁剪策略：

```text
1. 优先从 Redis message_ids 读取已按时间正序保存的最近 N 条消息
2. Redis 未命中时，MySQL 按 created_at 倒序取最近 N 条，再反转为正序
3. 如果字符数超过上限，从最早消息开始裁剪
4. 永远保留当前用户问题
5. 裁剪只影响本次 Prompt 注入，不删除 MySQL 完整历史
```

这对应 Agent Memory 文章里的“滑动窗口”方案：实现简单、延迟低、可控，但会丢弃窗口外上下文。因此本阶段需要明确它只解决当前 session 内最近追问，不承诺跨会话长期记忆。

### 8.3 会话 metadata

为了增强短期追问效果，`chat_sessions.metadata` 可保存轻量状态：

```json
{
  "last_intent": "product_recommendation",
  "last_insurance_type": "medical",
  "last_product_name": "某某百万医疗险",
  "last_product_url": "https://example.com/product",
  "last_product_platform": "alipay"
}
```

本阶段只保存当前 session 的最近状态，不做跨 session 用户画像。

## 九、Eino 编排接入点

后端请求模型需要扩展：

```go
type Request struct {
    Message       string
    RequestID     string
    AnonymousID   string
    UserID        string
    ChatSessionID string
    History       []ChatMessage
}
```

编排前置步骤：

```text
HTTP Handler
  -> session service 校验或创建会话
  -> message store 写入 user message 到 MySQL
  -> memory cache 追加 user message 到 Redis
  -> memory service 优先从 Redis 加载最近历史，必要时 MySQL 回源
  -> chatflow.Runner.Run(ctx, Request{History: history})
  -> 读取 SSE events
  -> 聚合 assistant 回复并写入 MySQL
  -> memory cache 追加 assistant message 到 Redis
```

Graph / Lite 两套编排都应复用同一个 `Request.History`，避免动态切换编排后记忆表现不一致。

## 十、错误码约定

| 场景 | 状态码 | 说明 |
|---|---|---|
| 缺少 `anonymous_id` 且未登录 | 400 | 前端需要先初始化匿名身份 |
| `chat_session_id` 不存在 | 404 | 前端应重新获取 current session |
| 会话不属于当前用户或匿名 ID | 403 | 禁止读取或写入 |
| 匿名用户访问旧 session | 409 | 前端应重新调用 current session |
| MySQL 写入失败 | 500 | 返回错误事件，避免静默丢消息 |
| Redis 不可用 | 不直接暴露 | 后端降级 MySQL 查询，并记录日志 |

## 十一、开发任务拆分

| 任务 | 内容 | 优先级 |
|---|---|---|
| BE-SESSION-01 | 增加 MySQL 配置与连接池 | P0 |
| BE-SESSION-02 | 增加 `chat_sessions`、`chat_messages` 建表脚本 | P0 |
| BE-SESSION-03 | 实现 session store：创建、获取当前、列表、归属校验 | P0 |
| BE-SESSION-04 | 实现 message store：写入、按 session 查询最近消息 | P0 |
| BE-SESSION-05 | `/api/chat` 接入 `anonymous_id`、`chat_session_id` | P0 |
| BE-SESSION-06 | SSE 完成后聚合并保存 assistant 消息 | P0 |
| BE-CACHE-01 | 增加 Redis 配置与连接池 | P0 |
| BE-CACHE-02 | 实现 Redis `message_ids` List + `messages` Hash 最近消息缓存 | P0 |
| BE-CACHE-03 | 实现 Redis miss 后 MySQL 回源和缓存重建 | P0 |
| BE-CACHE-04 | 实现匿名用户 latest session 缓存 | P1 |
| BE-CACHE-05 | 实现 Redis 追加、裁剪、删除最后一条的 Lua 原子脚本 | P1 |
| BE-MEMORY-01 | 实现 Redis 优先的最近 N 条消息加载与字符数裁剪 | P1 |
| BE-MEMORY-02 | 将短期历史传入 Lite 编排 | P1 |
| BE-MEMORY-03 | 将短期历史传入 Eino Graph 编排 | P1 |
| BE-MEMORY-04 | 在 prompt 中加入最近上下文 | P1 |
| BE-SESSION-07 | 匿名用户只返回最新 session 的校验与测试 | P1 |

## 十二、验收标准

1. MySQL 中能看到 `chat_sessions` 和 `chat_messages` 数据。
2. 用户发送一条消息后，MySQL 立即保存 user message。
3. assistant SSE 完成后，MySQL 保存完整 assistant message。
4. Redis 中能看到当前 session 的 `message_ids` List 和 `messages` Hash。
5. Redis 命中时，短期记忆不需要查询 MySQL 消息表。
6. Redis 缓存清空后，后端能从 MySQL 回源并重建最近消息缓存。
7. 刷新页面后，前端通过 `chat_session_id` 能恢复历史消息。
8. 登录用户可以获取多个会话。
9. 匿名用户只能获取最新一个会话。
10. 匿名用户传旧 `chat_session_id` 查询消息时，后端返回 409 或拒绝。
11. 用户在同一会话内追问时，Agent 能参考最近历史回答。
12. Lite 编排和 Eino Graph 编排都支持同样的短期记忆。
13. 最近消息超过 `memory_message_limit` 后，Redis 只保留窗口内消息，MySQL 仍保留完整历史。
14. assistant 流式失败时，不会把半截 assistant 消息写入 Redis 正式记忆。
15. Redis `message_ids` 和 `messages` 不一致时，能删除缓存并从 MySQL 重建。

## 十三、暂不实施项

1. 不做长期记忆。
2. 不做 RAG 历史检索。
3. 不做实体状态追踪。
4. 不做跨设备同步。
5. 不做用户画像沉淀。
6. 不做历史消息自动摘要。
7. 不做匿名用户全部历史会话列表返回。
