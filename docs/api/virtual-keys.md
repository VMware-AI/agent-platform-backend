# Virtual Keys & Rate Limits

[← API Reference index](./README.md)

> Source: `schema/virtualkey.graphql`

## Queries

### `gatewayAvailableModels`

Distinct model names that are bound to the given modelGateway AND have at least one backend physical model in a healthy state (status ∈ {full_healthy, partial_outage}). Sourced from the `provider_models` table — the periodic health-check worker keeps `status` up to date, so this list reflects the operator-console's "what's currently usable" view. Used to populate the issue form's "Models" multi-select after the operator picks a modelGateway. @hasRole: read_only or admin (matches virtualKeys permissioning).

```graphql
gatewayAvailableModels(gatewayConnectionId: ID!): [String!]!
```

- **Returns:** `[String!]!`
- **Auth:** `@hasRole(any: [admin, read_only])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `gatewayConnectionId` | `ID!` | yes | — |

### `virtualKeys`

agentId and modelGateway are independent optional filters; all null → all keys in the current tenant. Multiple set → intersection.

```graphql
virtualKeys(agentId: ID, modelGateway: ID): [VirtualKey!]!
```

- **Returns:** `[VirtualKey!]!`
- **Auth:** `@hasRole(any: [admin, read_only])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `agentId` | `ID` | no | — |
| `modelGateway` | `ID` | no | — |

## Mutations

### `issueVirtualKey`

```graphql
issueVirtualKey(input: IssueVirtualKeyInput!): IssuedVirtualKey!
```

- **Returns:** `IssuedVirtualKey!`
- **Auth:** `@hasPermission(perm: "key:manage")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `IssueVirtualKeyInput!` | yes | — |

### `revokeVirtualKey`

```graphql
revokeVirtualKey(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasPermission(perm: "key:manage")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `regenerateVirtualKey`

Rotate the key's secret at the gateway, keeping its governance row/binding. Returns the new secret ONCE (the old one stops working after litellm's grace). The maskedKey on the returned VirtualKey is updated to match the new secret. LLD-04 §3.

```graphql
regenerateVirtualKey(id: ID!): IssuedVirtualKey!
```

- **Returns:** `IssuedVirtualKey!`
- **Auth:** `@hasPermission(perm: "key:manage")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `setVirtualKeyEnabled`

Toggle enabled/disabled (distinct from revoke, which is terminal).

```graphql
setVirtualKeyEnabled(id: ID!, enabled: Boolean!): VirtualKey!
```

- **Returns:** `VirtualKey!`
- **Auth:** `@hasPermission(perm: "key:manage")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `enabled` | `Boolean!` | yes | — |

### `associateVirtualKeyAgent`

Bind (or rebind) an existing VirtualKey to an agent. Enforces the 1:1 active-key-per-agent invariant (DB partial unique index is the authoritative gate; the resolver also pre-checks for a clean 409).

```graphql
associateVirtualKeyAgent(virtualKeyId: ID!, agentId: ID!): VirtualKey!
```

