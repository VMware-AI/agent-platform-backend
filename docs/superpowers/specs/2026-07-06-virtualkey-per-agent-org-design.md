# Virtual Key 重构: per-user → per-agent-per-org

**日期**: 2026-07-06
**作者**: Claude Code (brainstorming session)
**目标**: 把 VirtualKey 从「per-user LiteLLM bearer 治理表」改造为「per-agent-per-org 的 org 范围资源」,新增 `maskedKey` 持久化预览列、`modelGateway: ModelGateway!` 嵌套关联字段、`associateVirtualKeyAgent` mutation、`gatewayAvailableModels` 实时 model 列表 query,删除 user_id / team_id 列与所有相关调用点。

**目标 (2026-07 补充)**: 同时建立 **model ↔ modelGateway 强耦合**: `issueVirtualKey` 强制写路径校验所选 models ⊆ 该 modelGateway 当前可用模型列表,前端不再经过 ModelRoute 间接选。

---

## 1. 背景 (Context)

当前 [ent/schema/virtualkey.go](ent/schema/virtualkey.go) 中的 VirtualKey 是 per-user 的:
- `user_id` 是 **必填** UUID, 用于权限/审计/虚拟 key 查询过滤/租户隔离
- `team_id` (string) 是 LiteLLM team 标识, 由 `deptIDFromTeam(vk.TeamID)` 转为部门 UUID 来路由网关
- `organization_id` (string) 仅作可选描述, 与 `team_id` 概念错位
- secret 明文返回仅在 issue / regenerate 时通过 `IssuedVirtualKey.secret` 一次性给出;查询永远看不到明文 —— 但**也没有 maskedKey 预览**

产品的诉求是把 VirtualKey 提升为「per-org 资源 + per-agent 绑定」: 不再归属个人, 而归属于组织, agent 通过 `associateVirtualKeyAgent` 显式绑定。 这与当前 per-user 模型有结构性冲突, 需要清理。

### 1.1 关键不变量 (必须保留)

1. **DB 中存明文 litellm_key**: 后续 revoke / regenerate / setVirtualKeyEnabled 都需要原文去调用 LiteLLM `/key/delete`, `/key/{key}/regenerate`, `/key/update`. 不能 hash. `Sensitive()` 注解保留.
2. **`agent_id` 部分唯一索引** (status <> 'revoked') 保留 —— 1 个 agent 同时只能有 1 个 active 密钥. DB 层强制约束.
3. **网关优先 (gateway-first) 顺序** 在 revoke / setVirtualKeyEnabled 上保留 —— 网关先收到指令, DB 后翻状态, 防止 "DB 已 revoked 但 gateway 还在计费" 的对账黑洞.
4. **付费流量不走本平台** —— LLM 请求仍由 Client → LiteLLM, 平台不代理.
5. **`Sensitive()` + `json:"-"` + `String()` 渲染为 `<sensitive>`** 用于所有 secret 类型字段, 包括新增的 `masked_key` (它虽然不是 secret, 但与 secret 同生命周期, 防止误从日志泄漏).
6. **审计覆盖所有变更**, 包括 `key.issue` / `key.revoke` / `key.regenerate` / `key.set_enabled` 这次新增 `key.associate_agent`.
7. **model ↔ modelGateway 强耦合**: `models` 字段中的每个模型名必须真实存在于 `IssueVirtualKeyInput.modelGateway` 指向的 GatewayConnection 当前可用模型列表中. 由 `issueVirtualKey` resolver 在调用 `gw.GenerateKey` 之前再调一次 `GET {gateway}/model/list` 做实时交集校验. 这是写路径闭环, 与 ModelRoute 体系无关 (key 不再绑 ModelRoute).

### 1.2 业务前提

- **没有历史数据** —— 不需要 backfill, 可以直接 DDL 删除列
- `organizationId` 是平台层 multi-tenant 概念, 与 LiteLLM team 不再区分 —— 凡是原来写 `team_id` 的地方都重写为 `organization_id`
- 前端接受 secret 单次返回 + maskedKey 始终返回的双轨模式
- **前端改造 (2026-07 补充)**: 前端 issueVirtualKey 表单重构 —— 先选 `modelGateway`, 选完调 `gatewayAvailableModels(gatewayConnectionId)` 实时拉模型列表填充 models 多选框. 表单草稿类型 (issue 时) 携带 `modelGateway` + `models[]`, 由后端做二次校验. 原有 ModelRoute 多选 UI 完全下线 (前端提交单独 PR).
- **ModelRoute 体系保留**: ModelRoute / createModelRoute / updateModelRoute / syncRouterSettings 都不动. ModelRoute 继续服务于 LiteLLM `/config/update` 路由推送, 与 VirtualKey 治理是两条独立轨道.

---

## 2. 数据模型 (Database Schema)

### 2.1 ent schema 改动

