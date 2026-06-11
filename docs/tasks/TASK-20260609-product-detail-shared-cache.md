# TASK-20260609-product-detail-shared-cache

状态：已完成
任务编号：TASK-20260609-product-detail-shared-cache
创建时间：2026-06-09
任务名称：产品详情解析持久化与 Redis 共享缓存后端改造

## 任务描述

根据 `docs/10-产品详情解析持久化与共享缓存改造方案.md`，改造 Go Eino 后端产品详情解析业务流程，使 `product_detail` 和 `product_followup` 支持 L1 进程内缓存、Redis 热缓存、MySQL 持久化共享库的分层命中，并在解析成功后沉淀可跨用户复用的商品详情数据。

本任务只修改后端业务流程，不改变现有 SSE 事件名和前端接口契约。

## Agent 列表

| Agent | 角色 | 负责模块 | 状态 | 进度 |
|---|---|---|---|---:|
| Team Leader | 协调/集成 | 任务拆分、服务主流程集成、任务文档、最终验证 | 完成 | 100% |
| Developer Agent A | 开发 | ProductKeyer、MySQL schema、ProductDetail repository | 完成 | 100% |
| Developer Agent B | 开发 | Redis detail hot cache、alias cache、分布式锁 | 完成 | 100% |
| Verifier Agent | 独立验证 | 集成点审查、风险识别、测试建议 | 完成 | 100% |

## 模块拆分

| 模块 | 路径范围 | 内容 |
|---|---|---|
| 商品标识与持久仓储 | `internal/skill/productdetail` | URL 规范化、`product_key`、MySQL 表结构、详情 upsert/query/touch |
| Redis 热缓存与锁 | `internal/skill/productdetail`、必要时复用 `internal/memory/rediscache` | detail cache、alias cache、compare-and-delete lock |
| 详情服务业务流程 | `internal/skill/productdetail/service.go` | `product_detail` / `product_followup` 接入 L1 -> Redis -> MySQL -> parse |
| 生产接线与配置 | `internal/config`、`internal/agent/chatflow/production.go` | 新增配置项、初始化 MySQL repository 和 Redis hot cache、失败降级 |
| 验证 | `internal/skill/productdetail`、`internal/config`、`internal/agent/chatflow` | 单测、回归测试、降级行为验证 |

## 模块依赖关系

1. 详情服务主流程依赖 ProductKeyer、DetailRepository、DetailHotCache 接口。
2. MySQL repository 与 Redis hot cache 可以并行开发。
3. Production wiring 依赖配置项和可注入接口。
4. Verifier Agent 独立审查，不参与开发实现。

## 执行要求

1. 只改后端业务流程，不改变 SSE 事件名和 payload 字段。
2. MySQL 是最终可信数据源，Redis 只做热缓存和并发锁。
3. Redis 或 MySQL 初始化失败、读写失败时，当前详情解析主链路继续可用。
4. 不保存用户问题、会话历史、健康告知、预算等个人信息到共享商品详情库。
5. 只有通过质量校验的解析结果才写入共享库。
6. `product_followup` 必须能从 Redis/MySQL 命中其他用户已解析的详情。

## 执行步骤

| 步骤 | 状态 | 说明 |
|---|---|---|
| 任务拆分与文档创建 | 完成 | 已按 multi-agent-development 技能创建任务文档并启动子 Agent |
| ProductKeyer 与 MySQL repository | 完成 | Developer Agent A 负责，Team Leader 合并接口 |
| Redis hot cache 与 lock | 完成 | Developer Agent B 负责，Team Leader 合并接口 |
| 服务主流程集成 | 完成 | 已接入 L1 -> Redis -> MySQL -> parse |
| 配置与 Production wiring | 完成 | 已新增配置项并接入生产 detail service options |
| 单元测试与回归测试 | 完成 | `go test ./...`、`go test -tags eino ./...`、race 重点包通过 |
| 任务报告 | 完成 | 已生成 `TASK-20260609-product-detail-shared-cache-report.md` |

## 异常情况

暂无。

## 建议及解决方法

后续可继续实现后台异步刷新、热门商品预解析和指标监控；本阶段只完成后端业务流程闭环。
