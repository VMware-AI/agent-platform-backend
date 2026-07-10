# Gateway Routing (Model Routes)

[← API Reference index](./README.md)

> Source: `schema/gateway-routing.graphql`

## Queries

### `modelRoutes`

Model routes are platform-global gateway config (no tenant_id), so admin-only — tenant-admin must not read/write another tenant's routes (no scoping exists).

```graphql
modelRoutes: [ModelRoute!]!
```

- **Returns:** `[ModelRoute!]!`
- **Auth:** `@hasRole(any: [admin])`

## Mutations

### `createModelRoute`

Model routes CRUD — design doc §3.2

```graphql
createModelRoute(input: CreateModelRouteInput!): ModelRoute!
```

- **Returns:** `ModelRoute!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `CreateModelRouteInput!` | yes | — |

### `updateModelRoute`

```graphql
updateModelRoute(id: ID!, input: UpdateModelRouteInput!): ModelRoute!
```

- **Returns:** `ModelRoute!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `input` | `UpdateModelRouteInput!` | yes | — |

### `setModelRouteEnabled`

```graphql
setModelRouteEnabled(id: ID!, enabled: Boolean!): ModelRoute!
```

- **Returns:** `ModelRoute!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `enabled` | `Boolean!` | yes | — |

### `deleteModelRoute`

```graphql
deleteModelRoute(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `syncRouterSettings`

Atomic 全量聚合覆盖刷新 — re-aggregates every active ModelRoute and POSTs the full router_settings payload to /config/update, grouped by backendGatewayId. Triggered automatically after a route save; exposed as a mutation so the console can call it explicitly. Each gateway receives only the routes bound to it.

```graphql
syncRouterSettings: Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

## Types

### ModelRoute

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `modelAlias` | `String!` | — |
| `backendGatewayId` | `ID!` | Required: the litellm gateway this route is hosted on. The router-settings push targets this gateway (no platform default fallback). |
| `gatewayName` | `String!` | Display name of the serving gateway (console 模型路由 list). |
| `upstreams` | `[String!]!` | — |
| `supportedModels` | `[String!]!` | Console alias for `upstreams` — the models this route can serve (模型路由 page). |
| `strategy` | `LoadBalancingStrategy!` | — |
| `uiStrategy` | `ModelRouteStrategy!` | Console-facing load-balancing strategy (模型路由 page). |
| `enabled` | `Boolean!` | — |
| `createdAt` | `Time!` | — |
| `updatedAt` | `Time!` | — |
| `fallbacks` | `[String!]!` | Fallback chains (LiteLLM design doc §3.2) |
| `contextWindowFallbacks` | `[String!]!` | — |
| `contentPolicyFallbacks` | `[String!]!` | — |

### CreateModelRouteInput

*Input*

Console 模型路由 create form (创建路由). modelAlias is set to name; supportedModels are stored as the route's model group. backendGatewayId is REQUIRED — a route without a gateway has no router-settings push target. strategy is the litellm LoadBalanceStrategy to set on this route; default (omitted) leaves the ent column default in place (SIMPLE_SHUFFLE). A duplicate name surfaces as a GraphQL error — re-saving the same name goes through updateModelRoute.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `backendGatewayId` | `ID!` | — |
| `gatewayName` | `String` | — |
| `supportedModels` | `[String!]` | — |
| `strategy` | `LoadBalancingStrategy` | — |
| `uiStrategy` | `ModelRouteStrategy` | — |
| `enabled` | `Boolean` | — |
| `fallbacks` | `[String!]` | — |
| `contextWindowFallbacks` | `[String!]` | — |
| `contentPolicyFallbacks` | `[String!]` | — |

### UpdateModelRouteInput

*Input*

Console 模型路由 edit form (编辑路由). All fields optional — only set ones change. backendGatewayId, when present, must point at a live gateway.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String` | — |
| `backendGatewayId` | `ID` | — |
| `gatewayName` | `String` | — |
| `supportedModels` | `[String!]` | — |
| `uiStrategy` | `ModelRouteStrategy` | — |
| `enabled` | `Boolean` | — |
| `fallbacks` | `[String!]` | — |
| `contextWindowFallbacks` | `[String!]` | — |
| `contentPolicyFallbacks` | `[String!]` | — |

### ModelRouteStrategy

*Enum*

Console-facing load-balancing strategy for a model route (模型路由 page). Distinct from the litellm LoadBalanceStrategy: a friendly, gateway-agnostic choice the operator picks in the UI and the backend round-trips verbatim.

| Value | Description |
|-------|-------------|
| `ROUND_ROBIN` | — |
| `WEIGHTED_ROUND_ROBIN` | — |
| `RANDOM` | — |
