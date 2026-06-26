# Resource Pools & vSphere

[← API Reference index](./README.md)

> Source: `schema/resourcepool.graphql`

## Queries

### `resourcePools`

```graphql
resourcePools(filter: ResourcePoolFilter pagination: Pagination sort: ResourcePoolSort): ResourcePoolConnection!
```

- **Returns:** `ResourcePoolConnection!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `filter` | `ResourcePoolFilter pagination: Pagination sort: ResourcePoolSort` | no | — |

### `resourcePool`

```graphql
resourcePool(id: ID!): ResourcePool
```

- **Returns:** `ResourcePool`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

## Mutations

### `createResourcePool`

```graphql
createResourcePool(input: CreateResourcePoolInput!): CreateResourcePoolPayload!
```

- **Returns:** `CreateResourcePoolPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `CreateResourcePoolInput!` | yes | — |

### `updateResourcePool`

```graphql
updateResourcePool(id: ID!, input: UpdateResourcePoolInput!): UpdateResourcePoolPayload!
```

- **Returns:** `UpdateResourcePoolPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `input` | `UpdateResourcePoolInput!` | yes | — |

### `deleteResourcePool`

```graphql
deleteResourcePool(id: ID!): DeleteResourcePoolPayload!
```

- **Returns:** `DeleteResourcePoolPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `testResourcePoolConnection`

Lightweight pre-save reachability probe from the 接入表单 (no credentials): validate the endpoint is well-formed and dial-reachable (0619 第13页 连接状态).

```graphql
testResourcePoolConnection(input: TestResourcePoolConnectionInput!): ResourcePoolConnectionTest!
```

- **Returns:** `ResourcePoolConnectionTest!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `TestResourcePoolConnectionInput!` | yes | — |

### `syncResourcePool`

Connect → count datacenters/clusters/hosts/VMs → persist (同步数据).

```graphql
syncResourcePool(id: ID!): SyncResourcePoolPayload!
```

- **Returns:** `SyncResourcePoolPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

## Types

### CreateResourcePoolPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `pool` | `ResourcePool!` | — |

### DeleteResourcePoolPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `deletedName` | `String!` | — |

### ResourcePool

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `endpoint` | `String!` | — |
| `contentLibraryName` | `String!` | Content library the pool deploys OVA templates from (console 接入表单). |
| `insecure` | `Boolean!` | Skip vCenter TLS verification for this pool (self-signed/internal CA). LLD-13. |
| `connectionStatus` | `PoolConnectionStatus!` | — |
| `datacenterCount` | `Int!` | — |
| `clusterCount` | `Int!` | — |
| `esxiHostCount` | `Int!` | — |
| `vmInstanceCount` | `Int!` | — |
| `syncStatus` | `ResourcePoolSyncState!` | — |
| `lastSyncedAt` | `Time` | — |
| `createdAt` | `Time!` | — |
| `updatedAt` | `Time!` | — |

### ResourcePoolConnection

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `nodes` | `[ResourcePool!]!` | — |
| `totalCount` | `Int!` | — |
| `pageInfo` | `PageInfo!` | — |

### ResourcePoolConnectionDetail

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `vSphereVersion` | `String!` | — |
| `itemCount` | `Int!` | — |

### ResourcePoolConnectionTest

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `ok` | `Boolean!` | — |
| `message` | `String!` | — |
| `detail` | `ResourcePoolConnectionDetail` | — |

### SyncResourcePoolPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `pool` | `ResourcePool!` | — |
| `syncedAt` | `Time!` | — |

### UpdateResourcePoolPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `pool` | `ResourcePool!` | — |

### CreateResourcePoolInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `endpoint` | `String!` | — |
| `contentLibraryName` | `String` | — |
| `datacenterCount` | `Int` | — |
| `clusterCount` | `Int` | — |
| `insecure` | `Boolean` | 跳过 vCenter TLS 验证(自签名/内网 CA);省略 = false(默认验证)。LLD-13。 |
| `username` | `String` | vCenter (JVC) 凭据(可选;真机连接必填,前端表单可后补)。后端写入 secret store (Vaultwarden)并只存返回的引用,明文不落库;优先于 secretRef。 |
| `password` | `String` | — |
| `secretRef` | `String` | 已有 secret store 引用(高级/预置);与 username/password 二选一。 |

### ResourcePoolFilter

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `nameKeyword` | `String` | — |
| `endpointKeyword` | `String` | — |
| `connectionStatus` | `PoolConnectionStatus` | — |

### ResourcePoolSort

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `field` | `ResourcePoolSortField!` | — |
| `direction` | `SortDirection!` | — |

### TestResourcePoolConnectionInput

*Input*

Pre-save reachability probe for the 接入表单 (carries NO credentials): the form checks an endpoint is reachable before the operator commits the pool. A full vCenter inventory probe needs credentials and runs later via syncResourcePool.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `endpoint` | `String!` | — |
| `contentLibraryName` | `String!` | — |

### UpdateResourcePoolInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String` | — |
| `endpoint` | `String` | — |
| `contentLibraryName` | `String` | — |
| `datacenterCount` | `Int` | — |
| `clusterCount` | `Int` | — |
| `insecure` | `Boolean` | 跳过 vCenter TLS 验证(自签名/内网 CA);省略 = 不变。LLD-13。 |
| `username` | `String` | 重填凭据(轮换):同 create,写 secret store 后只存引用。 |
| `password` | `String` | — |
| `secretRef` | `String` | — |

### PoolConnectionStatus

*Enum*

Console connection status is binary (CONNECTED / DISCONNECTED). The ent column keeps a third "error" state for accuracy; the GraphQL projection collapses it to DISCONNECTED (see toModelResourcePool).

| Value | Description |
|-------|-------------|
| `CONNECTED` | — |
| `DISCONNECTED` | — |

### ResourcePoolSortField

*Enum*

| Value | Description |
|-------|-------------|
| `NAME` | — |
| `ENDPOINT` | — |
| `CONNECTION_STATUS` | — |
| `DATACENTER_COUNT` | — |
| `CLUSTER_COUNT` | — |
| `ESXI_HOST_COUNT` | — |
| `VM_INSTANCE_COUNT` | — |
| `CREATED_AT` | — |
| `UPDATED_AT` | — |

### ResourcePoolSyncState

*Enum*

Inventory-sync state, distinct from connectionStatus. Derived: never synced → NEVER; last sync ok → SYNCED; last sync errored → FAILED. (SYNCING/PARTIAL are part of the console enum but the backend's sync is synchronous, so it never produces them today.)

| Value | Description |
|-------|-------------|
| `SYNCED` | — |
| `SYNCING` | — |
| `PARTIAL` | — |
| `FAILED` | — |
| `NEVER` | — |