修改文件: [ent/schema/virtualkey.go](ent/schema/virtualkey.go).

| 字段 | 改动 | 说明 |
| --- | --- | --- |
| `id` | 不变 | UUID PK |
| `litellm_key` | 不变 | Sensitive + NotEmpty, 明文, 用于 outbound gateway calls |
| `litellm_token` | 不变 | LiteLLM hashed token, 对账用 |
| `alias` | **删除** | 改名为 `name`, 见下 |
| `user_id` | **删除列** | 不再 per-user |
| `agent_id` | 不变 | nullable UUID, 1:1 部分唯一索引保留 (status <> 'revoked') |
| `team_id` | **删除列** | 被 `organization_id` 取代 |
| `gateway_connection_id` | **重命名为 `model_gateway_id`** | 单一列同时承担 "面向前端的 modelGateway 关联对象" 与 "LLD-14 颁发后生命周期 pin" 两面. NotNull. LLD-14 内部命名跟着改 (gatewayKeyClientForVK 等内部 helper 同步重命名). |
| `organization_id` | **NotEmpty** | string, 顶层必填. 升为行业务字段 |
| `name` | **新增** | string, NotEmpty. 取代 `alias` |
| `masked_key` | **新增** | string, Sensitive + NotEmpty. 持久化的脱敏预览, 例 "sk-aBcD...xyZ4" |
| `models` | 不变 | `[]string`, 用户勾选的模型名. issue 时由后端按 `modelGateway` 调 `/model/list` 做二次校验 (不删列, 因为需要在 DB 上记下 key 颁发时锁定的模型清单, 用于 reconcile / 展示). |
| `max_budget` / `*_limit` / `*_limit_type` / `budget_duration` / `expires_at` / `allowed_routes` / `tags` / `blocked` / `key_type` / `auto_rotate` / `rotation_interval` | 不变 | 治理配额与可观测性字段全部保留 |
| `status` | 不变 | enum: `active` / `disabled` / `revoked`, default `active` |
| `last_active_at` / `spend` | 不变 | 周期 worker 刷新 |
| `created_at` / `updated_at` (TimeMixin) | 不变 | |

### 2.2 索引不变

```
index.Fields("user_id")                            // 删除 (user_id 列移除)
index.Fields("agent_id").Unique().
    Annotations(entsql.IndexWhere("status <> 'revoked'"))   // 保留
```

未来如果按 org 查询频繁, 可以加 `index.Fields("organization_id")` (非唯一, 普通 lookup). 这次 PR 不加, 留给后续按查询热度优化.

### 2.3 迁移

无历史数据, 故:
- 删除 `user_id`, `team_id`, `alias` 列直接 DDL 删
- 新增 `name`, `masked_key` 列 NOT NULL DEFAULT '' (ent 自动生成), 然后立刻 `ALTER COLUMN ... SET NOT NULL` 在应用层控制. 这里走 ent 标准 `make migrate-diff name=xxx` 生成 DDL.
- 具体运行时: 重新 `ent generate`, `make migrate-diff name=vk_per_agent_org`, 把生成的 DDL PR 进来.

### 2.4 GraphQL 类型改动

#### VirtualKey

```graphql
type VirtualKey {
  id: ID!
  name: String!                    # NEW: 取代 alias
  maskedKey: String!               # NEW: 持久化预览
  organizationId: String!          # 提升为非空
  modelGateway: ModelGateway!      # NEW: 嵌套对象,引用 gateway_connections
  agentId: ID                      # (不变)
  models: [String!]!
  maxBudget: Float
  status: VirtualKeyStatus!
  expiresAt: Time
  duration: String                 # NEW: 与 expiresAt 共存的 “剩余天数” 脱敏展示
  createdAt: Time!
  updatedAt: Time!                 # NEW: TimeMixin 本来就有, 之前未暴露
  maxParallelRequests: Int
  tpmLimit: Int
  rpmLimit: Int
  rpmLimitType: String
  tpmLimitType: String
  budgetDuration: String
  allowedRoutes: [String!]!
  tags: [String!]!
  blocked: Boolean!
  keyType: String!
  autoRotate: Boolean!
  rotationInterval: String
  spend: Float!
  lastActiveAt: Time

  # 删除: alias, userId, teamId GraphQL 字段
  # modelGateway 嵌套对象需要手写 field resolver (gqlgen 不会自动从 UUID 列派生).
  # 实现见 §3.7 (modelGatewayFromEnt) — 复用 modelgateway.resolvers.go 中已
  # 有的字段映射, 单独 PR 抽 buildModelGatewayFromConn 公共 helper 避免双份镜像.
}
```

**注:** `duration` 字段在响应中是 "剩余天数" (string, 友好展示), 输入 `IssueVirtualKeyInput.duration` 是 "过期天数" (string, 业务字段). 二者通过 expiresAt 互通.

