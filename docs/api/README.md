# Agent Platform — GraphQL API Reference

Auto-generated from the source-of-truth schema modules under [`schema/*.graphql`](../../schema). Regenerate with `python3 tools/apidocs/gen.py` after any schema change (and `make schema-dump` to refresh the merged SDL at [`docs/schema.graphql`](../schema.graphql)).

All operations are served from a single endpoint: **`POST /query`**.

## Domains

| Page | Covers |
|------|--------|
| [Agents, Templates & Snapshots](./agents.md) | Agent catalog/templates, agent instances, deploy from OVA, snapshots, lifecycle |
| [Agent Config, Artifacts, Skills & Images](./agent-config.md) | Agent configs, content-library artifacts, skills, container images |
| [Model Gateways (Connections)](./model-gateway.md) | Model gateways, gateway connections, upstreams, difficulty router tiers |
| [Gateway Routing (Model Routes)](./gateway-routing.md) | Model routes (gateway routing topology, route CRUD, sync to gateway) |
| [Provider Model (Upstream LLM)](./provider-model.md) |  |
| [Virtual Keys & Rate Limits](./virtual-keys.md) | Per-user LiteLLM virtual keys and rate-limit policies |
| [Observability (Request Logs, Audit Logs, Metrics)](./observability.md) | Request logs, audit logs, request metrics, rate-limit policies |
| [Metering](./metering.md) | Token usage and cost aggregation (metering center) |
| [Platform (Users, Roles, Permissions, Departments)](./platform.md) | Users, built-in roles, custom roles, permissions, departments, memberships |
| [Resource Pools & vSphere](./resource-pools.md) | vCenter resource pools and vSphere placement pools |
| [OVA Marketplace](./ova-marketplace.md) | OVA template families and versions (agent marketplace catalog) |
| [Dashboard](./dashboard.md) | Console overview: stat cards, recent agents, system notices |

## Authentication & RBAC

### Bearer-token auth

1. Call the `login` mutation with `LoginInput { email, password }` (the form
   collects an email; the backend accepts username *or* email).
2. `login` returns an `AuthPayload` whose **`token`** is the session id.
3. Send that token on every subsequent request as
   **`Authorization: Bearer <token>`**. (For same-origin browser use the server
   also accepts the `ap_session` cookie as a fallback.)
4. `me` returns the current `User`; `logout` invalidates the session;
   `changePassword` rotates the caller's password.

`AuthPayload.mustChangePassword` (and `User.mustChangePassword`) signals a
first-login forced password change.

### How the directives gate operations

Two schema directives enforce access control on individual fields:

- **`@hasRole(any: [RoleName!]!)`** — the caller's platform role must be one of
  the listed roles. Unauthenticated callers are rejected (`unauthenticated`);
  authorized-but-wrong-role callers get `forbidden`.
- **`@hasPermission(perm: String!)`** — the caller must hold the named permission
  key. This is checked first against the static role→permission matrix
  (fast path) and then against the union of the caller's **custom-role**
  permissions (`user_roles → role_permissions`), so admin-configured custom
  roles actually grant access.

A field with no directive is available to any authenticated caller. Some
operations additionally enforce **owner scoping** in the resolver (e.g. a regular
user sees only their own agents) — that scoping is *not* expressed by a directive
and is noted in the per-operation descriptions where relevant.

> **Enum vs. storage spelling:** the GraphQL `RoleName` enum uses `tenant_admin`
> (GraphQL enums cannot contain hyphens), while the auth/session layer stores the
> role as `tenant-admin`. The directive layer maps between them
> (`internal/graph/directives.go`).

### Platform roles (`RoleName`)

| Role | Description |
|------|-------------|
| `admin` | Super administrator — platform-wide access. |
| `tenant_admin` | Tenant-wide administrator (progressive multi-tenant rollout). |
| `observability` | Read-only observability specialist (audit + metering). |
| `user` | Regular user — access to their own resources only. |

### Permission matrix (`rolePermissions`)

From `internal/auth/rbac.go`. A ✓ means the role holds that permission
platform-wide. `user` holds no platform-wide permissions; its access to its own
resources is resolved per-resource (owner scoping), not via this table.

