# TASK-20260520-eino-graph-orchestrator

## 目标

在保留原 `chatflow.Flow` 编排逻辑的前提下，新增基于 CloudWeGo Eino `compose.Graph` 的主对话流程编排版本，并支持通过环境变量动态切换。

## 切换方式

默认仍使用原流程：

```bash
HTTP_ADDR=127.0.0.1:34567 ORCHESTRATOR=lite go run ./cmd/server
```

启用 Eino Graph 流程：

```bash
HTTP_ADDR=127.0.0.1:34567 ORCHESTRATOR=eino_graph go run ./cmd/server
```

兼容别名：

- `ORCHESTRATOR=eino`
- `ORCHESTRATOR=graph`

如果 Graph 编译失败，生产入口会自动回退到 `lite`，避免服务不可用。

## Graph 节点

```text
START
  -> action_router
      -> product_detail -> END
      -> intent
          -> out_of_scope -> END
          -> followup -> END
          -> search -> answer -> finish -> END
```

节点说明：

| 节点 | 职责 | 输出事件 |
|---|---|---|
| `action_router` | 根据 `action` 分流普通问答或产品详情 | 无 |
| `product_detail` | 复用产品详情 runner | `status`, `detail_items`, `delta`, `done` |
| `intent` | 意图识别 | `status`, `error` |
| `out_of_scope` | 非保险问题拦截 | `delta`, `done` |
| `followup` | 缺槽追问 | `delta`, `done` |
| `search` | 平台产品搜索 + fallback 搜索上下文 | `status`, `products` |
| `answer` | 回答流式生成 | `status`, `delta` |
| `finish` | 输出来源、免责声明和结束事件 | `sources`, `disclaimer`, `done` |

## 链路时序

### 1. 运行时切换时序

```text
cmd/server
  -> api.NewHandler(nil)
  -> chatflow.NewProductionRunner()
      -> 读取 config.Load().Orchestrator
      -> ORCHESTRATOR=lite       => 返回 *Flow
      -> ORCHESTRATOR=eino_graph => NewGraphFlow(NewProduction())
            -> compile compose.Graph
            -> 编译失败则自动回退 *Flow
```

说明：

- API 层只依赖 `chatflow.Runner`，不感知当前是 `lite` 还是 `eino_graph`。
- `lite` 保留原 `Flow.run` 时序。
- `eino_graph` 使用相同底层服务对象：Intent、ProductSearch、Fallback、Answer、Followup、ProductDetail，因此业务结果和 SSE 协议保持一致。

### 2. 普通问答完整时序

以 `{"message":"百万医疗险怎么选？"}` 为例：

```text
T0  /api/chat 收到请求
T1  GraphFlow.Run 创建 events channel 和 graphState
T2  START -> action_router
T3  action_router 判断 action 为空，进入 intent
T4  intent 发送 status(analyzing)
T5  intent 调用 IntentClassifier.Classify
T6  routeIntent 根据 IntentResult 分支
T7  search 发送 status(searching)
T8  search 调用 ProductSearcher.Search
T9  search 如有产品，发送 products
T10 search 调用 FallbackSearcher.Search，写入 state.Results/state.Sources
T11 answer 发送 status(answering)
T12 answer 调用 AnswerStreamer.Stream
T13 answer 将每个 LLM chunk 转成 delta 事件
T14 finish 发送 sources
T15 finish 发送 disclaimer
T16 finish 发送 done
T17 END，关闭 events channel
```

对应 SSE 顺序：

```text
status(analyzing)
status(searching)
products            # 推荐/查询/对比意图才可能出现
status(answering)
delta...
sources
disclaimer
done
```

### 3. 缺槽追问时序

当意图识别结果为 `NeedsFollowup=true`：

```text
START
  -> action_router
  -> intent
      -> status(analyzing)
      -> Classify 得到 missing_slots
      -> routeIntent 进入 followup
  -> followup
      -> FollowupGenerator.Generate
      -> delta
      -> done
  -> END
```

该路径不会进入产品搜索，也不会生成 `sources/disclaimer`，与原 `lite` 流程保持一致。

### 4. 超范围问题时序

当意图识别结果为 `Intent=out_of_scope`：

```text
START
  -> action_router
  -> intent
      -> status(analyzing)
      -> Classify 得到 out_of_scope
      -> routeIntent 进入 out_of_scope
  -> out_of_scope
      -> delta
      -> done
  -> END
```

### 5. 产品详情/追问时序

当请求中 `action=product_detail` 或 `action=product_followup`：

```text
START
  -> action_router
      -> 命中产品详情 action
  -> product_detail
      -> 复用原 flow.runDetail
      -> DetailRunner.Run
      -> 转发产品详情 Skill 的事件
  -> END
```

典型 `product_detail` SSE 顺序：

```text
status(reading/fetching...)
status(extracting...)
detail_items
status(answering...)
delta...
done
```

典型 `product_followup` SSE 顺序：

```text
status...
delta...
done
```

### 6. 状态传递说明

Graph 内部只传递一个 `graphState`：

| 字段 | 写入节点 | 读取节点 | 说明 |
|---|---|---|---|
| `Request` | `GraphFlow.Run` | 全部节点 | 原始 `/api/chat` 请求 |
| `Events` | `GraphFlow.Run` | 全部输出节点 | SSE event channel |
| `Intent` | `intent` | `routeIntent`, `search`, `answer` | 意图、缺槽、追问标记 |
| `Results` | `search` | `answer` | fallback 搜索上下文 |
| `Sources` | `search` | `finish` | 去重后的来源列表 |

这种设计的目标是：让 Eino Graph 负责“节点和分支编排”，让原有服务继续负责“具体业务能力”，从而做到保留原流程且支持动态切换。

## 涉及文件

- `internal/agent/chatflow/graph.go`
- `internal/agent/chatflow/graph_test.go`
- `internal/agent/chatflow/flow.go`
- `internal/agent/chatflow/production.go`
- `internal/api/router.go`

## 验证记录

```bash
go test ./...
go test -race ./internal/agent/chatflow ./internal/api
go test -tags eino ./internal/agent/chatflow ./internal/api
ORCHESTRATOR=eino_graph python3 scripts/smoke_user_flow.py --start-server
```

验证结果：

- 全量 Go 测试通过。
- `chatflow` / `api` race 测试通过。
- `-tags eino` 局部测试通过。
- Eino Graph 模式 smoke 通过，覆盖 healthz、suggestions、knowledge chat、product_detail、product_followup。
