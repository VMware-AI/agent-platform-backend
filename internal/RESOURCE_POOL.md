# Resource Pool 资源池接入 — 状态机与同步机制

> 资源池接入涉及 `internal/vcenter`（vSphere/govmomi 封装）与 `internal/graph`（GraphQL resolver + 同步链路）两个包。本文档描述这两个包协作维护的状态机、同步触发点，以及后续修改时需要注意的不变量。

## 1. 同步入口（三个）

资源池同步由三个入口触发，**全部走 `Resolver.syncOnePool` 这一条共享路径**：

| 入口 | 文件 | 调用方式 |
|---|---|---|
| **Fire-and-forget first sync** | `internal/graph/resourcepool.resolvers.go` (`CreateResourcePool`) | 创建后 goroutine，`ctx` tag = `first-sync` |
| **Manual mutation** | `internal/graph/resourcepool.resolvers.go` (`SyncResourcePool`) | 同步调用，`ctx` tag = `manual` |
| **Background ticker** | `internal/graph/pool_auto_sync.go` (`syncAllPools`) | 每 `POOL_SYNC_INTERVAL_SECONDS` 秒一次，`ctx` tag = `ticker` |

**前提**：只有 `p.SecretRef != ""` 的池子才会 fire-and-forget / 进入 ticker 列表（无凭据同步必败，省一次徒劳）。

## 2. 状态字段（两层）

### 2.1 ent 持久化字段

存储只持久化两个字段：

| 字段 | 类型 | 含义 |
|---|---|---|
| `status` | enum (`disconnected` / `connected` / `error`) | 最近一次同步的端到端结果 |
| `last_synced_at` | `Time` (nullable) | 最近一次**成功**同步的时间戳 |

### 2.2 GraphQL 派生字段 `syncStatus`

