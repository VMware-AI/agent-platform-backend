# Dashboard

[← API Reference index](./README.md)

> Source: `schema/dashboard.graphql`

## Queries

### `dashboardOverview`

The console overview page. recentLimit/noticeLimit cap the two lists (默认 5). Figures are platform-global (counts/notices are not tenant-scoped yet), so this is restricted to platform roles — exposing it to tenant_admin would leak other tenants' counts/usage/audit. Per-tenant dashboard scoping is future work (C1).

```graphql
dashboardOverview(recentLimit: Int = 5, noticeLimit: Int = 5): DashboardOverview!
```

- **Returns:** `DashboardOverview!`
- **Auth:** `@hasRole(any: [admin, observability])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `recentLimit` | `Int` | no | `5` |
| `noticeLimit` | `Int` | no | `5` |

## Types

### DashboardNotice

*Object*

A system notice for the 系统通知 list, sourced from the most recent audit logs.

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `text` | `String!` | — |
| `status` | `DashboardNoticeStatus!` | — |
| `occurredAt` | `Time!` | — |

### DashboardOverview

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `stats` | `DashboardStats!` | — |
| `recentAgents` | `[DashboardRecentAgent!]!` | — |
| `notices` | `[DashboardNotice!]!` | — |

### DashboardRecentAgent

*Object*

A recently created agent for the 最近创建的实例 table.

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `agentName` | `String!` | the agent kind/type |
| `status` | `DashboardAgentStatus!` | — |
| `createdAt` | `Time!` | — |

### DashboardStats

*Object*

Headline counts for the overview stat cards.

| Field | Type | Description |
|-------|------|-------------|
| `totalAgents` | `Int!` | — |
| `runningAgents` | `Int!` | — |
| `stoppedAgents` | `Int!` | — |
| `exceptionAgents` | `Int!` | — |
| `totalVirtualKeys` | `Int!` | — |
| `totalGateways` | `Int!` | — |
| `totalResourcePools` | `Int!` | — |
| `totalUsers` | `Int!` | — |
| `monthlyCalls` | `Int!` | Calls = TokenUsage rows in the current calendar month (本月调用数). |
| `monthlyTokens` | `Int!` | Token + cost for the current calendar month (本月 Token / 预估费用). |
| `monthlyCost` | `Float!` | — |

### DashboardAgentStatus

*Enum*

Status of a recent agent row (mirrors Agent.status, minus provisioning which the console surfaces as stopped for the overview list).

| Value | Description |
|-------|-------------|
| `running` | — |
| `stopped` | — |
| `exception` | — |

### DashboardNoticeStatus

*Enum*

Severity of a system notice, mapped from the source audit-log result.

| Value | Description |
|-------|-------------|
| `success` | — |
| `warning` | — |
| `danger` | — |
