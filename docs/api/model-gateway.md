# Model Gateways (Connections)

[← API Reference index](./README.md)

> Source: `schema/modelgateway.graphql`

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

### `modelGatewayById`

0.1.x: 单条 lookup,用于 ProviderModel 表单 dropdown / 详情展示;masterKey 不暴露。

```graphql
modelGatewayById(id: ID!): ModelGateway!
```

- **Returns:** `ModelGateway!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

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

## Types

### DeleteModelGatewayPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `deletedID` | `ID!` | — |

### ModelGateway

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `provider` | `ModelGatewayProvider!` | — |
| `endpoint` | `String!` | — |
| `publicUrl` | `String` | — |
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
| `publicUrl` | `String` | Agent/VM reachable LiteLLM URL. endpoint is for backend control-plane access. |
| `masterKey` | `String` | litellm master key(接入表单填写)→ 后端写 secret store,只存引用,明文不落库。 |

### ModelGatewaySort

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `field` | `ModelGatewaySortField!` | — |
| `direction` | `SortDirection!` | — |

### TestModelGatewayConnectionInput

*Input*

Pre-create test input — the form-level "Test Connection" button on the 接入表单 uses this to ping a not-yet-persisted gateway config. Carries the minimal fields the probe needs: endpoint + masterKey. (name, provider are either fixed or irrelevant to the live test.)

| Field | Type | Description |
|-------|------|-------------|
| `endpoint` | `String!` | — |
| `masterKey` | `String!` | — |

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