#### IssuedVirtualKey

```graphql
type IssuedVirtualKey {
  virtualKey: VirtualKey!   # 包含 maskedKey 字段
  secret: String!           # 仅在 issue / regenerate 一次性返回
}
```

(`maskedKey` 不在 `IssuedVirtualKey` 上重复暴露, 直接读 `virtualKey.maskedKey`.)

#### IssueVirtualKeyInput

```graphql
input IssueVirtualKeyInput {
  organizationId: String!      # NEW: 顶层必填, 取代 teamId
  name: String!                # NEW: 取代 alias
  modelGateway: ID!              # NEW: 必填,引用 GatewayConnection (同时是 modelGateway 关联)
  agentId: ID                  # 可选, "创建时不绑, 之后 associateAgent 绑"
  duration: String             # NEW: 可选, 例 "30d". 服务端算出 expiresAt
  expiresAt: Time              # 可选. 与 duration 互斥 (同时传则 duration 优先)
  models: [String!]            # 可选, 用户按 modelGateway 过滤后勾选的模型名. 后端做二次校验.
  maxBudget: Float
  budgetDuration: String
  maxParallelRequests: Int
  rpmLimit: Int
  tpmLimit: Int
  rpmLimitType: String
  tpmLimitType: String
  allowedRoutes: [String!]
  tags: [String!]
  blocked: Boolean
  keyType: String
  autoRotate: Boolean
  rotationInterval: String

  # 删除: userId, teamId, alias
  # 注意: 原 GraphQL 中 organizationId 是可选的, 现在提升为必填
  # 注意: 模型选由 modelGateway + gatewayAvailableModels query 在前端组合, 不需要传 modelRouteName
}
```

#### Queries / Mutations

```graphql
extend type Query {
  # 实时返回指定 modelGateway 当前支持的模型列表 (调 LiteLLM /model/list).
  # 前端在「选了 modelGateway 之后」调用,把返回的字符串数组渲染为 models 多选框.
  # 每次实时拉, 不缓存 (model list 变更罕见, 缓存代价不值得).
  gatewayAvailableModels(gatewayConnectionId: ID!): [String!]!
      @hasRole(any: [admin, read_only])

  # 原: virtualKeys(userId: ID): [VirtualKey!]!
  virtualKeys(organizationId: ID, agentId: ID, modelGateway: ID): [VirtualKey!]!
      @hasRole(any: [admin, read_only])
  # 注: organizationId / agentId / modelGateway 是三个独立可选过滤; 都为 null 则返回当前租户内全部
  # 同时传多个则取交集
}

extend type Mutation {
  issueVirtualKey(input: IssueVirtualKeyInput!): IssuedVirtualKey!
      @hasPermission(perm: "key:manage")

  revokeVirtualKey(id: ID!): Boolean!
      @hasPermission(perm: "key:manage")

  regenerateVirtualKey(id: ID!): IssuedVirtualKey!
      @hasPermission(perm: "key:manage")

  setVirtualKeyEnabled(id: ID!, enabled: Boolean!): VirtualKey!
      @hasPermission(perm: "key:manage")

  # NEW
  associateVirtualKeyAgent(virtualKeyId: ID!, agentId: ID!): VirtualKey!
      @hasPermission(perm: "key:manage")
}
```

---

## 3. 业务逻辑 (Resolvers)

所有现有 resolver 集中在 [internal/graph/virtualkey.resolvers.go](internal/graph/virtualkey.resolvers.go), 重写路径是同一个文件.

### 3.1 IssueVirtualKey

| 步骤 | 说明 |
| --- | --- |
| 1. 校验 input.ModelGateway 必填 | 解析 UUID; 失败 → gqlerror 400. 这一步替代原来的 "按 teamID 推导 dept UUID → 查网关" 路径. |
| 2. 按 modelGateway 直查 GatewayConnection | `r.Ent.GatewayConnection.Get(ctx, modelGatewayID)`. 拿到 baseURL + masterKey 构造 `gateway.Client`. 查不到 → 404 "model gateway not found". |
| 3. 校验 organizationId 必填 | 服务端解析失败直接 gqlerror |
| 4. **取消原 user_id 校验路径** | 不再校验 user cross-tenant |
| 5. 校验 agentId | 若提供, 检查 1:1 唯一索引 (DB 层兜底) |
| 6. 解析 duration | 若给 "30d", 转换为 expiresAt = now + 30d. 若 duration 与 expiresAt 都给, duration 优先 |
| 7. **二次校验 models ⊆ gateway 可用模型** | 调 `gw.ListAvailableModels(ctx)` 拿到当前 LiteLLM 的模型列表 (实时, 无缓存). 计算 `input.Models - gatewayModels`. 若有差集 → gqlerror 400 "models not available on modelGateway X: [...]". **这一步是 model ↔ modelGateway 强耦合的真正闭环**. |
| 8. 调用 `gw.GenerateKey` | `/key/generate`, 拿到 KeyResponse { Key, Token } |
| 9. 计算 maskedKey | 取 secret 头 6 字符 + "..." + 尾 4 字符. 例 "sk-aBcD...xyZ4". 若 secret 长度 < 12, 整个 secret (兜底). 用 utility 函数 `redactKey(s string) string` |
| 10. `r.Ent.VirtualKey.Create()` | SetLitellmKey(resp.Key), SetLitellmToken(resp.Token), SetMaskedKey(maskedKey), SetName(input.Name), SetOrganizationID(input.OrganizationID), **SetModelGatewayID(modelGatewayID)** (取代 SetGatewayConnectionID;模型名仅做内部存储命名,GraphQL 字段叫 modelGateway), 可选 SetAgentID(input.AgentID), SetExpiresAt(expiresAt), SetModels(input.Models), 与剩余 governance 字段 1:1 镜像 |
| 11. Save → 失败补偿 gw.DeleteKey | 同原逻辑 |
| 12. audit "key.issue" success | 同原 |
| 13. return &IssuedVirtualKey{VirtualKey: toModelVirtualKey(vk), Secret: resp.Key} | 同原 |

