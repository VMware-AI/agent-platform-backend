# internal/graph — GraphQL resolvers + 资源池同步路径

GraphQL schema resolvers 的聚合处；同时承载**资源池同步路径**（与 [`../vcenter/`](../vcenter/) 协作）。

**资源池接入状态机与同步机制**: 见 [`../RESOURCE_POOL.md`](../RESOURCE_POOL.md)。这是后续维护资源池同步路径时的主文档。

## 资源池相关文件

| 文件 | 职责 |
|---|---|
| [`resourcepool.resolvers.go`](resourcepool.resolvers.go) | `CreateResourcePool` / `UpdateResourcePool` / `SyncResourcePool` / 两个 query |
| [`mappers.go`](mappers.go) | `toModelResourcePool` / `poolSyncState` 派生 |
| [`pool_sync_one.go`](pool_sync_one.go) | 唯一写 status 的路径（成功 / 失败 / breaker-open） |
| [`pool_auto_sync.go`](pool_auto_sync.go) | ticker 循环（`syncAllPools`） |
| [`pool_breaker.go`](pool_breaker.go) | gobreaker endpoint 注册表 |
| [`pool_retry.go`](pool_retry.go) | 指数退避重试 |
| [`pool_sync_source.go`](pool_sync_source.go) | ctx source tag（first-sync/ticker/manual） |
| [`pagination.go`](pagination.go) | `applyResourcePoolSort` 等排序 |
| [`vcenter_connect.go`](vcenter_connect.go) | `connectPool`（解析凭据 + 拨号） |
| [`secret_ref.go`](secret_ref.go) | 凭据 secret_ref 解析 |

## 修改时注意

1. **新增 sync 入口** → 用 `withSyncSource(ctx, ...)` 给 ctx 打 tag。
2. **新增 ent 字段** → 更新 `mappers.go::toModelResourcePool` + `mappers.go::poolSyncState`（如影响 syncStatus 派生）+ 重跑 `make generate`。
3. **改 status 写入** → 在 [`pool_sync_one.go`](pool_sync_one.go) 单点修改；保持"熔断器开启不写 status"的不变量。
4. **改失败路径** → 失败时用 `context.Background()` 而非请求 ctx，确保 status 能落库。