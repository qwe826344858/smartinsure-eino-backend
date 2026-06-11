# TASK-20260609-product-detail-shared-cache-report

状态：已完成
任务编号：TASK-20260609-product-detail-shared-cache
完成时间：2026-06-09
任务名称：产品详情解析持久化与 Redis 共享缓存后端改造

## 执行结果

已根据 `docs/10-产品详情解析持久化与共享缓存改造方案.md` 完成后端业务流程改造。

核心结果：

1. `product_detail` 支持 L1 进程内缓存、Redis 热缓存、MySQL 持久化库分层命中。
2. `product_followup` 不再只依赖进程内缓存，可复用 Redis/MySQL 中其他用户已解析的详情。
3. 解析成功后先写 MySQL，再写 Redis 和 L1；MySQL 写失败时当前用户仍可得到回答，但不写 Redis 共享缓存。
4. Redis lock 使用 owner token 和 Lua compare-and-delete 释放，避免误删其他请求锁。
5. Redis/MySQL 读写失败均按降级处理，不改变 SSE 事件名和前端接口契约。

## Agent 执行情况

| Agent | 模块 | 结果 |
|---|---|---|
| Developer Agent A | ProductKeyer、MySQL repository | 完成 URL 规范化、`product_key`、MySQL schema、upsert/query/touch |
| Developer Agent B | Redis hot cache、alias、lock | 完成 Redis detail envelope、alias cache、分布式锁 |
| Verifier Agent | 独立验证 | 输出 service 接入顺序、production wiring 风险、SSE 顺序和测试建议 |
| Team Leader | 集成与验证 | 合并接口、接入 `productdetail.Service`、接入配置和 production wiring、补充测试 |

## 主要改动

| 文件 | 内容 |
|---|---|
| `internal/skill/productdetail/keyer.go` | 新增 URL normalize、平台识别、URL hash、`product_key` 生成 |
| `internal/skill/productdetail/repository.go` | 新增共享详情仓储、Redis hot cache、lock 接口和数据结构 |
| `internal/skill/productdetail/mysql_repository.go` | 新增 MySQL 表结构、repository 实现、upsert/query/touch |
| `internal/skill/productdetail/redis_hot_cache.go` | 新增 Redis detail cache、alias cache、lock 和 envelope 编解码 |
| `internal/skill/productdetail/service.go` | 接入 L1 -> Redis -> MySQL -> parse，支持 `product_followup` 共享命中 |
| `internal/agent/chatflow/production.go` | 接入共享缓存配置、MySQL repository、Redis hot cache，复用 production deps |
| `internal/config/config.go` | 新增 product detail 共享缓存配置项 |
| `internal/skill/productdetail/*_test.go` | 补充 keyer、repository、Redis hot cache、service 命中和降级测试 |
| `internal/config/config_test.go` | 覆盖新增配置项 |

## 后端业务流程

`product_detail`：

```text
请求 product_url
  -> ProductKeyer 生成 normalized_url、url_hash、platform、product_key
  -> L1 ProductDetailCache
  -> Redis alias/detail
  -> MySQL alias/detail
  -> miss 后 Redis lock
  -> 抓取页面 + AI/heuristic 解析
  -> detail_items
  -> MySQL upsert
  -> Redis set detail/alias
  -> L1 set
  -> delta answer
```

`product_followup`：

```text
请求 product_url + question
  -> 复用同一 loadSharedDetail
  -> 命中后直接 answering + delta
  -> 未命中仍提示先查看产品详情
```

## 验证命令

已通过：

```bash
go test ./internal/skill/productdetail ./internal/agent/chatflow ./internal/config
go test -count=1 ./internal/skill/productdetail
go test ./...
go test -tags eino ./...
go test -race ./internal/agent/... ./internal/api ./internal/memory/...
```

## 异常情况

开发中出现一次接口命名冲突：Developer Agent A/B 的接口命名和 Team Leader 初始骨架不同。已按子 Agent 已落地代码统一为 `ProductIdentity`、`StoredProductDetail`、`DetailRecord` 三层结构，并保留对应测试。

未发现阻断异常。

## 遗留建议

1. 生产环境如果不希望服务启动自动 DDL，需要将 `EnsureSchema` 改为迁移脚本执行。
2. 后续可增加指标：Redis 命中率、MySQL 命中率、AI 解析次数、upsert 失败次数、lock 竞争次数。
3. 可进一步实现后台异步刷新和热门商品预解析。
4. 如需更严格质量控制，可新增 `PRODUCT_DETAIL_MIN_MATCH_RATE`，当前沿用现有抽取校验。
