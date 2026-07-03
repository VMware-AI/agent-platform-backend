# Observability (Request Logs, Audit Logs, Metrics)

[← API Reference index](./README.md)

> Source: `schema/observability.graphql`

> Rate-limit policies are co-located here (same source file) but are conceptually paired with [virtual keys](./virtual-keys.md), which bind to them via `rateLimitPolicyId`.

## Queries

### `requestLogs`

```graphql
requestLogs(filter: RequestLogFilter, page: PageInput): RequestLogConnection!
```

- **Returns:** `RequestLogConnection!`
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
- **Auth:** `@hasRole(any: [admin])`

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

### `gatewayHealth`

Upstream health across every configured gateway (fan-out to litellm /health).

```graphql
gatewayHealth: [GatewayHealth!]!
```

- **Returns:** `[GatewayHealth!]!`
- **Auth:** `@hasPermission(perm: "audit:view")`

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

### EndpointHealth

*Object*

One upstream deployment's health as litellm reports it (GET /health).

| Field | Type | Description |
|-------|------|-------------|
| `model` | `String!` | — |
| `apiBase` | `String` | — |

### GatewayHealth

*Object*

A gateway's health, aggregated by the backend fanning out to litellm's /health (LLD-15 T4). reachable reflects /health/readiness; the endpoint lists come from /health (litellm probes its upstreams).

| Field | Type | Description |
|-------|------|-------------|
| `gatewayId` | `ID!` | — |
| `gatewayName` | `String!` | — |
| `reachable` | `Boolean!` | — |
| `healthyCount` | `Int!` | — |
| `unhealthyCount` | `Int!` | — |
| `healthy` | `[EndpointHealth!]!` | — |
| `unhealthy` | `[EndpointHealth!]!` | — |
| `error` | `String` | — |

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

### RequestLogConnection

*Object*

Paged request logs with a real total for offset/limit pagination.

| Field | Type | Description |
|-------|------|-------------|
| `items` | `[RequestLog!]!` | — |
| `total` | `Int!` | — |

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
| `p50LatencyMs` | `Int!` | — |
| `p95LatencyMs` | `Int!` | — |
| `p99LatencyMs` | `Int!` | — |
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
| `userId` | `ID` | — |
| `from` | `Time` | createdAt window (inclusive); either bound may be omitted. |
| `to` | `Time` | — |
| `statusClass` | `RequestStatusClass` | status band, translated to a code range server-side (2xx/4xx/5xx). |

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

### RequestStatusClass

*Enum*

HTTP status band for filtering (avoids exposing raw code ranges to the client).

| Value | Description |
|-------|-------------|
| `SUCCESS` | 2xx |
| `CLIENT_ERROR` | 4xx |
| `SERVER_ERROR` | 5xx |
