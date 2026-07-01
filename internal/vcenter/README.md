# internal/vcenter — vSphere 客户端封装

vCenter 连接 / inventory 同步 / OVA 部署相关 vSphere API 的封装（基于 `github.com/vmware/govmomi`）。

**状态机与同步机制**: 见 [`../RESOURCE_POOL.md`](../RESOURCE_POOL.md)。

## 主要文件

- [`client.go`](client.go) — `Connect` / `Logout` / `Inventory` / `RetryableError` / `MaybeRetryable`
- [`inventory.go`](inventory.go) — `DataCenter` / `Cluster` / `PlacementRef` 类型定义
- [`full_inventory.go`](full_inventory.go) — `Client.FullInventory()`（按 DC 拆 + PBM graceful + standalone ESXi 报错）
- [`storage_profiles.go`](storage_profiles.go) — PBM profile 拉取（独立 endpoint）
- [`library.go`](library.go) — 内容库探测
- [`retryable_test.go`](retryable_test.go) / [`inventory_test.go`](inventory_test.go) — 单测

## 修改时注意

1. **新增 vCenter RPC**：返回 error 时考虑是否包入 `RetryableError`（让 `pool_retry.go` 能重试）。
2. **修改 `FullInventory`**：保持 PBM 失败时 `storagePolicies` 为 nil（与"拉了但空"区分）。
3. **新增 DC 子资源**：在 `inventoryForDC` 的 `CreateContainerView` kinds 列表里添加，并在最后 append 到对应字段。