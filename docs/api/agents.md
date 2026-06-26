# Agents, Templates & Snapshots

[← API Reference index](./README.md)

> Source: `schema/agent.graphql`, `schema/deploy.graphql`

## Queries

### `agentTemplates`

Catalog and configs are browsable by any authenticated user.

```graphql
agentTemplates: [AgentTemplate!]!
```

- **Returns:** `[AgentTemplate!]!`
- **Auth:** authenticated (no directive)

### `agentConfigs`

```graphql
agentConfigs(agentType: String): [AgentConfig!]!
```

- **Returns:** `[AgentConfig!]!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `agentType` | `String` | no | — |

### `agents`

Admin sees all agents; tenant-admin their tenant; a regular user only their own (owner scope). Paged/filtered/sorted connection (前后端整合契约).

```graphql
agents(filter: AgentFilter, pagination: Pagination, sort: AgentSort): AgentConnection!
```

- **Returns:** `AgentConnection!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `filter` | `AgentFilter` | no | — |
| `pagination` | `Pagination` | no | — |
| `sort` | `AgentSort` | no | — |

### `vmTemplates`

List OVA templates in a resource pool's vCenter (powers the deploy form).

```graphql
vmTemplates(resourcePoolId: ID!): [VMTemplate!]!
```

- **Returns:** `[VMTemplate!]!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `resourcePoolId` | `ID!` | yes | — |

### `vsphereResourcePools`

List the placement resource pools in a platform resource pool's vCenter (powers the deploy form's placement picker). A real OVA template has no source pool, so the deploy must pick one of these for targetResourcePool. Admin-only: it dials the pool's vCenter with privileged credentials, like vmTemplates.

```graphql
vsphereResourcePools(resourcePoolId: ID!): [VsphereResourcePool!]!
```

- **Returns:** `[VsphereResourcePool!]!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `resourcePoolId` | `ID!` | yes | — |

### `agentSnapshots`

List the agent VM's snapshots. Owner/admin (checked in resolver).

```graphql
agentSnapshots(agentId: ID!): [AgentSnapshot!]!
```

- **Returns:** `[AgentSnapshot!]!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `agentId` | `ID!` | yes | — |

## Mutations

### `upsertAgentTemplate`

Catalog management is admin-only.

```graphql
upsertAgentTemplate(input: UpsertAgentTemplateInput!): AgentTemplate!
```

- **Returns:** `AgentTemplate!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `UpsertAgentTemplateInput!` | yes | — |

### `createAgent`

Self-service: any authenticated user creates their own agent (owner = caller).

```graphql
createAgent(input: CreateAgentInput!): Agent!
```

- **Returns:** `Agent!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `CreateAgentInput!` | yes | — |

### `setAgentStatus`

```graphql
setAgentStatus(id: ID!, status: AgentStatus!): Agent!
```

- **Returns:** `Agent!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `status` | `AgentStatus!` | yes | — |

### `createAgentConfig`

Agent config management (智能体配置).

```graphql
createAgentConfig(input: CreateAgentConfigInput!): AgentConfig!
```

