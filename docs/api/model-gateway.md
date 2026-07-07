# Model Gateway & Routing

[← API Reference index](./README.md)

> Source: `schema/modelgateway.graphql`, `schema/gateway-routing.graphql`

## Queries

### `modelGateways`

page is the shared PageInput (limit/offset) defined alongside audit/read_only.

```graphql
modelGateways(filter: ModelGatewayFilterInput, page: PageInput!, sort: ModelGatewaySort): ModelGatewayConnection!
```

- **Returns:** `ModelGatewayConnection!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `filter` | `ModelGatewayFilterInput` | no | — |
| `page` | `PageInput!` | yes | — |
| `sort` | `ModelGatewaySort` | no | — |

### `modelGatewaySyncSummary`

```graphql
modelGatewaySyncSummary: ModelGatewaySyncSummary!
```

- **Returns:** `ModelGatewaySyncSummary!`
- **Auth:** `@hasRole(any: [admin])`

### `gatewayConnections`

```graphql
gatewayConnections: [GatewayConnection!]!
```

- **Returns:** `[GatewayConnection!]!`
- **Auth:** `@hasRole(any: [admin])`

### `upstreams`

```graphql
upstreams: [Upstream!]!
```

- **Returns:** `[Upstream!]!`
- **Auth:** `@hasRole(any: [admin])`

### `modelRoutes`

Model routes are platform-global gateway config (no tenant_id), so admin-only — tenant-admin must not read/write another tenant's routes (no scoping exists).

```graphql
modelRoutes: [ModelRoute!]!
```

- **Returns:** `[ModelRoute!]!`
- **Auth:** `@hasRole(any: [admin])`

### `routerTiers`

```graphql
routerTiers: [RouterTier!]!
```

- **Returns:** `[RouterTier!]!`
- **Auth:** `@hasRole(any: [admin])`

## Mutations

### `createModelGateway`

```graphql
createModelGateway(input: ModelGatewayInput!): ModelGateway!
```

- **Returns:** `ModelGateway!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `ModelGatewayInput!` | yes | — |

### `updateModelGateway`

```graphql
updateModelGateway(id: ID!, input: ModelGatewayInput!): ModelGateway!
```

- **Returns:** `ModelGateway!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `input` | `ModelGatewayInput!` | yes | — |

### `deleteModelGateway`

```graphql
deleteModelGateway(id: ID!): DeleteModelGatewayPayload!
```

- **Returns:** `DeleteModelGatewayPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `syncModelGatewayConnection`

同步一个已存在的 gateway: 探测连通性 + 路由策略 + 后端模型数, 写回 ent 列. gateway 字段返回同步后的最新状态, 业务信息从 gateway 内部读取.

```graphql
syncModelGatewayConnection(id: ID!): ModelGatewaySyncResult!
```

- **Returns:** `ModelGatewaySyncResult!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `testNewModelGatewayConnection`

Pre-create dry-run probe. No row is created or modified; the result's `gateway` field is null. Strategy probe is intentionally skipped (dry-run 只测连通性, 不拉 /config/router 也不解析 /models 的 data 数组).

```graphql
testNewModelGatewayConnection(input: TestModelGatewayConnectionInput!): ModelGatewayTestResult!
```

- **Returns:** `ModelGatewayTestResult!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `TestModelGatewayConnectionInput!` | yes | — |

### `registerGatewayConnection`

```graphql
registerGatewayConnection(input: RegisterGatewayConnectionInput!): GatewayConnection!
```

- **Returns:** `GatewayConnection!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `RegisterGatewayConnectionInput!` | yes | — |

### `testGatewayConnection`

```graphql
testGatewayConnection(id: ID!): GatewayStatus!
```

- **Returns:** `GatewayStatus!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `deleteGatewayConnection`

```graphql
deleteGatewayConnection(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `upsertUpstream`

```graphql
upsertUpstream(input: UpsertUpstreamInput!): Upstream!
```

- **Returns:** `Upstream!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `UpsertUpstreamInput!` | yes | — |

### `deleteUpstream`

```graphql
deleteUpstream(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `upsertModelRoute`

Model routes are platform-global gateway config (no tenant_id) → admin-only, mirroring modelGateways. tenant-admin holds "route:manage" but has no tenant scoping here, so a permission gate would leak cross-tenant read/write.

```graphql
upsertModelRoute(input: UpsertModelRouteInput!): ModelRoute!
```

- **Returns:** `ModelRoute!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `UpsertModelRouteInput!` | yes | — |

### `createModelRoute`

Console 模型路由 page CRUD (创建/编辑路由). create mints a new route by id; update edits an existing one by id (distinct from the name-keyed upsert above).