- **Returns:** `VirtualKey!`
- **Auth:** `@hasPermission(perm: "key:manage")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `virtualKeyId` | `ID!` | yes | — |
| `agentId` | `ID!` | yes | — |

## Types

### IssuedVirtualKey

*Object*

Returned only at issue / regenerate time — carries the secret, which is never queryable again. The virtualKey.maskedKey field is also populated here so the operator sees the preview in the same response.

| Field | Type | Description |
|-------|------|-------------|
| `virtualKey` | `VirtualKey!` | — |
| `secret` | `String!` | — |

### VirtualKey

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | Human-readable label. Required since 2026-07 refactor. |
| `maskedKey` | `String!` | Persistent, safe-to-display preview of the secret (e.g. "sk-aBcD...XyZ"). Always populated; updated alongside any secret change. |
| `modelGateway` | `ModelGateway!` | Nested object: the modelGateway that issued this key. Maps to the ent `model_gateway_id` column (renamed from `gateway_connection_id`). Required since per-agent-per-org refactor — every VirtualKey is bound to exactly one modelGateway. The frontend renders this as the "gateway" pill on the operator console. |
| `agentId` | `ID` | — |
| `models` | `[String!]!` | — |
| `maxBudget` | `Float` | — |
| `status` | `VirtualKeyStatus!` | — |
| `expiresAt` | `Time` | — |
| `duration` | `String` | Human-readable remaining-lifetime, derived from expiresAt for display (e.g. "30d", "12h", "" when no expiry). Computed by the resolver; not persisted as a separate column. |
| `createdAt` | `Time!` | — |
| `updatedAt` | `Time!` | — |
| `maxParallelRequests` | `Int` | Per-key rate-limit / quota controls (LiteLLM design doc §4.2). |
| `tpmLimit` | `Int` | — |
| `rpmLimit` | `Int` | — |
| `rpmLimitType` | `String` | — |
| `tpmLimitType` | `String` | — |
| `budgetDuration` | `String` | — |
| `allowedRoutes` | `[String!]!` | allowed_routes — empty list means "no restriction" (the frontend's "Allow All Routes" switch ON translates to omit-this-field; ON → omit, OFF → fill with /v1/chat/completions etc). |
| `tags` | `[String!]!` | Operational metadata (LiteLLM design doc §4.2). |
| `blocked` | `Boolean!` | — |
| `keyType` | `String!` | — |
| `autoRotate` | `Boolean!` | — |
| `rotationInterval` | `String` | — |
| `spend` | `Float!` | Live spend + last-active (refreshed by the periodic worker; the console's progress bar reads these directly). |
| `lastActiveAt` | `Time` | — |
| `userId` | `String!` | 前端传入的 user_id,LiteLLM gateway 也用这个值作为 user_id。 必填,IssueVirtualKeyInput 强制要求前端传值,后端不做默认。 |

### IssueVirtualKeyInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | Required. Human-readable label. |
| `modelGateway` | `ID!` | Required. References the GatewayConnection that issues this key and will receive every model+route check. Resolver verifies each entry in `models` against the gateway's live model list (gatewayAvailableModels) before mint. |
| `duration` | `String` | Friendly duration input. Accepts "<n>d" / "<n>h" / "<n>w" / "<n>m". The server computes expiresAt = now + duration and persists it on the returned VirtualKey. This is the ONLY way to set an expiry at issue time; callers cannot pass an absolute timestamp. |
| `models` | `[String!]` | Optional. Models named MUST be a subset of `modelGateway`'s live model list (verified server-side via gatewayAvailableModels). Resolver 400s on stale names. Empty = omit (litellm default = no restriction). |
| `maxBudget` | `Float` | — |
| `budgetDuration` | `String` | — |
| `maxParallelRequests` | `Int` | — |
| `rpmLimit` | `Int` | — |
| `tpmLimit` | `Int` | — |
| `rpmLimitType` | `String` | — |
| `tpmLimitType` | `String` | — |
| `allowedRoutes` | `[String!]` | allowedRoutes — when the form's "Allow All Routes" switch is ON, the frontend OMITS this field. When OFF, it sends the explicit list. |
| `metadata` | `Map` | Auxiliary key metadata forwarded to the gateway as-is (mirrors the `metadata` map on the /key/generate wire body; see also the deploy flows that already use this for `{"agent": ...}`). Tags now live under `metadata.tags` (e.g. `{"tags": ["project:demo","env:test"]}`) rather than as a top-level field. The resolver translates back to `tags` on the persisted VirtualKey so the read-side response and the ent column remain unchanged. |
| `keyType` | `String` | Operational / catalog metadata (LiteLLM design doc §4.2). |
| `autoRotate` | `Boolean` | — |
| `rotationInterval` | `String` | — |
| `userId` | `String!` | Required. LiteLLM /key/generate 现在校验 user_id 非空。 前端必须传一个非空字符串;后端透传到 ent.VirtualKey.user_id 和 gateway.GenerateKeyRequest.UserID,不做默认值兜底。 |

### LimitType

*Enum*

Limit-type enum — LiteLLM's per-key quota vocabulary. Optional; if unset, LiteLLM defaults to "best_effort" or similar per its own config.

| Value | Description |
|-------|-------------|
| `guaranteed_throughput` | — |
| `best_effort` | — |

### RoutePermission

*Enum*

RoutePermission — frontend multi-select enum mapped to /v1/* paths (LiteLLM design doc §4.2). The form's "Allow All Routes" switch, when ON, OMITS the allowed_routes field entirely; when OFF, the form picks one or more of these and translates to ["/v1/chat/completions", ...].

| Value | Description |
|-------|-------------|
| `CHAT` | — |
| `EMBEDDINGS` | — |
| `IMAGES` | — |
| `AUDIO` | — |

### VirtualKeyStatus

*Enum*

| Value | Description |
|-------|-------------|
| `active` | — |
| `disabled` | — |
| `revoked` | — |