- **Returns:** `AgentConfig!`
- **Auth:** `@hasRole(any: [admin, tenant_admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `CreateAgentConfigInput!` | yes | — |

### `updateAgentConfig`

```graphql
updateAgentConfig(id: ID!, input: UpdateAgentConfigInput!): AgentConfig!
```

- **Returns:** `AgentConfig!`
- **Auth:** `@hasRole(any: [admin, tenant_admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `input` | `UpdateAgentConfigInput!` | yes | — |

### `deleteAgentConfig`

```graphql
deleteAgentConfig(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin, tenant_admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `setDefaultAgentConfig`

Mark this config the default for its agent type (unsets others of that type).

```graphql
setDefaultAgentConfig(id: ID!): AgentConfig!
```

- **Returns:** `AgentConfig!`
- **Auth:** `@hasRole(any: [admin, tenant_admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `setAgentConfigKnowledge`

Replace the config's mounted OKF knowledge packs (LLD-11 K2). Each id must be a kind=knowledge artifact visible to the caller's tenant; the set is replaced wholesale.

```graphql
setAgentConfigKnowledge(configId: ID!, knowledgeArtifactIds: [ID!]!): AgentConfig!
```

- **Returns:** `AgentConfig!`
- **Auth:** `@hasRole(any: [admin, tenant_admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `configId` | `ID!` | yes | — |
| `knowledgeArtifactIds` | `[ID!]!` | yes | — |

### `deployAgent`

Create a NEW agent from a catalog OVA version and provision its VM. Creates the agent row (kind from the family), issues a gateway key, clones the VM from the version's ovaIdentifier, injects cloud-init, powers on, marks it running. On failure the VM, key and (if needed) the new agent row are rolled back (no orphans). Admin-only: the 智能体市场 (and its OVA catalog queries) are gated to admins, so deploy is consistently admin-only too (owner = the admin caller).

```graphql
deployAgent(input: DeployAgentInput!): DeployedAgent!
```

- **Returns:** `DeployedAgent!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `DeployAgentInput!` | yes | — |

### `recycleAgent`

Owner or admin. Destroys the agent's VM, revokes its key, marks it stopped. confirm must be true (double-confirm on a destructive operation).

```graphql
recycleAgent(input: RecycleAgentInput!): Agent!
```

- **Returns:** `Agent!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `RecycleAgentInput!` | yes | — |

### `snapshotAgent`

Owner or admin. Snapshots the agent's VM (LLD-03 §4).

```graphql
snapshotAgent(input: SnapshotAgentInput!): AgentSnapshot!
```

- **Returns:** `AgentSnapshot!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `SnapshotAgentInput!` | yes | — |

### `revertAgentSnapshot`

Owner or admin. Reverts the agent's VM to a snapshot — DESTRUCTIVE, confirm must be true (discards all state since the snapshot).

```graphql
revertAgentSnapshot(input: RevertAgentSnapshotInput!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `RevertAgentSnapshotInput!` | yes | — |

### `requestRotation`

Owner or admin. Enqueue a credential rotation for the agent's VM (LLD-08); the agent-manager daemon executes it on its next heartbeat. No-op (true) if a rotation of that kind is already in flight.

```graphql
requestRotation(agentId: ID!, kind: RotationKind!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `agentId` | `ID!` | yes | — |
| `kind` | `RotationKind!` | yes | — |

### `revokeAgentEnrollment`

Owner or admin. Revoke the agent VM's bearer credential — its next heartbeat is rejected (LLD-08 §4.4). Idempotent.

```graphql
revokeAgentEnrollment(agentId: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `agentId` | `ID!` | yes | — |

## Types

### Agent

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `type` | `String!` | type = the agent's catalog kind (goose/xiaoguai/…); typeLabel = the template's display name for that kind (前后端整合契约). type replaces the old agentType. |
| `typeLabel` | `String!` | — |
| `status` | `AgentStatus!` | — |
| `apiKey` | `AgentApiKey` | apiKey/owner are resolved lazily from the agent's virtual_key_id / owner_user_id. |
| `owner` | `User` | — |
| `endpoint` | `String` | endpoint = the VM ref (moRef/name); null until deployed. |
| `templateFamilyId` | `ID` | Catalog provenance, set when the agent was deployed from an OVA version (智能体 市场 deploy). Null for agents created directly via createAgent. |
| `templateVersionId` | `ID` | — |
| `resourcePoolId` | `ID` | The vCenter resource pool the agent's VM lives in (set at deploy). Null until deployed. |
| `credentials` | `AgentCredentials` | Run-as credentials for the agent's VM. Currently sources `username` from the owning user (the agent has no separate OS account today); resolver-computed. |
| `createdAt` | `Time!` | — |
| `updatedAt` | `Time!` | — |

### AgentApiKey

*Object*

A virtual gateway key as shown on the agent list (id + display alias).

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |

### AgentConfig

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `agentType` | `String!` | — |
| `isDefault` | `Boolean!` | — |
| `artifactId` | `ID` | The default_config artifact this config pulls (LLD-09 inline content); lets the 智能体配置 edit form preselect the current artifact. Null when none is set. |
| `knowledge` | `[Artifact!]!` | OKF knowledge packs mounted on this config (N:M, LLD-11 K2). Sent to the agent VM at deploy; the daemon pulls each over the control-plane channel (非 RAG). Lazily resolved (loads the edge only when selected). |
| `createdAt` | `Time!` | — |

### AgentConnection

*Object*

Connection wrapper for the paged/filtered/sorted agent list (前后端整合契约).

| Field | Type | Description |
|-------|------|-------------|
| `nodes` | `[Agent!]!` | — |
| `totalCount` | `Int!` | — |
| `pageInfo` | `PageInfo!` | — |

### AgentCredentials

*Object*

Run-as credentials surfaced for a deployed agent. Only `username` is exposed; the password is never returned by the API (it is a Sensitive VM secret).

| Field | Type | Description |
|-------|------|-------------|
| `username` | `String!` | — |

### AgentSnapshot

*Object*

A vCenter snapshot of an agent's VM (LLD-03 §4 生命周期/快照).

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `description` | `String` | — |
| `state` | `String!` | vCenter power state recorded in the snapshot. |
| `createdAt` | `Time!` | — |

### AgentTemplate

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `kind` | `String!` | — |
| `display` | `String!` | — |
| `description` | `String` | — |
| `installMethod` | `InstallMethod!` | — |
| `installCommand` | `String` | — |
| `status` | `AgentTemplateStatus!` | — |
| `version` | `String` | — |
| `knowledgeRoot` | `String` | OKF knowledge-grounding convention per kind (LLD-11 K4). knowledgeRoot = VM unpack dir for mounted packs; knowledgePrompt = the "consult local knowledge index.md first" system-prompt snippet (非 RAG). |
| `knowledgePrompt` | `String` | — |
| `createdAt` | `Time!` | — |

### DeployedAgent

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `agent` | `Agent!` | — |
| `virtualKeySecret` | `String!` | The issued virtual-key secret — returned ONCE. |
| `templateVersion` | `OvaTemplateVersion!` | The catalog version the agent was cloned from + the pool it landed in, so the console can confirm the deployment without a second round-trip. |
| `resourcePool` | `ResourcePool!` | — |

### PageInfo

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `page` | `Int!` | — |
| `pageSize` | `Int!` | — |
| `totalPages` | `Int!` | — |

### VMTemplate

*Object*

An OVA template VM available to clone agents from.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `uuid` | `String!` | — |

### VsphereResourcePool

*Object*

A vCenter resource pool offered as a placement target for the cloned VM. A true OVA template has NO source resource pool, so a real deploy must pick one of these (its `path`) for DeployAgentInput.targetResourcePool.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | Human label — the pool's base name (e.g. "Resources"). |
| `path` | `String!` | Inventory path that vCenter's CloneFromTemplate resolves to place the clone (e.g. "/DC0/host/DC0_C0/Resources"). Full path so it stays unambiguous across multiple datacenters. This is the value to send as targetResourcePool. |

### AgentFilter

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `status` | `AgentStatus` | — |
| `type` | `String` | = agent kind |
| `nameKeyword` | `String` | substring match on name |
| `keyKeyword` | `String` | substring match on apiKey.name (virtual_key alias) |
| `ownerKeyword` | `String` | substring match on owner.username / owner.email |

### AgentSort

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `field` | `AgentSortField!` | — |
| `direction` | `SortDirection!` | — |

### CreateAgentConfigInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `agentType` | `String!` | — |
| `isDefault` | `Boolean` | — |
| `artifactId` | `ID` | — |

### CreateAgentInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `agentType` | `String!` | — |
| `configId` | `ID` | — |
| `resourcePoolId` | `ID` | — |

### DeployAgentInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | Display name for the new agent (and its cloned VM). |
| `templateFamilyId` | `ID!` | The OVA template family (its `type` becomes the agent's kind) and the specific version to clone from (its `ovaIdentifier` is the source template). |
| `templateVersionId` | `ID!` | — |
| `resourcePoolId` | `ID!` | Target vCenter resource pool to place the clone in. |
| `departmentId` | `ID` | Department whose gateway issues the agent's virtual key + whose gateway public_url is baked into cloud-init (LLD-13 §3.3). Omitted → platform default gateway. |
| `targetResourcePool` | `String` | Optional vSphere resource-pool name to place the VM clone in. A true OVA template has NO source resource pool, so vCenter's CloneFromTemplate requires an explicit placement pool for real deploys ("source has no resource pool; specify resourcePool"). Empty = inherit the source template's pool (only works when the source is a regular VM, e.g. vcsim). Optional to keep the contract backward-compatible. |
| `hostname` | `String` | Optional cloud-init hostname for the VM (defaults to none). |
| `maxBudget` | `Float` | Optional per-key spend cap handed to the gateway when issuing the agent's key. |

### Pagination

*Input*

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `page` | `Int!` | `1` | — |
| `pageSize` | `Int!` | `10` | — |

### RecycleAgentInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `agentId` | `ID!` | — |
| `confirm` | `Boolean!` | Destructive op double-confirm — must be true. |

### RevertAgentSnapshotInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `agentId` | `ID!` | — |
| `snapshotName` | `String!` | — |
| `confirm` | `Boolean!` | Destructive op double-confirm — must be true (discards state since the snapshot). |

### SnapshotAgentInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `agentId` | `ID!` | — |
| `name` | `String!` | — |
| `description` | `String` | — |

### UpdateAgentConfigInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String` | — |
| `artifactId` | `ID` | — |

### UpsertAgentTemplateInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `kind` | `String!` | — |
| `display` | `String!` | — |
| `description` | `String` | — |
| `installMethod` | `InstallMethod!` | — |
| `installCommand` | `String` | — |
| `status` | `AgentTemplateStatus!` | — |
| `version` | `String` | — |
| `knowledgeRoot` | `String` | OKF grounding convention (LLD-11 K4); operators may override the seeded defaults. |
| `knowledgePrompt` | `String` | — |

### AgentSortField

*Enum*

| Value | Description |
|-------|-------------|
| `NAME` | — |
| `TYPE` | — |
| `STATUS` | — |
| `API_KEY_NAME` | — |
| `OWNER` | — |
| `CREATED_AT` | — |
| `UPDATED_AT` | — |

### AgentStatus

*Enum*

| Value | Description |
|-------|-------------|
| `provisioning` | — |
| `running` | — |
| `stopped` | — |
| `exception` | — |

### AgentTemplateStatus

*Enum*

| Value | Description |
|-------|-------------|
| `active` | — |
| `deferred` | — |

### InstallMethod

*Enum*

| Value | Description |
|-------|-------------|
| `offline_tar` | — |
| `curl` | — |
| `unset` | — |

### RotationKind

*Enum*

| Value | Description |
|-------|-------------|
| `rotate_ui_password` | — |
| `rotate_os_password` | — |

### SortDirection

*Enum*

| Value | Description |
|-------|-------------|
| `ASC` | — |
| `DESC` | — |