`syncStatus` 是 **每次读取时计算** 的视图（不存储），见 [`internal/graph/mappers.go:115-121`](mappers.go#L115-L121)：

| 条件 | 派生值 |
|---|---|
| `status == error` | `FAILED` |
| `status != error` 且 `last_synced_at == nil` | `NEVER` |
| `status != error` 且 `last_synced_at != nil` | `SYNCED` |
| `SYNCING` / `PARTIAL` | **永不产生**（sync 是同步函数） |

> `updateResourcePool` mutation **不会重置** `status` 或 `last_synced_at`——它只覆盖凭据/名称/endpoint。

### 2.3 `syncStatus` filter 简化

[`internal/graph/resourcepool.resolvers.go`](resourcepool.resolvers.go) 的 filter 把 `syncStatus` 投影到 ent.status：

```go
switch *filter.SyncStatus {
case model.ResourcePoolSyncStateFailed:
    base = base.Where(resourcepool.StatusEQ(resourcepool.StatusError))
case model.ResourcePoolSyncStateSynced:
    base = base.Where(resourcepool.StatusEQ(resourcepool.StatusConnected))
case model.ResourcePoolSyncStateNever, ..., Partial:
    base = base.Where(resourcepool.StatusEQ(resourcepool.StatusDisconnected))
}
```

`last_synced_at` 在 filter 里没有参与（避免 subquery）。如需精确 NEVER，可改成 `WHERE last_synced_at IS NULL`。

## 3. 状态转移图

```
            CREATE             SYNC ok            SYNC fail         breaker open
        ┌─────────┐         ┌──────────┐         ┌──────────┐         ┌──────────┐
        │disconnected│ ───► │connected │ ───►    │connected │ ───►    │  error   │
        │+t_stmp=nil │      │+t_stmp=t │        │+t_stmp=t │        │+t_stmp=t │
        └─────────┘         └──────────┘         └──────────┘         └──────────┘
            ▲                     │                   │                     │
            │                     │   breaker probe ok / retry ok            │
            │                     └───────────────────┘                     │
            │                                                                    │
            │   breaker still open (next ticker tick / manual sync)           │
            └──────────────── status left as-is ─────────────────────────────┘
```

**核心不变量**：

- `status == connected` ⇒ `last_synced_at != nil`（成功路径同时写两者）
- `status == disconnected` ⇒ `last_synced_at == nil`（ent schema 默认值；只有新创建或"sync 失败回退"才有可能，但当前实现下 sync 失败不回退到 disconnected，而是写 error）
- `status == error` ⇒ `last_synced_at` **保持上次成功的时间**（失败时不擦除，便于前端显示"上次成功时间 + 当前失败"）

## 4. 状态写入路径（唯一一处：`syncOnePool`）

[`internal/graph/pool_sync_one.go`](pool_sync_one.go) 是**唯一**改 `status` 的地方。两处写入：

### 4.1 成功路径

```go
updated, err := r.Ent.ResourcePool.UpdateOne(pool).
    SetStatus(resourcepool.StatusConnected).
    SetLastSyncedAt(now).
    SetInventory(inventory).
    Save(ctx)
```

### 4.2 失败路径（仅真失败）

```go
_, _ = r.Ent.ResourcePool.UpdateOne(pool).
    SetStatus(resourcepool.StatusError).
    Save(context.Background())
```

注意：失败路径用 `context.Background()` 而非 ctx——避免请求 ctx 取消导致 status 也没机会落库。

### 4.3 熔断器开启 — 不写 status

```go
if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
    log.Printf("pool-sync %s: breaker open, skipped (status left as %s)", pool.Name, pool.Status)
    return nil, time.Time{}, err
}
```

**为什么不写**：避免一个 healthy 池子因为 vCenter 短暂抖动被错标 error。下次 ticker tick 或手动 sync 会探测恢复。

## 5. 容错链（syncOnePool 内部）

```
30s 单池超时  →  指数退避重试(1s/2s/4s + jitter, ≤ POOL_SYNC_MAX_RETRIES 次)
            →  sony/gobreaker 熔断器(per-endpoint)
              ├─ 状态: Closed → Open(连续 POOL_SYNC_BREAKER_THRESHOLD 次失败) → HalfOpen(等待 POOL_SYNC_BREAKER_OPEN_SECONDS)
              └─ Open 状态拒绝调用(返回 ErrOpenState) → syncOnePool 不写 status
```

### 5.1 重试触发条件

只有 `*vcenter.RetryableError` 触发重试。`internal/vcenter/client.go` 的 `MaybeRetryable` 根据错误文本（connection refused / i/o timeout / EOF / reset by peer 等）自动包装。业务错误（auth fail / object not found / invalid login）**不重试**。

### 5.2 默认行为（未调 `EnablePoolSync`）

`newTestResolver` 默认**不**调 `EnablePoolSync`，所以 `poolBreakers == nil`。`syncOnePool` 检测到 nil 时跳过 breaker 层、直接执行重试 + 单池超时——但仍走完整 connect→inventory→full inventory→DB 写回链路。这让大多数测试不必关心 breaker 配置。

## 6. 各场景的最终状态

| 场景 | `status` | `last_synced_at` | `syncStatus` |
|---|---|---|---|
| 刚 `CreateResourcePool`（无 secret_ref） | `disconnected` | nil | `NEVER` |
| 刚 `CreateResourcePool`（带 secret_ref，goroutine 未跑完） | `disconnected` | nil | `NEVER` |
| Fire-and-forget 成功 | `connected` | set | `SYNCED` |
| Fire-and-forget 失败（连接/认证错误） | `error` | nil | `FAILED` |
| 手动 `syncResourcePool` 成功 | `connected` | set | `SYNCED` |
| 手动 `syncResourcePool` 失败 | `error` | 保留上次成功时间 | `FAILED` |
| 后台 ticker 成功 | `connected` | set | `SYNCED` |
| 后台 ticker 失败 | `error` | 保留上次成功时间 | `FAILED` |
| 熔断器开启（gobreaker ErrOpenState） | **不变**（保留上次） | 不变 | 不变 |
| `UpdateResourcePool` 改名字/凭据 | **不变** | **不变** | **不变** |
| `DeleteResourcePool` | 行被删 | 删 | — |

## 7. 修改状态机时的注意事项

1. **新增 sync 触发入口** → 必须 `withSyncSource(ctx, ...)` 给 ctx 打 tag（见 [`internal/graph/pool_sync_source.go`](graph/pool_sync_source.go)），否则日志无法区分来源。
2. **新增 ent 字段** → 必须同时更新：
   - `ent/schema/resourcepool.go` 字段
   - `internal/graph/model/models_gen.go`（gqlgen 自动生成，但需要重跑 `make generate`）
   - `internal/graph/mappers.go::toModelResourcePool` 投影
   - GraphQL schema（如需 GraphQL 暴露）
   - `mappers.go::poolSyncState` 派生逻辑（如影响 syncStatus 语义）
3. **新增 sync 失败语义** → 必须决定是否写 status：
   - 真失败 → `SetStatus(error)`
   - 临时不可达（breaker open / ctx cancelled） → 不写 status
4. **新增 vCenter 操作** → 必须考虑：
   - 是否包入 `*vcenter.RetryableError`（决定能否重试）
   - 是否需要纳入 `FullInventory` 的 PBM 失败 graceful 处理（`storagePolicies` 保持 nil 区分"PBM 没拉过"与"拉了但空"）
5. **修改 `syncStatus` 派生** → 注意 `SYNCING`/`PARTIAL` 永远不会被返回（注释在 [`schema/resourcepool.graphql:13-15`](../schema/resourcepool.graphql#L13-L15)）。如果要支持，必须新增 ent 列（如 `in_flight`）+ 写入路径。
6. **修改失败路径写入逻辑** → 失败时用 `context.Background()` 而非请求 ctx——确保 status 能落库。

## 8. 相关文件索引

| 文件 | 职责 |
|---|---|
| [`ent/schema/resourcepool.go`](../ent/schema/resourcepool.go) | 持久化字段定义（status enum 默认值、`last_synced_at` nullable） |
| [`internal/vcenter/inventory.go`](vcenter/inventory.go) | `DataCenter`/`Cluster`/`PlacementRef` 类型 |
| [`internal/vcenter/client.go`](vcenter/client.go) | `Connect` / `Inventory` / `Logout` / `MaybeRetryable` / `RetryableError` |
| [`internal/vcenter/full_inventory.go`](vcenter/full_inventory.go) | `FullInventory`（按 DC 拆 + PBM graceful + standalone ESXi 报错） |
| [`internal/vcenter/storage_profiles.go`](vcenter/storage_profiles.go) | PBM profile 拉取（独立 endpoint） |
| [`internal/graph/mappers.go`](graph/mappers.go) | `toModelResourcePool` / `poolSyncState` 派生 |
| [`internal/graph/resourcepool.resolvers.go`](graph/resourcepool.resolvers.go) | `CreateResourcePool` / `UpdateResourcePool` / `SyncResourcePool` / 三个 query |
| [`internal/graph/pool_sync_one.go`](graph/pool_sync_one.go) | 唯一写 status 的路径（成功 / 失败 / breaker-open） |
| [`internal/graph/pool_auto_sync.go`](graph/pool_auto_sync.go) | ticker 循环 |
| [`internal/graph/pool_breaker.go`](graph/pool_breaker.go) | gobreaker endpoint 注册表 |
| [`internal/graph/pool_retry.go`](graph/pool_retry.go) | 指数退避重试 |
| [`internal/graph/pool_sync_source.go`](graph/pool_sync_source.go) | ctx source tag（first-sync/ticker/manual） |
| [`internal/config/config.go`](config/config.go) | 5 个 env var（interval/timeout/retries/breaker threshold/breaker open） |
| [`schema/resourcepool.graphql`](../schema/resourcepool.graphql) | GraphQL schema |
| [`docs/api/resource-pools.md`](../docs/api/resource-pools.md) | 自动生成的 API 文档 |