**maskedKey 算法** (在 `internal/gateway/client.go` 或新 helper 文件中)::

```go
// redactKey returns a safe-to-display preview of an API key.
//   sk-aBcDeFgHiJkLmNoPqRsT  →  "sk-aBcD...RsT"   (head 6 + "..." + tail 4)
//   < 12 chars               →  full string
// Format keeps operator recognizable (liteLLM prefix) without leaking
// the body. No random/structural info, so not a secret itself.
func redactKey(plain string) string {
    if len(plain) < 12 {
        return plain
    }
    return plain[:6] + "..." + plain[len(plain)-4:]
}
```

### 3.2 RevokeVirtualKey / RegenerateVirtualKey / SetVirtualKeyEnabled

逻辑保持. 用户 `id` 解析与 DB 字段名变更 (SetStatus, SetLitellmKey 等) 不变. **regenerate 必须同时覆写 masked_key** —— 这是 regenerate 的副作用, 文档要明确. (保留 unmask 是设计缺陷, 不能保留旧 masked_key 与新 secret 错配.)

### 3.3 AssociateVirtualKeyAgent

```go
func (r *mutationResolver) AssociateVirtualKeyAgent(
    ctx context.Context, virtualKeyId string, agentId string,
) (*model.VirtualKey, error) {
    vkID, err := uuid.Parse(virtualKeyId)
    if err != nil { return nil, gqlerror.Errorf("invalid virtualKeyId") }
    aID, err := uuid.Parse(agentId)
    if err != nil { return nil, gqlerror.Errorf("invalid agentId") }

    vk, err := r.Ent.VirtualKey.Get(ctx, vkID)
    if err != nil { return nil, err }

    // 1:1 unique invariant (DB 兜底). If target agent already has another
    // non-revoked key, reject. 无并发乐观控制: 依赖 partial unique index 在
    // DB 层拒绝 (race condition: 两个并发 AssociateVirtualKeyAgent 同 agent,
    // 第二个 Save 会收到唯一索引冲突, 转为 4xx).
    existing, err := r.Ent.VirtualKey.Query().
        Where(virtualkey.AgentIDEQ(aID), virtualkey.StatusNEQ(virtualkey.StatusRevoked)).
        Only(ctx)
    if err == nil && existing != nil && existing.ID != vkID {
        return nil, gqlerror.Errorf("agent %s already has an active key %s", aID, existing.ID)
    }
    // ent.NotFound is fine → 目标 agent 当前无 active key

    // 重绑 (含"先清理旧关联后绑新"): 直接 SetAgentID 即可; 若旧 agent 不为空,
    // 旧 agent 唯一索引会自动释放 (因为本次 Save 把 agent_id 从旧挪到新).
    updated, err := r.Ent.VirtualKey.UpdateOneID(vkID).SetAgentID(aID).Save(ctx)
    if err != nil { return nil, err }

    r.audit(ctx, "key.associate_agent", "virtual_key", vkID.String(), true, actorID(auth.FromContext(ctx)))
    return toModelVirtualKey(updated), nil
}
```

### 3.4 VirtualKeys (Query)

