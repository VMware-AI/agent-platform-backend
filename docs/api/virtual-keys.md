# Virtual Keys & Rate Limits

[← API Reference index](./README.md)

> Source: `schema/virtualkey.graphql`

> **Rate-limit policies** (the `RateLimitPolicy` type, the `rateLimitPolicies` query, and the `upsertRateLimitPolicy` / `setRateLimitPolicyEnabled` / `deleteRateLimitPolicy` mutations) are defined in `schema/observability.graphql` and documented on the [Observability page](./observability.md). A virtual key references a policy via `IssueVirtualKeyInput.rateLimitPolicyId`.

## Queries

### `virtualKeys`

```graphql
virtualKeys(userId: ID): [VirtualKey!]!
```

- **Returns:** `[VirtualKey!]!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `userId` | `ID` | no | — |

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

Rotate the key's secret at the gateway, keeping its governance row/binding. Returns the new secret ONCE (the old one stops working after litellm's grace). LLD-04 §3.

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

## Types

### IssuedVirtualKey

*Object*

Returned only at issue time — carries the secret, which is never queryable again.

| Field | Type | Description |
|-------|------|-------------|
| `virtualKey` | `VirtualKey!` | — |
| `secret` | `String!` | — |

### VirtualKey

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `alias` | `String` | — |
| `userId` | `ID!` | — |
| `agentId` | `ID` | — |
| `rateLimitPolicyId` | `ID` | — |
| `teamId` | `String` | — |
| `models` | `[String!]!` | — |
| `maxBudget` | `Float` | — |
| `status` | `VirtualKeyStatus!` | — |
| `expiresAt` | `Time` | — |
| `createdAt` | `Time!` | — |

### IssueVirtualKeyInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `userId` | `ID!` | — |
| `agentId` | `ID` | — |
| `rateLimitPolicyId` | `ID` | Associated rate-limit policy; its rpm/tpm are applied to the litellm key. |
| `teamId` | `String` | — |
| `models` | `[String!]` | — |
| `maxBudget` | `Float` | — |
| `rpmLimit` | `Int` | — |
| `tpmLimit` | `Int` | — |
| `alias` | `String` | — |
| `expiresAt` | `Time` | Optional expiry; when set, the key stops working at the gateway after this time. |

### VirtualKeyStatus

*Enum*

| Value | Description |
|-------|-------------|
| `active` | — |
| `disabled` | — |
| `revoked` | — |
