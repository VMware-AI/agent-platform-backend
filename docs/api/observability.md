# Observability (Request Logs, Audit Logs, Metrics)

[← API Reference index](./README.md)

> Source: `schema/observability.graphql`

> Rate-limit policies are co-located here (same source file) but are conceptually paired with [virtual keys](./virtual-keys.md), which bind to them via `rateLimitPolicyId`.

## Queries

### `requestLogs`

```graphql
requestLogs(filter: RequestLogFilter, page: PageInput): [RequestLog!]!
```

- **Returns:** `[RequestLog!]!`
- **Auth:** `@hasPermission(perm: "audit:view")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `filter` | `RequestLogFilter` | no | — |
| `page` | `PageInput` | no | — |

### `rateLimitPolicies`

```graphql
rateLimitPolicies: [RateLimitPolicy!]!
```

- **Returns:** `[RateLimitPolicy!]!`
- **Auth:** `@hasRole(any: [admin, tenant_admin])`

### `requestMetrics`

```graphql
requestMetrics(from: Time!, to: Time!, granularity: RequestMetricsBucketGranularity!, filter: RequestMetricsFilter): RequestMetrics!
```

- **Returns:** `RequestMetrics!`
- **Auth:** `@hasPermission(perm: "audit:view")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `from` | `Time!` | yes | — |
| `to` | `Time!` | yes | — |
| `granularity` | `RequestMetricsBucketGranularity!` | yes | — |
| `filter` | `RequestMetricsFilter` | no | — |

## Mutations

### `recordRequestLog`

```graphql
recordRequestLog(input: RecordRequestLogInput!): RequestLog!
```

- **Returns:** `RequestLog!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `RecordRequestLogInput!` | yes | — |

### `upsertRateLimitPolicy`

```graphql
upsertRateLimitPolicy(input: UpsertRateLimitPolicyInput!): RateLimitPolicy!
```

- **Returns:** `RateLimitPolicy!`
- **Auth:** `@hasPermission(perm: "route:manage")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `UpsertRateLimitPolicyInput!` | yes | — |

### `setRateLimitPolicyEnabled`

```graphql
setRateLimitPolicyEnabled(id: ID!, enabled: Boolean!): RateLimitPolicy!
```

- **Returns:** `RateLimitPolicy!`
- **Auth:** `@hasPermission(perm: "route:manage")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `enabled` | `Boolean!` | yes | — |

### `deleteRateLimitPolicy`

Delete a policy. Refused while any non-revoked virtual key still references it (reassign/revoke those keys first) so bound keys never silently lose their limits.

```graphql
deleteRateLimitPolicy(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasPermission(perm: "route:manage")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

## Types

### RateLimitPolicy

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `rpm` | `Int` | — |
| `tpm` | `Int` | — |
| `enabled` | `Boolean!` | — |
| `createdAt` | `Time!` | — |

### RequestLog

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `requestId` | `String!` | — |
| `userId` | `ID` | — |
| `agentId` | `ID` | — |
| `model` | `String` | — |
| `inputTokens` | `Int!` | — |
| `outputTokens` | `Int!` | — |
| `latencyMs` | `Int!` | — |
| `statusCode` | `Int!` | — |
| `detail` | `String` | — |
| `createdAt` | `Time!` | — |

### RequestMetrics

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `rangeStart` | `Time!` | — |
| `rangeEnd` | `Time!` | — |
| `granularity` | `RequestMetricsBucketGranularity!` | — |
| `buckets` | `[RequestMetricsBucket!]!` | — |
| `summary` | `RequestMetricsSummary!` | — |

### RequestMetricsBucket

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `timestamp` | `Time!` | — |
| `requestCount` | `Int!` | — |
| `errorCount` | `Int!` | — |
| `avgLatencyMs` | `Int!` | — |
| `p95LatencyMs` | `Int!` | — |
| `inputTokensTotal` | `Int!` | — |
| `outputTokensTotal` | `Int!` | — |

### RequestMetricsSummary

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `totalRequests` | `Int!` | — |
| `totalErrors` | `Int!` | — |
| `errorRate` | `Float!` | — |
| `avgLatencyMs` | `Int!` | — |
| `p95LatencyMs` | `Int!` | — |
| `totalInputTokens` | `Int!` | — |
| `totalOutputTokens` | `Int!` | — |

### RecordRequestLogInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `requestId` | `String!` | — |
| `userId` | `ID` | — |
| `agentId` | `ID` | — |
| `model` | `String` | — |
| `inputTokens` | `Int` | — |
| `outputTokens` | `Int` | — |
| `latencyMs` | `Int` | — |
| `statusCode` | `Int!` | — |
| `detail` | `String` | — |

### RequestLogFilter

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `statusCode` | `Int` | — |
| `agentId` | `ID` | — |
| `model` | `String` | — |
| `requestId` | `String` | — |

### RequestMetricsFilter

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `statusCode` | `Int` | — |
| `agentId` | `ID` | — |
| `model` | `String` | — |

### UpsertRateLimitPolicyInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `rpm` | `Int` | — |
| `tpm` | `Int` | — |
| `enabled` | `Boolean` | — |

### RequestMetricsBucketGranularity

*Enum*

| Value | Description |
|-------|-------------|
| `MINUTE` | — |
| `HOUR` | — |
| `DAY` | — |
