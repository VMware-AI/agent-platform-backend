# Model Gateway & Routing

[← API Reference index](./README.md)

> Source: `schema/modelgateway.graphql`, `schema/gateway-routing.graphql`

## Queries

### `modelGateways`

page is the shared PageInput (limit/offset) defined alongside audit/observability.

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

### `testModelGatewayConnection`

```graphql
testModelGatewayConnection(id: ID!): ModelGatewayTestResult!
```

- **Returns:** `ModelGatewayTestResult!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

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
| `loadBalanceStrategy` | `LoadBalanceStrategy!` | — |
| `createdAt` | `Time!` | — |

### ModelGateway

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `provider` | `ModelGatewayProvider!` | — |
| `endpoint` | `String!` | — |
| `status` | `ModelGatewayStatus!` | — |
| `backendModelCount` | `Int!` | — |
| `loadBalancingStrategy` | `LoadBalancingStrategy` | — |
| `latencyMs` | `Int` | — |
| `adminUrl` | `String` | — |
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

| Field | Type | Description |
|-------|------|-------------|
| `success` | `Boolean!` | — |
| `status` | `ModelGatewayStatus!` | — |
| `latencyMs` | `Int` | — |
| `message` | `String!` | — |
| `testedAt` | `Time!` | — |
| `gateway` | `ModelGateway!` | — |
| `loadBalancingStrategy` | `LoadBalancingStrategy` | — |

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
| `strategy` | `LoadBalanceStrategy!` | — |
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
| `status` | `ModelGatewayStatus` | — |

### ModelGatewayInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `provider` | `ModelGatewayProvider!` | — |
| `endpoint` | `String!` | — |
| `adminUrl` | `String` | — |
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
| `loadBalanceStrategy` | `LoadBalanceStrategy` | — |
| `publicUrl` | `String` | The URL provisioned VMs call (LLD-13 §3.3); omitted → falls back to endpoint. |
| `isDefault` | `Boolean` | Mark this the platform default gateway; setting true clears the flag on any other. |

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
| `strategy` | `LoadBalanceStrategy` | — |
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

### LoadBalanceStrategy

*Enum*

| Value | Description |
|-------|-------------|
| `simple_shuffle` | — |
| `latency` | — |
| `usage_v2` | — |
| `least_busy` | — |
| `cost` | — |

### LoadBalancingStrategy

*Enum*

| Value | Description |
|-------|-------------|
| `ROUND_ROBIN` | — |
| `LATENCY_BASED` | — |
| `USAGE_BASED_V2` | — |
| `LEAST_BUSY` | — |
| `COST_BASED` | — |

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
| `STATUS` | — |
| `CREATED_AT` | — |
| `UPDATED_AT` | — |

### ModelGatewayStatus

*Enum*

| Value | Description |
|-------|-------------|
| `CONNECTED` | — |
| `DISCONNECTED` | — |
| `ERROR` | — |

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