```go
func (r *queryResolver) VirtualKeys(
    ctx context.Context, organizationId *string, agentId *string, modelGateway *string,
) ([]model.VirtualKey, error) {
    q := r.Ent.VirtualKey.Query()
    if organizationId != nil {
        q = q.Where(virtualkey.OrganizationIDEQ(*organizationId))
    }
    if agentId != nil {
        aid, err := uuid.Parse(*agentId)
        if err != nil { return nil, gqlerror.Errorf("invalid agentId") }
        q = q.Where(virtualkey.AgentIDEQ(aid))
    }
    if modelGateway != nil {
        mid, err := uuid.Parse(*modelGateway)
        if err != nil { return nil, gqlerror.Errorf("invalid modelGateway") }
        q = q.Where(virtualkey.ModelGatewayIDEQ(mid))
    }
    // 租户隔离: key 属于 org 的 tenant, tenant-admin 仅看本租户范围
    // 现状: tenantScopeFor 是 ctx 派生, 走的是 user→tenant 链;
    // 新逻辑需要走 organizationId→tenant.
    // 待办: 与 LLD-10 B-class 现有实现确认一致后接入.
    // ...
    keys, err := q.Order(orderNewest).All(ctx)
    if err != nil { return nil, err }
    return mapSlice(keys, toModelVirtualKey), nil
}
```

> **P0 待澄清**: 平台层 organization 与 tenant 表的关系, 当前 LLD-10 B-class 通过 users 表取. 这一段在实现时需要再核对一次 `internal/graph/tenant_scope.go`, 但**不阻断** schema 重构 —— tenant 隔离错配时走 "denyAll" 兜底 (返回空列表) 即可, 安全侧不恶化.

### 3.5 toModelVirtualKey

