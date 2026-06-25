# Metering

[← API Reference index](./README.md)

> Source: `schema/metering.graphql`

## Queries

### `tokenUsage`

metering:view permission (admin + observability).

```graphql
tokenUsage(userId: ID, page: PageInput): [TokenUsage!]!
```

- **Returns:** `[TokenUsage!]!`
- **Auth:** `@hasPermission(perm: "metering:view")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `userId` | `ID` | no | — |
| `page` | `PageInput` | no | — |

### `meteringSummary`

```graphql
meteringSummary(userId: ID): MeteringSummary!
```

- **Returns:** `MeteringSummary!`
- **Auth:** `@hasPermission(perm: "metering:view")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `userId` | `ID` | no | — |

### `meteringOverview`

Aggregated metering for the console 计量中心 over a time range (默认近7天).

```graphql
meteringOverview(range: MeteringTimeRange = LAST_7_DAYS, userId: ID): MeteringOverview!
```

- **Returns:** `MeteringOverview!`
- **Auth:** `@hasPermission(perm: "metering:view")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `range` | `MeteringTimeRange` | no | `LAST_7_DAYS` |
| `userId` | `ID` | no | — |

## Mutations

### `recordTokenUsage`

Ingest path (gateway usage callback / telemetry). Admin-scoped for M1; TODO: dedicated service token.

```graphql
recordTokenUsage(input: RecordTokenUsageInput!): TokenUsage!
```

- **Returns:** `TokenUsage!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `RecordTokenUsageInput!` | yes | — |

## Types

### AgentUsage

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `agentId` | `ID!` | — |
| `inputTokens` | `Int!` | — |
| `outputTokens` | `Int!` | — |
| `cost` | `Float!` | — |

### AgentUsageRow

*Object*

Per-agent usage row for the metering 智能体用量 table. Adds requests (row count = usage events) and a display name on top of the token/cost sums.

| Field | Type | Description |
|-------|------|-------------|
| `agentId` | `ID!` | — |
| `agentName` | `String!` | — |
| `inputTokens` | `Int!` | — |
| `outputTokens` | `Int!` | — |
| `totalTokens` | `Int!` | — |
| `requests` | `Int!` | — |
| `cost` | `Float!` | — |

### DailyUsageRow

*Object*

Per-day usage row for the metering 每日用量/趋势 table+chart (date = YYYY-MM-DD).

| Field | Type | Description |
|-------|------|-------------|
| `date` | `String!` | — |
| `inputTokens` | `Int!` | — |
| `outputTokens` | `Int!` | — |
| `totalTokens` | `Int!` | — |
| `requests` | `Int!` | — |
| `cost` | `Float!` | — |

### DateUsage

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `date` | `String!` | YYYY-MM-DD |
| `inputTokens` | `Int!` | — |
| `outputTokens` | `Int!` | — |
| `cost` | `Float!` | — |

### MeteringCostSummary

*Object*

Cost summary cards (计费概览): total cost over the selected range, plus the current calendar month's cost (the "本月" card).

| Field | Type | Description |
|-------|------|-------------|
| `totalCost` | `Float!` | — |
| `monthlyCost` | `Float!` | — |

### MeteringOverview

*Object*

Everything the metering center renders for one time range (计量中心).

| Field | Type | Description |
|-------|------|-------------|
| `range` | `MeteringTimeRange!` | — |
| `totalInputTokens` | `Int!` | — |
| `totalOutputTokens` | `Int!` | — |
| `totalTokens` | `Int!` | — |
| `totalRequests` | `Int!` | — |
| `byAgent` | `[AgentUsageRow!]!` | — |
| `byModel` | `[ModelUsageRow!]!` | — |
| `byDay` | `[DailyUsageRow!]!` | — |
| `cost` | `MeteringCostSummary!` | — |

### MeteringSummary

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `totalInputTokens` | `Int!` | — |
| `totalOutputTokens` | `Int!` | — |
| `totalCost` | `Float!` | — |
| `byModel` | `[ModelUsage!]!` | — |
| `byAgent` | `[AgentUsage!]!` | — |
| `byDate` | `[DateUsage!]!` | — |

### ModelUsage

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `model` | `String!` | — |
| `inputTokens` | `Int!` | — |
| `outputTokens` | `Int!` | — |
| `cost` | `Float!` | — |

### ModelUsageRow

*Object*

Per-model usage row for the metering 模型用量 table.

| Field | Type | Description |
|-------|------|-------------|
| `model` | `String!` | — |
| `inputTokens` | `Int!` | — |
| `outputTokens` | `Int!` | — |
| `totalTokens` | `Int!` | — |
| `requests` | `Int!` | — |
| `cost` | `Float!` | — |

### TokenUsage

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `userId` | `ID!` | — |
| `agentId` | `ID` | — |
| `model` | `String!` | — |
| `inputTokens` | `Int!` | — |
| `outputTokens` | `Int!` | — |
| `cost` | `Float!` | — |
| `createdAt` | `Time!` | — |

### RecordTokenUsageInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `userId` | `ID!` | — |
| `agentId` | `ID` | — |
| `model` | `String!` | — |
| `inputTokens` | `Int!` | — |
| `outputTokens` | `Int!` | — |
| `cost` | `Float` | — |

### MeteringTimeRange

*Enum*

Time window for the metering center (计量中心 time-range selector). LAST_30_DAYS / THIS_MONTH map to a concrete [from,to); LAST_7_DAYS is the default.

| Value | Description |
|-------|-------------|
| `LAST_7_DAYS` | — |
| `LAST_30_DAYS` | — |
| `THIS_MONTH` | — |
