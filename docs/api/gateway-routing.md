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

Atomic 全量聚合覆盖刷新 — re-aggregates every ModelRoute and POSTs the full router_settings payload to /config/update, grouped by modelGatewayId. Triggered automatically after a route save; exposed as a mutation so the console can call it explicitly. Each gateway receives only the routes bound to it.

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
| `modelGateway` | `ModelGateway!` | Required: the litellm gateway this route is hosted on. The router-settings push targets this gateway (no platform default fallback). |
| `supportedModels` | `[String!]!` | The litellm model group served by this route. Mapped 1:1 to the `models` array on the wire (single-element in the common case; multi-element is allowed but rare). |
| `strategy` | `LoadBalancingStrategy!` | The litellm LoadBalanceStrategy applied at push time. The console form exposes only the friendly values (round-robin / weighted / random); the backend translates these into the litellm enum below and persists here. |
| `createdAt` | `Time!` | — |
| `updatedAt` | `Time!` | — |
| `fallbacks` | `[String!]!` | Fallback chains surfaced to litellm via POST /config/update. Three independent lists map 1:1 to the doc's three fallback kinds (general / context-window / content-policy). Each entry is a litellm model name referenced by the corresponding route's supportedModels[0] on the wire. |
| `contextWindowFallbacks` | `[String!]!` | — |
| `contentPolicyFallbacks` | `[String!]!` | — |

### CreateModelRouteInput

*Input*

Console 模型路由 create form (创建路由). name is the route's identifier and becomes the routing_group.group_name on the wire. modelGatewayId is REQUIRED — a route without a gateway has no router-settings push target. strategy is the litellm LoadBalanceStrategy to set on this route; default (omitted) leaves the ent column default in place (SIMPLE_SHUFFLE). A duplicate name surfaces as a GraphQL error — re-saving the same name goes through updateModelRoute.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `modelGatewayId` | `ID!` | — |
| `supportedModels` | `[String!]!` | — |
| `strategy` | `LoadBalancingStrategy` | — |
| `fallbacks` | `[String!]` | — |
| `contextWindowFallbacks` | `[String!]` | — |
| `contentPolicyFallbacks` | `[String!]` | — |

### UpdateModelRouteInput

*Input*

Console 模型路由 edit form (编辑路由). All fields optional — only set ones change. modelGatewayId, when present, must point at a live gateway.

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String` | — |
| `modelGatewayId` | `ID` | — |
| `supportedModels` | `[String!]` | — |
| `strategy` | `LoadBalancingStrategy` | — |
| `fallbacks` | `[String!]` | — |
| `contextWindowFallbacks` | `[String!]` | — |
| `contentPolicyFallbacks` | `[String!]` | — |