[internal/graph/mappers.go:115-199](internal/graph/mappers.go#L115) 现有 mapper 改名/扩展:

```go
func toModelVirtualKey(ctx context.Context, r *Resolver, k *ent.VirtualKey) (*model.VirtualKey, error) {
    // ModelGateway 嵌套对象 — 由 gqlgen 自动生成的 field resolver (modelGateway)
    // 触发. 我们需要提供 ent.VirtualKey.ModelGatewayID, 然后 resolver 内部走
    // Ent.GatewayConnection.Get + 已有 modelGatewayById resolver 的 model 构造逻辑.
    // 这里我们直接构造 ModelGateway 对象, 不调用 modelGatewayById (那会做权限检查,
    // 此处我们已经过 key:manage 权限).
    mg, err := modelGatewayFromEnt(ctx, r, k.ModelGatewayID)
    if err != nil {
        return nil, err
    }
    return &model.VirtualKey{
        ID:             k.ID.String(),
        Name:           k.Name,
        MaskedKey:      k.MaskedKey,
        OrganizationID: k.OrganizationID,
        ModelGateway:   mg,
        AgentID:        uuidOrNil(k.AgentID),
        Models:         modelsOrEmpty(k.Models),
        Tags:           tagsOrEmpty(k.Tags),
        AllowedRoutes:  routesOrEmpty(k.AllowedRoutes),
        Blocked:        k.Blocked,
        KeyType:        k.KeyType,
        AutoRotate:     k.AutoRotate,
        Spend:          float64(k.Spend),
        Status:         model.VirtualKeyStatus(string(k.Status)),
        CreatedAt:      k.CreatedAt,
        UpdatedAt:      k.UpdatedAt,
        Duration:       formatRemainingDuration(k.ExpiresAt),
        // Optional/nullable pointers:
        MaxParallelRequests: k.MaxParallelRequests,
        TpmLimit:            k.TpmLimit,
        RpmLimit:            k.RpmLimit,
        RpmLimitType:        k.RpmLimitType,
        TpmLimitType:        k.TpmLimitType,
        BudgetDuration:      k.BudgetDuration,
        ExpiresAt:           k.ExpiresAt,
        RotationInterval:    k.RotationInterval,
        LastActiveAt:        k.LastActiveAt,
        MaxBudget:           k.MaxBudget,
    }, nil
}
```

`formatRemainingDuration` 是纯展示函数: `expiresAt == nil` 返回 null, 否则返回 "Xd" / "Xh" 形式 (按需).

辅助 helper: `modelsOrEmpty` / `tagsOrEmpty` / `routesOrEmpty` 都是 nil → `[]string{}` 防 GraphQL non-null 数组返 null 的标准化:

```go
func modelsOrEmpty(s []string) []string  { if s == nil { return []string{} }; return s }
func tagsOrEmpty(s []string) []string    { if s == nil { return []string{} }; return s }
func routesOrEmpty(s []string) []string  { if s == nil { return []string{} }; return s }
```

**注意**: `toModelVirtualKey` 签名从原 `(k *ent.VirtualKey) *model.VirtualKey` 改为 `(ctx, r, k)` 三参, 因为 ModelGateway 嵌套对象需要 ctx + Resolver. 所有原调用方 (4 个 resolver + mapper 引用) 同步更新.

### 3.6 GatewayAvailableModels (Query)

```go
func (r *queryResolver) GatewayAvailableModels(
    ctx context.Context, gatewayConnectionId string,
) ([]string, error) {
    mid, err := uuid.Parse(gatewayConnectionId)
    if err != nil { return nil, gqlerror.Errorf("invalid gatewayConnectionId") }

    conn, err := r.Ent.GatewayConnection.Get(ctx, mid)
    if err != nil {
        if ent.IsNotFound(err) { return nil, gqlerror.Errorf("model gateway not found") }
        return nil, err
    }

    gw := r.buildGatewayKeyClient(ctx, conn)
    if gw == nil {
        return nil, gqlerror.Errorf("model gateway client unavailable")
    }

    // 实时拉 LiteLLM /model/list — 不缓存. 失败透传 (e.g. LiteLLM 离线 → 502).
    models, err := gw.ListAvailableModels(ctx)
    if err != nil {
        return nil, fmt.Errorf("list available models from gateway: %w", err)
    }
    return models, nil
}
```

### 3.7 modelGatewayFromEnt (内部 helper)

```go
// modelGatewayFromEnt constructs the GraphQL ModelGateway from an ent.GatewayConnection.
// Mirrors modelgateway.resolvers.go's ModelGatewayById, minus the permission check
// (we have already passed key:manage).
func modelGatewayFromEnt(ctx context.Context, r *Resolver, connID uuid.UUID) (*model.ModelGateway, error) {
    conn, err := r.Ent.GatewayConnection.Get(ctx, connID)
    if err != nil {
        if ent.IsNotFound(err) {
            return nil, gqlerror.Errorf("model gateway not found")
        }
        return nil, err
    }
    // 复用现有 ModelGateway 字段填充逻辑, 不重复镜像每个字段.
    // (实际实现应抽 modelgateway.resolvers.go 中的字段映射为单独 helper —
    //  本次 PR 内完成, 避免双份字段映射.)
    return buildModelGatewayFromConn(conn), nil
}
```

注: `buildModelGatewayFromConn` 是从现有 `modelgateway.resolvers.go::ModelGatewayById` 抽取的字段映射函数 —— 本次 PR 同时把那段抽出来, 避免双份镜像逻辑.

---

## 4. 移除与停止点

1. **删除** `internal/graph/account_helpers.go::revokeUserKeys` (DeleteUser 不再级联 revoke 任何 key)
2. **修改** [internal/graph/account.resolvers.go](internal/graph/account.resolvers.go) `DeleteUser` 流程: 不调用 `r.revokeUserKeys(ctx, uid)`. 顶部注释更新: "user 删除后, 其名下虚拟 key 仍生效, 由 administrator 显式 revoke"
3. **删除** VirtualKey GraphQL 类型上的 `alias`, `userId`, `teamId` 字段
4. **更新** `internal/graph/tenant_scope.go` 关于 "virtual key → user's tenant" 的注释 —— 改为 "key → org's tenant"
5. **审计** [internal/graph/gateway_resolve.go:212-224](internal/graph/gateway_resolve.go#L212) `deptIDFromTeam` 函数: 语义保留 (team string → dept UUID), 但调用方改为传 `input.OrganizationID`. 函数本体不动, 注释改为 "teamID/OrganizationID equivalent (LLD-13 §3.3)".
6. **(2026-07 补充) 重命名 `gateway_connection_id` → `model_gateway_id`**: ent schema 列重命名, 内部 helper 同步 (`gatewayKeyClientForVK` → `modelGatewayClientForVK`, `gatewayKeyClientForConn` → `modelGatewayClientForConn`). 所有调用方 `internal/graph/deploy.resolvers.go`, `internal/graph/deploy_targets.go`, `internal/graph/department.resolvers.go` 同步改字段名.
7. **(2026-07 补充) 新增 `gatewayAvailableModels` resolver + `gw.ListAvailableModels` 客户端方法**: 新增 LiteLLM `/model/list` 调用. 写路径前置校验 + 读路径实时返回.

---

## 5. 错误处理

| 场景 | 错误形态 |
| --- | --- |
| `issueVirtualKey` 无 organizationId | gqlerror 400 "organizationId is required" |
| `issueVirtualKey` 无 modelGateway / UUID 解析失败 | gqlerror 400 "modelGateway is required" |
| `issueVirtualKey` modelGateway 查不到 GatewayConnection | gqlerror 404 "model gateway not found" |
| `issueVirtualKey` models 不在该 modelGateway 的实时可用列表 | gqlerror 400 "models not available on modelGateway X: [..stale..]" |
| `gatewayAvailableModels` gatewayConnectionId 解析失败 | gqlerror 400 "invalid gatewayConnectionId" |
| `gatewayAvailableModels` LiteLLM /model/list 失败 (网络/4xx/5xx) | 透传 + 502 |
| `issueVirtualKey` agentId 已被其他 active key 占用 | gqlerror 409 "agent already has active key" |
| `issueVirtualKey` LiteLLM mint 失败 | 透传 + audit "key.issue" fail |
| `issueVirtualKey` DB Save 失败 (含部分唯一索引冲突) | gw.DeleteKey 补偿 + audit "key.issue" fail |
| `regenerateVirtualKey` DB Save 失败 | context.WithoutCancel 不变 + audit "key.regenerate" fail |
| `revokeVirtualKey` LiteLLM delete 失败 | 整操作 fail, DB 不动 + audit "key.revoke" fail |
| `associateVirtualKeyAgent` agentId 已绑其他 active key | gqlerror 409 |
| `associateVirtualKeyAgent` 唯一索引 race | 捕 ent 唯一约束 err 转 409 |
| `virtualKeys(organizationId, agentId)` 跨租户访问 | tenant 隔离 denyAll (走现有 LLD-10 B-class), 返空列表 |
| `setVirtualKeyEnabled` on revoked key | gqlerror 400 "key is revoked" |
| `regenerateVirtualKey` on revoked key | gqlerror 400 "key is revoked and cannot be regenerated" |
| `duration` 与 `expiresAt` 同时给 | duration 优先, expiresAt 被覆盖 (服务端日志记录一次, 不报错) |
| `duration` 解析失败 (非 `^\d+(d|h|w|m)$`) | gqlerror 400 "invalid duration format" |

---

## 6. 验证 (Verification)

CLAUDE.md §1 / §2 明确项目开发阶段不维护/不主动跑 `go test`, 验证主要由 CI gate + 手跑命令组成.

### 6.1 必跑的本地命令 (PR 推送前)

```bash
GOTOOLCHAIN=go1.25.0 go generate ./ent/...    # 重新生成 ent 代码
GOTOOLCHAIN=go1.25.0 go run -mod=mod github.com/99designs/gqlgen generate    # 重新生成 gqlgen
GOTOOLCHAIN=go1.25.0 gofmt -l .                                         # 必须空 (CLAUDE.md §3)
GOTOOLCHAIN=go1.25.0 go vet ./...                                        # 必须 0 错
GOTOOLCHAIN=go1.25.0 go build ./...                                      # 必须成功
make migrate-diff name=vk_per_agent_org                                  # 必须生成唯一 SQL
make docs                                                                 # 同步 docs/schema.graphql + docs/api/virtual-keys.md
```

### 6.2 CI gate 必过

CLAUDE.md §2: 不可绕过.
- `gofmt` (drift)
- `migration-drift` (ent schema 改后必过)
- `docs-check`
- `go vet`
- `build`

### 6.3 不写测试

CLAUDE.md §1: 开发期不维护 `*_test.go`. 不写新测试, 不动现有 `*_test.go` (现有 `virtualkey_policy_test.go` 等如不兼容 mappers 改名, 暂不动 —— 待维护者显式宣布进入收尾阶段再统一修复).

### 6.4 手动冒烟 (本地 dev mode, 不强求)

- admin role 进控制台
- issue 一个 key (organizationId 必须), 看返回 secret + maskedKey
- virtualKeys(organizationId) 看返回列表带 maskedKey (无 secret)
- associateVirtualKeyAgent 绑 agent
- revoke 一次, 确认 gateway 收到 /key/delete + DB 翻 status=revoked
- regenerate 一次, 确认返回新 secret, maskedKey 同步更新

---

## 7. 范围之外 (Out of Scope)

- **(2026-07 补充) 前端表单改造**: agent-platform-console 的 `VirtualKeyFormModal.vue` 从「选 ModelRoute 别名」改为「先选 modelGateway → 拉 gatewayAvailableModels → 多选 models」. 这是兄弟仓 PR (前端). 本仓 PR 不动前端.
- `maskedKey` 加在 DeployedAgent.virtualKeySecret (LLD-13 §3.3 部署返回) 上 —— 留作后续 PR. 本次 PR 不改 deploy.
- 限制 organization_id 必须是预存在 platform org —— 当前平台无 org 表 (org 是 string), 留作后续. 这一版 issuer 信任调用者.
- Key 自动 rotation 周期 worker (auto_rotate=true 触发) —— 已有字段, 无 worker. 后续.
- 国际化 i18n / duration 多语言展示 —— 留作前端 PR.
- 增强的 rate-limit / quota / RBAC —— 与 RateLimitPolicy 移除的现状一致, 无影响.
- 任何 `*_test.go` 维护 —— CLAUDE.md §1 例外.
- Q3 多租户强隔离 (LLD-10 B-class 与 organization_id 的对齐) —— 已有 `tenantScopeFor` 兜底, 本次 PR 不重写, P0 follow-up.

---

## 8. 关键风险与缓解

| 风险 | 缓解 |
| --- | --- |
| `feat/model-routing` 分支有独立代码 (RateLimitPolicy 移除 + 清理), 重 generate 可能冲突 | 提交前 rebase main + 重新 generate, 不在同一个 PR 里混 |
| `generated.go` diff 巨大 (任何 GraphQL change 都这样) | PR 描述中标注 "generated" 让 reviewer 跳过对生成物的 review |
| `associatedVirtualKeyAgent` 与现有 `RegenerateVirtualKey` 行为冲突: regenerate 不改 agent_id (regen 是 secret rotate, 不是 governance change) | regenerate 的注释 §3.2 明确 "regenerate 不改 agent_id"; rename `associateVirtualKeyAgent` 强调这是 governance 操作 |
| tenant_scope 与 organizationId 对接缺失, 当前版本 "denyAll" → 列表返空, 不恶化 | PR 注释里加 FIXME + TODO, 后续 PR 强化 |
| 用户已经传过来的旧字段 (userId / teamId) 在 GraphQL 直接报 unknown field | GraphQL 客户端会立即发现. 字段硬删 (不 preserve). 前端需同步 |
| `masked_key` 与 `litellm_key` 错位 (regenerate 后) | regenerate resolver 一行 `SetMaskedKey(newMasked)`, 必须在同一 Save 中, 不可分两步 |
| duration "30d" / "30D" / "30 days" 等多种表达 | 接受 strict 格式 `^\d+(d|h|w|m)$`, 其余 400. 不做 fuzzy |

---

## 9. 关键文件清单 (实施时动这些)

| 文件 | 改动类型 |
| --- | --- |
| [ent/schema/virtualkey.go](ent/schema/virtualkey.go) | 字段重命名/增删 |
| [schema/virtualkey.graphql](schema/virtualkey.graphql) | 类型/input/operations 重写 |
| [schema/schema.graphql](schema/schema.graphql) | 自动 (gqlgen 重新生成) |
| [internal/graph/virtualkey.resolvers.go](internal/graph/virtualkey.resolvers.go) | 4 个 resolver + 1 个新 resolver |
| [internal/graph/mappers.go](internal/graph/mappers.go) | `toModelVirtualKey` 改字段 |
| [internal/graph/model/models_gen.go](internal/graph/model/models_gen.go) | 自动 (gqlgen 重新生成) |
| [internal/graph/generated.go](internal/graph/generated.go) | 自动 (gqlgen) |
| [internal/gateway/client.go](internal/gateway/client.go) | 加 `redactKey` utility |
| [internal/graph/account_helpers.go](internal/graph/account_helpers.go) | 删 `revokeUserKeys` |
| [internal/graph/account.resolvers.go](internal/graph/account.resolvers.go) | `DeleteUser` 取消级联调用 |
| [internal/graph/tenant_scope.go](internal/graph/tenant_scope.go) | 注释 + logic 与 org 对齐 (TODO/FIXME) |
| [internal/graph/gateway_resolve.go](internal/graph/gateway_resolve.go) | 注释 + 调用方传 OrganizationID |
| [ent/migrate/schema.go](ent/migrate/schema.go) | 自动 (`make migrate-diff name=vk_per_agent_org`) |
| [ent/migrate/migrations/atlas.sum](ent/migrate/migrations/atlas.sum) | 自动 |
| [docs/api/virtual-keys.md](docs/api/virtual-keys.md) | 类型/字段表重写 (手动同步 `make docs`) |
| [docs/schema.graphql](docs/schema.graphql) | 自动 (`make docs`) |
| [postman/](postman/) collection JSON | 删除旧字段示例 + 加 associateVirtualKeyAgent 示例 + 加 gatewayAvailableModels 调用示例 |
| [internal/graph/modelgateway.resolvers.go](internal/graph/modelgateway.resolvers.go) | 抽取 `buildModelGatewayFromConn` helper 给 §3.7 复用 |
| `internal/gql/testdata/client_operations/*.graphql` | 与新 schema 同步 (用于 auto-gen postman / 客户端代码) |

---

## 10. 验证步骤 (End-to-End 提交前必过)

1. `GOTOOLCHAIN=go1.25.0 go generate ./ent/...` → ent 代码重生成成功
2. `GOTOOLCHAIN=go1.25.0 go run -mod=mod github.com/99designs/gqlgen generate` → generated.go + models_gen.go 重生成成功
3. `GOTOOLCHAIN=go1.25.0 gofmt -l .` → 输出空
4. `GOTOOLCHAIN=go1.25.0 go vet ./...` → 0 错
5. `GOTOOLCHAIN=go1.25.0 go build ./...` → 成功
6. `make migrate-diff name=vk_per_agent_org` → 生成新 migration, 仅本次相关列变化 (alias→name, user_id/team_id drop, organization_id not null, masked_key add)
7. `make docs` → docs/api/virtual-keys.md 与 docs/schema.graphql 同步
8. push → CI 全绿 (gofmt, migration-drift, docs-check, vet, build)
9. 本地 dev mode 冒烟 (CLI 用法参见 §6.4)
