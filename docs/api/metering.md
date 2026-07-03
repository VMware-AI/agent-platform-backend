# Metering

[← API Reference index](./README.md)

> Source: `schema/metering.graphql`, `schema/observability-spend.graphql`

## Queries

### `tokenUsage`

metering:view permission (admin + read_only).

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

### `spendReport`

litellm-authoritative spend, fanned out across gateways (pull + short cache).

```graphql
spendReport(input: SpendReportInput!): SpendReport!
```

- **Returns:** `SpendReport!`
- **Auth:** `@hasPermission(perm: "metering:view")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `SpendReportInput!` | yes | — |

### `budgets`

```graphql
budgets(scope: BudgetScope!): [Budget!]!
```

- **Returns:** `[Budget!]!`
- **Auth:** `@hasPermission(perm: "metering:view")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `scope` | `BudgetScope!` | yes | — |

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

### Budget

*Object*

A budget card: current spend vs the configured max, with reset timing.

| Field | Type | Description |
|-------|------|-------------|
| `scope` | `String!` | team/user/key identifier |
| `label` | `String!` | — |
| `spend` | `Float!` | — |
| `maxBudget` | `Float` | null = no cap |
| `remaining` | `Float` | — |
| `budgetResetAt` | `Time` | — |
| `utilizationPct` | `Float` | spend/maxBudget*100; null when maxBudget is null |

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

### GatewaySpendStatus

*Object*

Per-gateway fetch outcome so a single unreachable gateway degrades gracefully (partial data visible) instead of failing the whole report.

| Field | Type | Description |
|-------|------|-------------|
| `gatewayId` | `ID!` | — |
| `gatewayName` | `String!` | — |
| `ok` | `Boolean!` | — |
| `error` | `String` | — |

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

### SpendDailyPoint

*Object*

Cost trend point (all gateways merged), date is YYYY-MM-DD.

| Field | Type | Description |
|-------|------|-------------|
| `date` | `String!` | — |
| `spend` | `Float!` | — |
| `totalTokens` | `Int!` | — |

### SpendReport

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `from` | `Time!` | — |
| `to` | `Time!` | — |
| `groupBy` | `SpendGroupBy!` | — |
| `rows` | `[SpendRow!]!` | — |
| `totals` | `SpendTotals!` | — |
| `byDay` | `[SpendDailyPoint!]!` | — |
| `gateways` | `[GatewaySpendStatus!]!` | — |

### SpendRow

*Object*

One aggregated row for the selected dimension, summed across all gateways.

| Field | Type | Description |
|-------|------|-------------|
| `key` | `String!` | team_id / hashed api_key / model name (user_id is deferred to the copy phase). |
| `label` | `String!` | Resolved display name: department name / key alias / model — falls back to key. |
| `spend` | `Float!` | — |
| `promptTokens` | `Int!` | — |
| `completionTokens` | `Int!` | — |
| `totalTokens` | `Int!` | — |
| `requests` | `Int!` | — |

### SpendTotals

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `spend` | `Float!` | — |
| `promptTokens` | `Int!` | — |
| `completionTokens` | `Int!` | — |
| `totalTokens` | `Int!` | — |
| `requests` | `Int!` | — |

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

### SpendReportInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `from` | `Time!` | Custom window (lifts the LAST_7/30_DAYS/THIS_MONTH restriction of meteringOverview). |
| `to` | `Time!` | — |
| `groupBy` | `SpendGroupBy!` | — |

### BudgetScope

*Enum*

| Value | Description |
|-------|-------------|
| `TEAMS` | — |
| `USERS` | — |
| `KEYS` | — |

### MeteringTimeRange

*Enum*

Time window for the metering center (计量中心 time-range selector). LAST_30_DAYS / THIS_MONTH map to a concrete [from,to); LAST_7_DAYS is the default.

| Value | Description |
|-------|-------------|
| `LAST_7_DAYS` | — |
| `LAST_30_DAYS` | — |
| `THIS_MONTH` | — |

### SpendGroupBy

*Enum*

| Value | Description |
|-------|-------------|
| `TEAM` | — |
| `USER` | — |
| `API_KEY` | — |
| `MODEL` | — |