| Permission key | `admin` | `tenant_admin` | `observability` | `user` |
|----------------|:------:|:--------------:|:---------------:|:------:|
| `agent:manage` | ✓ | ✓ | | |
| `key:manage` | ✓ | ✓ | | |
| `route:manage` | ✓ | ✓ | | |
| `audit:view` | ✓ | ✓ | ✓ | |
| `metering:view` | ✓ | ✓ | ✓ | |
| `user:manage` | ✓ | ✓ | | |

> Note: `tenant_admin` holds `route:manage`, but the model-gateway/route
> operations are gated with `@hasRole(any: [admin])` (not the permission),
> because routes are platform-global config with no tenant scoping — a permission
> gate would leak cross-tenant config. See `schema/gateway-routing.graphql`.

## Core operations

These live in `schema/schema.graphql` and are not tied to a single domain. `login` is the only operation callable while unauthenticated.

### Queries

### `me`

```graphql
me: User!
```

- **Returns:** `User!`
- **Auth:** authenticated (no directive)

### `auditLogs`

```graphql
auditLogs(filter: AuditFilter, page: PageInput): AuditConnection!
```

- **Returns:** `AuditConnection!`
- **Auth:** `@hasPermission(perm: "audit:view")`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `filter` | `AuditFilter` | no | — |
| `page` | `PageInput` | no | — |

### Mutations

### `login`

```graphql
login(input: LoginInput!): AuthPayload!
```

- **Returns:** `AuthPayload!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `LoginInput!` | yes | — |

### `logout`

```graphql
logout: Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** authenticated (no directive)

### `changePassword`

```graphql
changePassword(oldPassword: String!, newPassword: String!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `oldPassword` | `String!` | yes | — |
| `newPassword` | `String!` | yes | — |

## Core types

### AuditConnection

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `items` | `[AuditLog!]!` | — |
| `total` | `Int!` | — |

### AuditLog

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `actorUserId` | `ID` | — |
| `actorName` | `String` | Human-friendly actor name (resolved from actorUserId); null for platform/system actions or deleted users. |
| `action` | `String!` | — |
| `resourceType` | `String` | — |
| `resourceId` | `String` | — |
| `ip` | `String` | — |
| `result` | `String!` | — |
| `detail` | `String` | — |
| `createdAt` | `Time!` | — |

### AuthPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `token` | `String!` | Bearer token (= session id). The client stores it and sends it as `Authorization: Bearer <token>` on subsequent requests (前后端整合:token 认证). |
| `user` | `User!` | — |
| `mustChangePassword` | `Boolean!` | — |

### User

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `username` | `String!` | — |
| `displayName` | `String!` | Human-friendly name for display (前后端整合契约). Defaults to username until a dedicated display-name field exists. |
| `email` | `String!` | — |
| `role` | `RoleName!` | — |
| `tenantId` | `ID` | — |
| `mustChangePassword` | `Boolean!` | — |
| `enabled` | `Boolean!` | — |
| `connectionStatus` | `ConnectionStatus!` | ONLINE when the user currently has at least one live session (derived from the session store, not a column). For the `me` caller this is necessarily ONLINE. |
| `lastLoginAt` | `Time` | — |
| `createdAt` | `Time!` | — |

### AuditFilter

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `actorUserId` | `ID` | — |
| `actionPrefix` | `String` | action category prefix, e.g. "user." / "router." / "key." |
| `search` | `String` | substring match across action + resourceId |
| `from` | `Time` | createdAt window (inclusive); either bound may be omitted. |
| `to` | `Time` | — |
| `result` | `String` | exact result: "success" \| "fail". |
| `resourceType` | `String` | exact resource type, e.g. "user" / "gateway_connection" / "virtual_key". |

### LoginInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `email` | `String!` | The console login form collects an email; the backend accepts username or email. |
| `password` | `String!` | — |
| `remember` | `Boolean` | When false, the session cookie is a session cookie (cleared on browser close); when true or omitted, it persists for the session TTL ("remember me"). |

### PageInput

*Input*

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `limit` | `Int` | `50` | — |
| `offset` | `Int` | `0` | — |

### RoleName

*Enum*

Platform role enum (用于 @hasRole 鉴权 + User.role). Renamed from `Role` so the frontend-facing `Role` *entity* (in account.graphql) can own that name (前后端整合).

| Value | Description |
|-------|-------------|
| `admin` | — |
| `user` | — |
| `read_only` | — |

### Map

*Scalar*

Free-form JSON object (gqlgen built-in) — used for Artifact metadata (LLD-06).


### Time

*Scalar*
