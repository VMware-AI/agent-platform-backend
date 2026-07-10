# VirtualKey user_id 字段 — 设计

日期:2026-07-09
分支:`修改virutalkey`(dev 阶段)
状态:设计稿,等待 review

## 背景

LiteLLM `/key/generate` 在某个版本升级后开始校验 `user_id` 非空(此前该字段为 `omitempty`,代码侧既未发也未填)。当前 `IssueVirtualKey` resolver 没有为 `gateway.GenerateKeyRequest.UserID` 赋值,导致 mint 调用被网关拒绝。

与此同时,`ent.VirtualKey` 历史 schema 经过多次重构(包括 `600d608` drop 旧 user_id/team_id 列、`e4e42dd` 重写为 per-agent-per-org、`6d756ef` regenerate ent),目前**没有 user_id 列**。

这次同时把字段加回 GraphQL input/output、ent 列、gateway wire 三处,让前端负责传值,后端做透传,不做兜底。

## 决策

| 项 | 值 | 理由 |
|---|---|---|
| ent 列类型 | `String`, `NotEmpty` | 直传,无 FK;与 gateway wire 形状一致 |
| GraphQL `IssueVirtualKeyInput.userId` | `String!`(必填) | 前端必须传,后端不做默认 |
| GraphQL `VirtualKey.userId` | `String!`(必填) | 列 NotEmpty,语义对齐 |
| 后端默认值 | **无** | 用户多次确认「前端处理」「后端不设置默认值」 |
| Gateway wire | `GenerateKeyRequest.UserID = input.UserID` | 直传 |
| Resolver 校验 | `input.UserID == ""` → 400 | GraphQL `!` 已保证,但加兜底保险 |
| ent edge / FK | 不加 | 用户在第二轮澄清中明确否决 UUID-FK 方案 |
| 持久化 | `create.SetUserID(input.UserID)` | 与列同名,直传 |
| 测试 | 不写(CLAUDE.md §1) | dev 阶段不主动维护测试 |
| Migration | 不写(CLAUDE.md §2 dev-stage 调整) | 靠 `ent.Client.Schema.Create()` 落库 |

## 变更面

### 1. GraphQL SDL — `schema/virtualkey.graphql`

```graphql
type VirtualKey {
  id: ID!
  ...
  # 前端传入的 user_id,LiteLLM gateway 也用这个值作为 user_id。
  # 必填,IssueVirtualKeyInput 强制要求前端传值,后端不做默认。
  userId: String!
  ...
}

input IssueVirtualKeyInput {
  # Required. Human-readable label.
  name: String!
  ...
  # Required. LiteLLM /key/generate 现在校验 user_id 非空。
  # 前端必须传一个非空字符串;后端透传到 ent.VirtualKey.user_id
  # 和 gateway.GenerateKeyRequest.UserID,不做默认值兜底。
  userId: String!
}
```

### 2. ent schema — `ent/schema/virtualkey.go`

在 `Fields()` 中加入:

```go
// 前端传入的 user_id;与 gateway.GenerateKeyRequest.UserID 直传。
// NotEmpty(dev-only:历史行没这个列,prod ALTER 会失败 — 后续 prod
// 数据迁移是另一个工作,见 Out of Scope)。
field.String("user_id").NotEmpty(),
```

不加 edge、不加 index。

### 3. Resolver — `internal/graph/virtualkey.resolvers.go::IssueVirtualKey`

在 step 1 校验中,加入 `input.UserID == ""` 兜底:

```go
if input.UserID == "" {
    return nil, gqlerror.Errorf("userId is required")
}
```

在 step 4 构建 `gateway.GenerateKeyRequest` 时:

```go
gReq := gateway.GenerateKeyRequest{
    UserID:              input.UserID,  // 新增
    Models:              input.Models,
    ...
}
```

在 step 5 创建 ent 时:

```go
create := r.Ent.VirtualKey.Create().
    SetLitellmKey(resp.Key).
    ...
    SetUserID(input.UserID).  // 新增,与 gateway wire 同源
    ...
```

### 4. 自动 regenerate

`make generate` 会更新:

- `ent/virtualkey/virtualkey.go`(`FieldUserID`、`ByUserID` 等)
- `ent/virtualkey/where.go`(`UserID` / `UserIDEQ` 等谓词)
- `ent/virtualkey_create.go`(`SetUserID`)
- `internal/graph/generated.go`(`IssueVirtualKeyInput`、`VirtualKey` 字段映射)
- `internal/graph/model/models_gen.go`(`IssueVirtualKeyInput.UserID`、`VirtualKey.UserID`)

### 5. Docs — `make docs`

`schema-dump` 重新生成 `docs/schema.graphql`;`apidocs` 重新生成 `docs/api/virtual-keys.md`。

## Out of Scope(显式排除)

- **后端默认值** — 用户明确说「前端处理」「后端不设置默认值」。即使 gateway 校验失败,也不在 resolver 里塞 `"admin"`。
- **prod 历史数据迁移** — `NotEmpty` 列在 prod 已经存在 `virtual_keys` 行的环境下会被 ALTER 拒绝。本次仅 dev / 预发布;prod 上线时需要单独的 backfill spec(类似 `cmd/backfill-vk-gateway` 的模式)。
- **UUID-FK 关联 users 表** — 第二轮澄清中被否决。后续若要按 user_id 过滤 / join,可能再起一个 spec 升级成 FK edge。
- **单元测试** — CLAUDE.md §1(开发期不反复维护测试)。
- **Versioned SQL migration** — CLAUDE.md §2(dev 阶段关掉 migration-drift;`Schema.Create()` 兜底)。
- **其他 mutation** — `revokeVirtualKey` / `regenerateVirtualKey` / `setVirtualKeyEnabled` / `associateVirtualKeyAgent` 不动 — `user_id` 在 `IssueVirtualKey` 时已经固化到 ent 行和 gateway,这些 mutation 不重发 user_id。
- **`VirtualKeys` query filter** — 不在 input 加 `userId` 参数。本 spec 只解决 mint 阶段的 user_id 缺失;后续按 user 过滤查询是另一个 use case。

## 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| prod 已有 `virtual_keys` 行没有 `user_id` | ALTER ADD NOT NULL 失败 | dev-only;prod 上线前需要 backfill(不在本 spec) |
| 前端没及时加 user_id | mint 调用 400 | GraphQL `!` 编译期暴露;resolver 二次校验兜底 |
| gateway 版本回退后 `user_id` 重新 optional | 字段冗余但无害 | 不主动删;后续按需清理 |

## 影响范围 — 一览表

| 文件 | 改动类型 |
|---|---|
| `schema/virtualkey.graphql` | 手改 |
| `ent/schema/virtualkey.go` | 手改 |
| `ent/virtualkey/...` | regenerate 产物 |
| `internal/graph/virtualkey.resolvers.go` | 手改 |
| `internal/graph/generated.go` | regenerate 产物 |
| `internal/graph/model/models_gen.go` | regenerate 产物 |
| `docs/schema.graphql` | regenerate 产物 |
| `docs/api/virtual-keys.md` | regenerate 产物 |

## 实现顺序

1. 改 `ent/schema/virtualkey.go`(`user_id NotEmpty`)
2. `make generate`(产物 1)
3. 改 `schema/virtualkey.graphql`(input/output 加 userId)
4. `make generate` 第二次(产物 2,带 GraphQL 字段)
5. 改 `internal/graph/virtualkey.resolvers.go` IssueVirtualKey
6. `make docs`
7. 跑 CI gate 检查:`gofmt` + `go vet` + `go build` + `make docs-check`