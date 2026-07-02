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

### `contentLibraries`

List all content libraries available on the given resource pool's vCenter. Used by the Add OVA Template dialog to populate the library picker.

```graphql
contentLibraries(resourcePoolId: ID!): [String!]!
```

- **Returns:** `[String!]!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `resourcePoolId` | `ID!` | yes | — |

### `contentLibraryItems`

List OVA items in the named content library of the given resource pool. Used by the Add OVA Template dialog to populate the template picker.

```graphql
contentLibraryItems(resourcePoolId: ID!, libraryName: String!): [ContentLibraryItem!]!
```

- **Returns:** `[ContentLibraryItem!]!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `resourcePoolId` | `ID!` | yes | — |
| `libraryName` | `String!` | yes | — |

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

Connect → fetch inventory tree → persist (同步数据).

```graphql
syncResourcePool(id: ID!): SyncResourcePoolPayload!
```

- **Returns:** `SyncResourcePoolPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

## Types

### Cluster

*Object*

vSphere cluster (parented under a Datacenter's host folder).

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `path` | `String!` | — |
| `esxiHosts` | `[PlacementRef!]!` | — |
| `resourcePools` | `[PlacementRef!]!` | — |

### ContentLibraryItem

*Object*

A single item (OVF/OVA package) inside a vCenter content library. Name is used directly as ovaIdentifier when creating an OvaTemplateVersion.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `type` | `String!` | — |

### CreateResourcePoolPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `pool` | `ResourcePool!` | — |

### DataCenter

*Object*

vSphere datacenter — top-level node of vCenter inventory. storagePolicies is a non-null list: a failed PBM pull and "no profiles" both surface as [] (the null-vs-[] distinction was never wired through; make the field nullable in a dedicated contract change if the console ever needs to tell them apart — see #98).

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `path` | `String!` | — |
| `clusters` | `[Cluster!]!` | — |
| `datastores` | `[PlacementRef!]!` | — |
| `networks` | `[PlacementRef!]!` | — |
| `folders` | `[PlacementRef!]!` | — |
| `storagePolicies` | `[PlacementRef!]!` | — |

### DeleteResourcePoolPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `deletedName` | `String!` | — |

### PlacementRef

*Object*

vCenter deployment candidate resource — minimum information for an OVA deployment target. Name is the vCenter display label; Path is the full inventory path (e.g. /DC0/host/DC0_C0/Resources) used by find.NewFinder. Path may be null when the resource is unambiguously identified by name.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `path` | `String` | — |

### ResourcePool

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `endpoint` | `String!` | — |
| `contentLibraryName` | `String!` | Content library the pool deploys OVA templates from (console 接入表单). |
| `insecure` | `Boolean!` | Skip vCenter TLS verification for this pool (self-signed/internal CA). LLD-13. |
| `datacenters` | `[DataCenter!]!` | vCenter inventory snapshot — full nested tree (DC > Cluster > Host > RP plus datastores / networks / vm folders / storage policies). Synced by the background ticker; consumed by the OVA deploy form for cascading dropdowns. |
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
| `vSphereVersion` | `String!` | 真实 vSphere 版本(带凭证探测时);仅可达性探测时为空字符串。 |
| `contentLibraries` | `[String!]!` | vCenter 上所有内容库名称列表(带凭证探测时返回);仅可达性探测时为空数组。 |

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
| `syncStatus` | `ResourcePoolSyncState` | — |

### ResourcePoolSort

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `field` | `ResourcePoolSortField!` | — |
| `direction` | `SortDirection!` | — |

### TestResourcePoolConnectionInput

*Input*

Pre-save probe for the 接入表单. When username/password are supplied it performs a REAL authenticated probe (login → read vSphere version → verify the content library exists and count its items); without credentials it falls back to a lightweight reachability (TCP) check, as before. Credentials are used only for the probe — never persisted here (createResourcePool stores them).

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `endpoint` | `String!` | — |
| `username` | `String` | vCenter 凭据。提供时执行真实认证探测并返回内容库列表;省略则退化为仅可达性 TCP 拨测(向后兼容)。 明文不落库、不入日志。 |
| `password` | `String` | — |
| `insecure` | `Boolean` | 跳过 vCenter TLS 验证(自签名/内网 CA);省略 = false。与 CreateResourcePoolInput 一致。 |

### UpdateResourcePoolInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String` | — |
| `endpoint` | `String` | — |
| `contentLibraryName` | `String` | — |
| `insecure` | `Boolean` | 跳过 vCenter TLS 验证(自签名/内网 CA);省略 = 不变。LLD-13。 |
| `username` | `String` | 重填凭据(轮换):同 create,写 secret store 后只存引用。 |
| `password` | `String` | — |
| `secretRef` | `String` | — |

### ResourcePoolSortField

*Enum*

| Value | Description |
|-------|-------------|
| `NAME` | — |
| `ENDPOINT` | — |
| `SYNC_STATUS` | — |
| `CREATED_AT` | — |
| `UPDATED_AT` | — |

### ResourcePoolSyncState

*Enum*

Inventory-sync state. Derived: never synced → NEVER; last sync ok → SYNCED; last sync errored → FAILED. (SYNCING/PARTIAL are part of the console enum but the backend's sync is synchronous, so it never produces them today.)

| Value | Description |
|-------|-------------|
| `SYNCED` | — |
| `SYNCING` | — |
| `PARTIAL` | — |
| `FAILED` | — |
| `NEVER` | — |