```graphql
createModelRoute(input: CreateModelRouteInput!): ModelRoute!
```

- **Returns:** `ModelRoute!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `CreateModelRouteInput!` | yes | — |

### `updateModelRoute`

```graphql
updateModelRoute(id: ID!, input: UpdateModelRouteInput!): ModelRoute!
```

- **Returns:** `ModelRoute!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `input` | `UpdateModelRouteInput!` | yes | — |

### `setModelRouteEnabled`

```graphql
setModelRouteEnabled(id: ID!, enabled: Boolean!): ModelRoute!
```

- **Returns:** `ModelRoute!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `enabled` | `Boolean!` | yes | — |

### `deleteModelRoute`

```graphql
deleteModelRoute(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `setRouterTier`

The difficulty router: map a tier to a model alias → syncs litellm Complexity Router.

```graphql
setRouterTier(tier: RouterTierLevel!, modelAlias: String!): RouterTier!
```

- **Returns:** `RouterTier!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `tier` | `RouterTierLevel!` | yes | — |
| `modelAlias` | `String!` | yes | — |

## Types

### DeleteModelGatewayPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `deletedID` | `ID!` | — |

### GatewayConnection

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `endpoint` | `String!` | — |
| `publicUrl` | `String` | The URL provisioned VMs/agents call (LLD-13 §3.3). Falls back to endpoint when unset. |
| `isDefault` | `Boolean!` | The platform default gateway — used for ops with no department context. At most one. |
| `status` | `GatewayStatus!` | — |
| `loadBalanceStrategy` | `LoadBalancingStrategy!` | — |
| `createdAt` | `Time!` | — |

### ModelGateway

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `provider` | `ModelGatewayProvider!` | — |
| `endpoint` | `String!` | — |
| `backendModelCount` | `Int!` | — |
| `loadBalancingStrategy` | `LoadBalancingStrategy` | — |
| `lastSyncAt` | `Time` | — |
| `lastSyncStatus` | `ModelGatewaySyncState!` | — |
| `lastSyncMessage` | `String` | — |
| `createdAt` | `Time!` | — |
| `updatedAt` | `Time!` | — |

### ModelGatewayConnection

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `nodes` | `[ModelGateway!]!` | — |
| `totalCount` | `Int!` | — |

### ModelGatewaySyncResult

*Object*

syncModelGatewayConnection 的返回值：gateway 必返（非 null），所有策略/状态/ lastSyncAt/backendModelCount 等信息都从 gateway 内部字段读取。

| Field | Type | Description |
|-------|------|-------------|
| `success` | `Boolean!` | — |
| `message` | `String!` | — |
| `gateway` | `ModelGateway!` | — |

### ModelGatewaySyncSummary

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `state` | `ModelGatewaySyncState!` | — |
| `lastSyncedAt` | `Time` | — |
| `successCount` | `Int!` | — |
| `failedCount` | `Int!` | — |
| `message` | `String` | — |

### ModelGatewayTestResult

*Object*

testNewModelGatewayConnection (dry-run, pre-create) 的返回值：仅三字段，不带 gateway。

| Field | Type | Description |
|-------|------|-------------|
| `success` | `Boolean!` | — |
| `message` | `String!` | — |
| `testedAt` | `Time!` | — |

### ModelRoute

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `modelAlias` | `String!` | — |
| `backendGatewayId` | `ID` | — |
| `gatewayName` | `String!` | Display name of the serving gateway (console 模型路由 list). |
| `upstreams` | `[String!]!` | — |
| `supportedModels` | `[String!]!` | Console alias for `upstreams` — the models this route can serve (模型路由 page). |
| `strategy` | `LoadBalancingStrategy!` | — |
| `uiStrategy` | `ModelRouteStrategy!` | Console-facing load-balancing strategy (模型路由 page). |
| `enabled` | `Boolean!` | — |
| `createdAt` | `Time!` | — |
| `updatedAt` | `Time!` | — |

### RouterTier

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `tier` | `RouterTierLevel!` | — |
| `modelAlias` | `String!` | — |

### Upstream

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `provider` | `UpstreamProvider!` | — |
| `apiBase` | `String` | — |
| `model` | `String!` | — |
| `enabled` | `Boolean!` | — |
| `createdAt` | `Time!` | — |

### CreateModelRouteInput

*Input*

Console 模型路由 create form (创建路由). modelAlias defaults to name when omitted; supportedModels are stored as the route's upstream group.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `backendGatewayId` | `ID` | — |
| `gatewayName` | `String` | — |
| `supportedModels` | `[String!]` | — |
| `uiStrategy` | `ModelRouteStrategy` | — |
| `enabled` | `Boolean` | — |

### ModelGatewayFilterInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `search` | `String` | — |

### ModelGatewayInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `provider` | `ModelGatewayProvider!` | — |
| `endpoint` | `String!` | — |
| `masterKey` | `String` | litellm master key(接入表单填写)→ 后端写 secret store,只存引用,明文不落库。 |

### ModelGatewaySort

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `field` | `ModelGatewaySortField!` | — |
| `direction` | `SortDirection!` | — |

### RegisterGatewayConnectionInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `endpoint` | `String!` | — |
| `masterKey` | `String` | litellm master key(接入表单填写)→ 后端写 secret store,只存引用,明文不落库; 优先于 masterKeyRef。 |
| `masterKeyRef` | `String` | — |
| `loadBalanceStrategy` | `LoadBalancingStrategy` | — |
| `publicUrl` | `String` | The URL provisioned VMs call (LLD-13 §3.3); omitted → falls back to endpoint. |
| `isDefault` | `Boolean` | Mark this the platform default gateway; setting true clears the flag on any other. |

### TestModelGatewayConnectionInput

*Input*

Pre-create test input — the form-level "Test Connection" button on the 接入表单 uses this to ping a not-yet-persisted gateway config. Carries the minimal fields the probe needs: endpoint + masterKey. (name, provider are either fixed or irrelevant to the live test.)

| Field | Type | Description |
|-------|------|-------------|
| `endpoint` | `String!` | — |
| `masterKey` | `String!` | — |

### UpdateModelRouteInput

*Input*

Console 模型路由 edit form (编辑路由). All fields optional — only set ones change.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String` | — |
| `backendGatewayId` | `ID` | — |
| `gatewayName` | `String` | — |
| `supportedModels` | `[String!]` | — |
| `uiStrategy` | `ModelRouteStrategy` | — |
| `enabled` | `Boolean` | — |

### UpsertModelRouteInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `modelAlias` | `String!` | — |
| `backendGatewayId` | `ID` | — |
| `upstreams` | `[String!]` | — |
| `strategy` | `LoadBalancingStrategy` | — |
| `enabled` | `Boolean` | — |

### UpsertUpstreamInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `provider` | `UpstreamProvider!` | — |
| `apiBase` | `String` | — |
| `apiKey` | `String` | 上游 API key(上游表单填写)→ 后端写 secret store,只存引用,明文不落库; 优先于 apiKeyRef。 |
| `apiKeyRef` | `String` | — |
| `model` | `String!` | — |
| `enabled` | `Boolean` | — |

### GatewayStatus

*Enum*

| Value | Description |
|-------|-------------|
| `connected` | — |
| `disconnected` | — |
| `error` | — |

### LoadBalancingStrategy

*Enum*

| Value | Description |
|-------|-------------|
| `SIMPLE_SHUFFLE` | — |
| `LEAST_BUSY` | — |
| `LATENCY_BASED_ROUTING` | — |
| `USAGE_BASED_ROUTING_V2` | — |
| `COST_BASED_ROUTING` | — |

### ModelGatewayProvider

*Enum*

| Value | Description |
|-------|-------------|
| `LITELLM` | — |

### ModelGatewaySortField

*Enum*

| Value | Description |
|-------|-------------|
| `NAME` | — |
| `ENDPOINT` | — |
| `CREATED_AT` | — |
| `UPDATED_AT` | — |

### ModelGatewaySyncState

*Enum*

| Value | Description |
|-------|-------------|
| `SYNCED` | — |
| `SYNCING` | — |
| `PARTIAL` | — |
| `FAILED` | — |
| `NEVER` | — |

### ModelRouteStrategy

*Enum*

Console-facing load-balancing strategy for a model route (模型路由 page). Distinct from the litellm LoadBalanceStrategy: a friendly, gateway-agnostic choice the operator picks in the UI and the backend round-trips verbatim.

| Value | Description |
|-------|-------------|
| `ROUND_ROBIN` | — |
| `WEIGHTED_ROUND_ROBIN` | — |
| `RANDOM` | — |

### RouterTierLevel

*Enum*

| Value | Description |
|-------|-------------|
| `SIMPLE` | — |
| `MEDIUM` | — |
| `COMPLEX` | — |
| `REASONING` | — |

### UpstreamProvider

*Enum*

| Value | Description |
|-------|-------------|
| `vllm` | — |
| `openai` | — |
| `anthropic` | — |
| `minimax` | — |
| `codex` | — |
