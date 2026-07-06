# VirtualKey per-agent-per-org Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor the VirtualKey entity from per-user to per-agent-per-org: drop `user_id` / `team_id` / `alias` columns, add `name` / `masked_key` / `organization_id (required)` / `updated_at (exposed)` / `duration (display-only)` fields, add the `associateVirtualKeyAgent` mutation, and remove the DeleteUser→revokeUserKeys cascade.

**Architecture:** Single-PR, large-but-coherent rewrite. Schema-level rename + 1 new GraphQL operation + 1 utility function (`redactKey`) + a small mapper rewrite. Generated artifacts (`ent/`, `generated.go`, `models_gen.go`, `docs/schema.graphql`) are produced by `go generate` / `gqlgen` / `make docs`; the plan only touches the **source-of-truth** files and verifies the generated ones drift in lock-step. No data backfill needed (no historical rows).

**Tech Stack:** Go 1.25 (pinned via `GOTOOLCHAIN=go1.25.0`), ent ORM, gqlgen, GraphQL, LiteLLM gateway client.

## Global Constraints

- Toolchain (CLAUDE.md §3): `GOTOOLCHAIN=go1.25.0` for **every** Go command. Local go 1.26 will produce inconsistent formatting and ent output.
- Generate-first: After editing `ent/schema/*.go` or `schema/*.graphql`, regenerate before any `go build` / `go vet`.
- Gate non-negotiable (CLAUDE.md §2): `gofmt -l .` must return empty; `make migrate-diff name=<x>` must succeed; `make docs` must succeed; `go vet` and `go build` must be 0-error.
- No test maintenance (CLAUDE.md §1): **Do NOT modify or add `*_test.go` files in this PR.** If existing tests break (e.g. `virtualkey_policy_test.go`), leave them — the maintainer will sweep tests on the weekly test run. But the production code (`*.go` outside `_test.go`) must compile cleanly under `go vet`.
- Single-PR scope: One module / one change set. Do not bundle unrelated refactors.
- Secret handling: `litellm_key` and `masked_key` are both `Sensitive()` and **must not** leak into logs / GraphQL responses except via the documented fields (`IssuedVirtualKey.secret` once; `VirtualKey.maskedKey` always).
- Commit messages end with: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`
- Conventional Commits prefix: `feat(virtualkey): ...` for schema/mutation changes; `chore(virtualkey): ...` for generated artifact-only changes; `refactor(virtualkey): ...` for deletions (e.g. `revokeUserKeys`).

## File Structure

| File | Role | This PR |
| --- | --- | --- |
| `ent/schema/virtualkey.go` | ent schema definition | **Modify** — fields renamed/added/dropped |
| `ent/runtime.go` | auto-registered validators | Auto (regenerated) |
| `ent/virtualkey.go` | top-level ent type | Auto (regenerated) |
| `ent/virtualkey/*.go` | sub-package | Auto (regenerated) |
| `ent/virtualkey_create.go`, `..._update.go`, `..._query.go`, `..._delete.go`, `..._where.go` | builders | Auto (regenerated) |
| `ent/migrate/schema.go` | atlas schema | Auto (regenerated) |
| `ent/migrate/migrations/atlas.sum` | migration hash | Auto (regenerated) |
| `schema/virtualkey.graphql` | GraphQL SDL source-of-truth | **Modify** — types/inputs/operations rewritten |
| `schema/schema.graphql` | merged SDL | Auto (from `make docs`) |
| `internal/graph/model/models_gen.go` | GraphQL Go structs | Auto (from `gqlgen generate`) |
| `internal/graph/generated.go` | GraphQL execution | Auto (from `gqlgen generate`) |
| `internal/graph/virtualkey.resolvers.go` | resolver implementations | **Modify** — 4 existing + 1 new mutation; new query signature |
| `internal/graph/mappers.go` | `toModelVirtualKey` | **Modify** — field list updated |
| `internal/gateway/client.go` | gateway HTTP client + utility | **Modify** — add `redactKey` helper |
| `internal/graph/account_helpers.go` | `revokeUserKeys` | **Modify** — remove function |
| `internal/graph/account.resolvers.go` | `DeleteUser` | **Modify** — drop `revokeUserKeys` call site |
| `internal/graph/tenant_scope.go` | tenant isolation | **Modify** — comment update + scope path stub |
| `internal/graph/gateway_resolve.go` | gateway routing | **Modify** — comment + call-site rename to OrganizationID |
| `docs/api/virtual-keys.md` | API doc | **Modify** — sync with new schema |
| `docs/schema.graphql` | mirrored SDL | Auto (from `make docs`) |
| `postman/*.json` | postman collection | **Modify** — drop obsolete fields, add `associateVirtualKeyAgent` example |
| `internal/gql/testdata/client_operations/*.graphql` | gqlgen client fixtures | **Modify** — match new schema |

## Task Decomposition

Tasks are ordered so each compiles cleanly on top of the previous one (with regenerations folded into the task that needs them). A reviewer can gate-approve at any task boundary.

- **Task 1**: Add `redactKey` utility (no schema change, isolated, easy review).
- **Task 2**: Rewrite `ent/schema/virtualkey.go` — rename `alias`→`name`, drop `user_id` / `team_id`, add `masked_key`, promote `organization_id` to NotEmpty.
- **Task 3**: Regenerate ent + generate migration.
- **Task 4**: Rewrite `schema/virtualkey.graphql` — new VirtualKey type, new input, new mutation, new query signature.
- **Task 5**: Regenerate gqlgen.
- **Task 6**: Rewrite `internal/graph/mappers.go::toModelVirtualKey`.
- **Task 7**: Rewrite `internal/graph/virtualkey.resolvers.go` — `IssueVirtualKey`, `RegenerateVirtualKey`, new `AssociateVirtualKeyAgent`, `VirtualKeys` query.
- **Task 8**: Update `internal/graph/gateway_resolve.go` + `tenant_scope.go` + remove `revokeUserKeys` + update `DeleteUser`.
- **Task 9**: Sync docs (`docs/api/virtual-keys.md`) + postman fixtures + `make docs`.
- **Task 10**: Final verification sweep + commit.
- **Task 11**: Add `gw.ListAvailableModels` client method + `GatewayAvailableModels` resolver + `buildModelGatewayFromConn` helper extraction.

---

## Task 1: Add `redactKey` helper to gateway client

**Files:**
- Modify: `internal/gateway/client.go` (add at the end of the file, after `redactSecrets`)

**Interfaces:**
- Consumes: nothing
- Produces: `func redactKey(plain string) string` — package-private. Returns "head6...tail4" for keys ≥ 12 chars, else returns the full string.

- [ ] **Step 1: Read the existing `redactSecrets` to confirm placement style**

```bash
sed -n '590,620p' /Users/gary/code/agent-platform-backend/internal/gateway/client.go
```

Look for the function body of `redactSecrets` (around line 597) so the new helper matches surrounding style (comments, error handling, naming).

- [ ] **Step 2: Add `redactKey` after `redactSecrets`**

Append at the end of `internal/gateway/client.go`:

```go
// redactKey returns a safe-to-display preview of an API key.
//   "sk-aBcDeFgHiJkLmNoPqRsTuVwXyZ" → "sk-aBcD...XyZ"  (head 6 + "..." + tail 4)
//   < 12 chars                       → full string verbatim (no information to redact)
//
// Format keeps the LiteLLM "sk-" prefix recognizable for operators without
// leaking the secret body. NOT itself a secret — pure projection. Used by
// VirtualKey.Issue / Regenerate to populate the persistent masked_key column.
func redactKey(plain string) string {
    if len(plain) < 12 {
        return plain
    }
    return plain[:6] + "..." + plain[len(plain)-4:]
}
```

- [ ] **Step 3: Build to confirm syntax**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go build ./internal/gateway/...
```

Expected: exit 0, no output.

- [ ] **Step 4: Commit**

```bash
cd /Users/gary/code/agent-platform-backend
git add internal/gateway/client.go
git commit -m "feat(virtualkey): add redactKey helper for masked_key column"
```

---

## Task 2: Rewrite `ent/schema/virtualkey.go`

**Files:**
- Modify: `ent/schema/virtualkey.go` (full rewrite of `Fields()` and `Indexes()`; the package comment also needs updating since "per-user" no longer applies)

**Interfaces:**
- Consumes: nothing
- Produces: New field set. Tasks 3 onward depend on this file's shape.

- [ ] **Step 1: Update the package comment**

In `ent/schema/virtualkey.go`, change the doc comment on `VirtualKey` struct from:

```go
// VirtualKey is a per-user LiteLLM virtual key issued by the gateway (LLD-04).
// The secret itself is Sensitive (never serialized to GraphQL/logs); it is
// delivered to the VM via guestinfo. The DB row holds governance metadata.
```

to:

```go
// VirtualKey is a per-agent-per-org LiteLLM virtual key issued by the
// gateway (LLD-04, refactored 2026-07). The secret (litellm_key) and its
// persistent preview (masked_key) are both Sensitive — neither is ever
// serialized to GraphQL or logs. litellm_key is delivered to the VM via
// guestinfo; masked_key is shown on the operator console. The DB row
// holds governance metadata and is owned by the organization, not a
// single user.
```

- [ ] **Step 2: Rewrite `Fields()` body**

Replace the entire `Fields()` return value in `ent/schema/virtualkey.go` with:

```go
return []ent.Field{
    field.UUID("id", uuid.UUID{}).Default(uuid.New),
    // The LiteLLM virtual key secret — kept out of all output.
    field.String("litellm_key").Sensitive().NotEmpty(),
    // LiteLLM's hashed token (what GET /key/list returns). Stored at issue
    // time so reconciliation matches by it; empty for keys issued before
    // this field.
    field.String("litellm_token").Optional(),
    // Persistent, safe-to-display preview of the secret. Sensitive (we do
    // not want it in logs/JSON even though it is not itself a secret).
    // Format: "head6...tail4" via gateway.redactKey.
    field.String("masked_key").Sensitive().NotEmpty(),
    // Human-readable label, required since 2026-07 refactor (replaces
    // the prior optional alias column).
    field.String("name").NotEmpty(),
    // Organization this key belongs to. Required since the per-agent-per-org
    // refactor; replaces the prior optional "team_id" / "organization_id"
    // pair. Used both as the tenant-scope root and as the LiteLLM team
    // identifier on the wire.
    field.String("organization_id").NotEmpty(),
    field.UUID("agent_id", uuid.UUID{}).Optional().Nillable(),
    // The GatewayConnection that ISSUED this key (LLD-14). Its whole lifecycle
    // (revoke/regenerate/recycle/reconcile) routes by this, decoupled from
    // the organization's *current* gateway binding. NULL for keys issued
    // before this field (or on the legacy injected gateway) → callers fall
    // back to the organization → department → gateway derivation. No FK
    // edge: a key row outlives a gateway delete (audit), and deletion is
    // guarded in app logic.
    //
    // 2026-07 rename: gateway_connection_id → model_gateway_id. Single
    // column carries both the user-facing ModelGateway association and the
    // LLD-14 post-issue lifecycle pin. Required since per-agent-per-org
    // refactor: every VirtualKey MUST be bound to exactly one modelGateway
    // so that IssueVirtualKey can statically pin the secret-spending
    // target. The GraphQL field that reads this column is `modelGateway`
    // (a nested ModelGateway object); see §2.4 in the spec.
    field.UUID("model_gateway_id", uuid.UUID{}),
    field.Strings("models").Optional(),
    field.Float("max_budget").Optional(),
    field.Int("max_parallel_requests").Optional(),
    field.Int("tpm_limit").Optional(),
    field.Int("rpm_limit").Optional(),
    field.String("tpm_limit_type").Optional(), // "guaranteed_throughput" etc
    field.String("rpm_limit_type").Optional(),
    field.String("budget_duration").Optional(),
    field.Enum("status").Values("active", "disabled", "revoked").Default("active"),
    field.Time("expires_at").Optional().Nillable(),
    field.Strings("allowed_routes").Optional(),
    field.Strings("tags").Optional(),
    field.Bool("blocked").Default(false),
    field.String("key_type").Default("default"),
    field.Bool("auto_rotate").Default(false),
    field.String("rotation_interval").Optional(), // "30d" etc
    field.Time("last_active_at").Optional().Nillable(),
    field.Int("spend").Optional().Default(0),
}
```

Notes:
- `alias`, `user_id`, `team_id` are dropped.
- `masked_key`, `name` are added.
- `organization_id` becomes `NotEmpty()`.
- Field order is alphabetical within groups (matches prior style).

- [ ] **Step 3: Rewrite `Indexes()` body**

Replace `Indexes()` return with:

```go
return []ent.Index{
    // Enforce the 1:1 agent↔key invariant at the DB layer so two concurrent
    // IssueVirtualKey calls can't both mint a key for the same agent (the
    // read-then-create check in the resolver is racy). Partial: a revoked
    // key frees the agent to be re-issued; a NULL agent_id (org-level keys
    // not yet bound to an agent) is naturally unconstrained.
    index.Fields("agent_id").Unique().Annotations(entsql.IndexWhere("status <> 'revoked'")),
}
```

Note: the prior `index.Fields("user_id")` is dropped because the column is gone.

- [ ] **Step 4: Visual diff check**

```bash
cd /Users/gary/code/agent-platform-backend
git diff -- ent/schema/virtualkey.go
```

Expected: shows removal of `alias`, `user_id`, `team_id`; addition of `masked_key`, `name`; `organization_id` flipped from `.Optional()` to `.NotEmpty()`; index list trimmed.

- [ ] **Step 5: Commit**

```bash
cd /Users/gary/code/agent-platform-backend
git add ent/schema/virtualkey.go
git commit -m "refactor(virtualkey): rename alias→name, drop user_id/team_id, add masked_key, require organization_id"
```

---

## Task 3: Regenerate ent + generate migration

**Files:**
- Auto-modified: `ent/runtime.go`, `ent/virtualkey.go`, `ent/virtualkey/*.go`, `ent/virtualkey_create.go`, `ent/virtualkey_update.go`, `ent/virtualkey_query.go`, `ent/virtualkey_delete.go`, `ent/virtualkey/where.go`, `ent/migrate/schema.go`
- Auto-modified: `ent/migrate/migrations/atlas.sum`
- New file: `ent/migrate/migrations/YYYYMMDDHHMMSS_vk_per_agent_org.sql` (or similar — `make migrate-diff` decides the name)

**Interfaces:**
- Consumes: schema from Task 2
- Produces: regenerated ent code + a new migration file. All later tasks compile against this.

- [ ] **Step 1: Regenerate ent**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go generate ./ent/...
```

Expected: command exits 0; may print "generated" lines from `entc`.

- [ ] **Step 2: Verify regenerated code compiles**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go build ./ent/...
```

Expected: exit 0. (Other packages referencing the renamed/dropped fields will fail here — that's expected and is fixed in later tasks. If `go build ./ent/...` itself fails, stop and re-check Task 2.)

- [ ] **Step 3: Generate migration**

```bash
cd /Users/gary/code/agent-platform-backend
make migrate-diff name=vk_per_agent_org
```

Expected: a new file appears under `ent/migrate/migrations/`. Inspect it:

```bash
ls -t ent/migrate/migrations/ | head -3
git diff --stat ent/migrate/migrations/
```

Verify the migration DDL contains:
- `DROP COLUMN alias` (or similar — depends on ent atlas version)
- `DROP COLUMN user_id`
- `DROP COLUMN team_id`
- `ADD COLUMN masked_key ... NOT NULL`
- `ADD COLUMN name ... NOT NULL`
- `ALTER COLUMN organization_id SET NOT NULL` (or similar)
- `DROP INDEX` for the old `user_id` index

If the migration looks empty or wildly different, **stop** — likely an ent codegen issue. Re-run Step 1.

- [ ] **Step 4: gofmt**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 gofmt -l .
```

Expected: empty output. (If non-empty, run `GOTOOLCHAIN=go1.25.0 gofmt -w .` and re-check.)

- [ ] **Step 5: Commit generated artifacts and migration**

```bash
cd /Users/gary/code/agent-platform-backend
git add ent/
git commit -m "chore(virtualkey): regenerate ent + migration for per-agent-per-org schema"
```

---

## Task 4: Rewrite `schema/virtualkey.graphql`

**Files:**
- Modify: `schema/virtualkey.graphql` (full rewrite)

**Interfaces:**
- Consumes: schema from Task 2/3
- Produces: New SDL. Tasks 5 onward depend on this file's shape.

- [ ] **Step 1: Replace the file contents**

Write the following to `schema/virtualkey.graphql` (full file replacement):

```graphql
# Per-agent-per-org LiteLLM virtual keys (refactored 2026-07, was per-user).
# See LLD-04 / LLD-07 §8. Secret returned ONCE on issue/regenerate via the
# IssuedVirtualKey wrapper; the persistent maskedKey preview is exposed on
# every VirtualKey read.

enum VirtualKeyStatus {
  active
  disabled
  revoked
}

# RoutePermission — frontend multi-select enum mapped to /v1/* paths
# (LiteLLM design doc §4.2). The form's "Allow All Routes" switch, when ON,
# OMITS the allowed_routes field entirely; when OFF, the form picks one or
# more of these and translates to ["/v1/chat/completions", ...].
enum RoutePermission {
  CHAT
  EMBEDDINGS
  IMAGES
  AUDIO
}

type VirtualKey {
  id: ID!
  # Human-readable label. Required since 2026-07 refactor.
  name: String!
  # Persistent, safe-to-display preview of the secret (e.g. "sk-aBcD...XyZ").
  # Always populated; updated alongside any secret change.
  maskedKey: String!
  # Organization this key belongs to. Required. Drives both tenant
  # isolation and LiteLLM team routing.
  organizationId: String!
  # Nested object: the modelGateway that issued this key. Maps to the ent
  # `model_gateway_id` column (renamed from `gateway_connection_id`).
  # Required since per-agent-per-org refactor — every VirtualKey is bound
  # to exactly one modelGateway. The frontend renders this as the
  # "gateway" pill on the operator console.
  modelGateway: ModelGateway!
  agentId: ID
  models: [String!]!
  maxBudget: Float
  status: VirtualKeyStatus!
  expiresAt: Time
  # Human-readable remaining-lifetime, derived from expiresAt for display
  # (e.g. "30d", "12h", "" when no expiry). Computed by the resolver; not
  # persisted as a separate column.
  duration: String
  createdAt: Time!
  updatedAt: Time!
  # Per-key rate-limit / quota controls (LiteLLM design doc §4.2).
  maxParallelRequests: Int
  tpmLimit: Int
  rpmLimit: Int
  rpmLimitType: String
  tpmLimitType: String
  budgetDuration: String
  # allowed_routes — empty list means "no restriction" (the frontend's
  # "Allow All Routes" switch ON translates to omit-this-field; ON → omit,
  # OFF → fill with /v1/chat/completions etc).
  allowedRoutes: [String!]!
  # Operational metadata (LiteLLM design doc §4.2).
  tags: [String!]!
  blocked: Boolean!
  keyType: String!
  autoRotate: Boolean!
  rotationInterval: String
  # Live spend + last-active (refreshed by the periodic worker; the
  # console's progress bar reads these directly).
  spend: Float!
  lastActiveAt: Time
}

# Returned only at issue / regenerate time — carries the secret, which is
# never queryable again. The virtualKey.maskedKey field is also populated
# here so the operator sees the preview in the same response.
type IssuedVirtualKey {
  virtualKey: VirtualKey!
  secret: String!
}

# Limit-type enum — LiteLLM's per-key quota vocabulary. Optional; if unset,
# LiteLLM defaults to "best_effort" or similar per its own config.
enum LimitType {
  guaranteed_throughput
  best_effort
}

input IssueVirtualKeyInput {
  # Required. Drives tenant scope + LiteLLM team routing.
  organizationId: String!
  # Required. Human-readable label.
  name: String!
  # Required. References the GatewayConnection that issues this key and
  # will receive every model+route check. Resolver verifies each entry in
  # `models` against the gateway's live model list (gatewayAvailableModels)
  # before mint.
  modelGateway: ID!
  # Optional. Can be left unbound at issue and later set via
  # associateVirtualKeyAgent(virtualKeyId, agentId).
  agentId: ID
  # Friendly duration input. Accepts "<n>d" / "<n>h" / "<n>w" / "<n>m".
  # When set, server computes expiresAt = now + duration. If both duration
  # and expiresAt are provided, duration takes precedence (expiresAt is
  # silently overridden; logged once at server side).
  duration: String
  expiresAt: Time
  # Optional. Models named MUST be a subset of `modelGateway`'s live model
  # list (verified server-side via gatewayAvailableModels). Resolver 400s
  # on stale names. Empty = omit (litellm default = no restriction).
  models: [String!]
  maxBudget: Float
  budgetDuration: String
  maxParallelRequests: Int
  rpmLimit: Int
  tpmLimit: Int
  rpmLimitType: String
  tpmLimitType: String
  # allowedRoutes — when the form's "Allow All Routes" switch is ON, the
  # frontend OMITS this field. When OFF, it sends the explicit list.
  allowedRoutes: [String!]
  tags: [String!]
  blocked: Boolean
  # Operational / catalog metadata (LiteLLM design doc §4.2).
  keyType: String
  autoRotate: Boolean
  rotationInterval: String
}

extend type Query {
  # Real-time model list for a modelGateway (calls LiteLLM /model/list on
  # demand — no cache). Frontend uses this to populate the issue form's
  # "Models" multi-select after the operator picks a modelGateway.
  # @hasRole: read_only or admin (matches virtualKeys permissioning).
  gatewayAvailableModels(gatewayConnectionId: ID!): [String!]!
    @hasRole(any: [admin, read_only])

  # organizationId, agentId, and modelGateway are independent optional
  # filters; all null → all keys in the current tenant. Multiple set →
  # intersection.
  virtualKeys(organizationId: ID, agentId: ID, modelGateway: ID): [VirtualKey!]!
    @hasRole(any: [admin, read_only])
}

extend type Mutation {
  issueVirtualKey(input: IssueVirtualKeyInput!): IssuedVirtualKey!
    @hasPermission(perm: "key:manage")
  revokeVirtualKey(id: ID!): Boolean!
    @hasPermission(perm: "key:manage")
  # Rotate the key's secret at the gateway, keeping its governance row/binding.
  # Returns the new secret ONCE (the old one stops working after litellm's
  # grace). The maskedKey on the returned VirtualKey is updated to match the
  # new secret. LLD-04 §3.
  regenerateVirtualKey(id: ID!): IssuedVirtualKey!
    @hasPermission(perm: "key:manage")
  # Toggle enabled/disabled (distinct from revoke, which is terminal).
  setVirtualKeyEnabled(id: ID!, enabled: Boolean!): VirtualKey!
    @hasPermission(perm: "key:manage")
  # Bind (or rebind) an existing VirtualKey to an agent. Enforces the 1:1
  # active-key-per-agent invariant (DB partial unique index is the
  # authoritative gate; the resolver also pre-checks for a clean 409).
  associateVirtualKeyAgent(virtualKeyId: ID!, agentId: ID!): VirtualKey!
    @hasPermission(perm: "key:manage")
}
```

- [ ] **Step 2: Visual sanity check**

```bash
cd /Users/gary/code/agent-platform-backend
grep -c "^type VirtualKey\b\|^input IssueVirtualKeyInput\|^type IssuedVirtualKey\|^extend type \(Query\|Mutation\)" schema/virtualkey.graphql
```

Expected: 6 (VirtualKey, IssueVirtualKeyInput, IssuedVirtualKey, Query, Mutation — and one extra because `^type VirtualKey` matches itself once and `^type IssuedVirtualKey` matches itself once → 5 total). Actually `grep -c` counts lines, so expect 5.

- [ ] **Step 3: Commit**

```bash
cd /Users/gary/code/agent-platform-backend
git add schema/virtualkey.graphql
git commit -m "refactor(virtualkey): rewrite GraphQL SDL — per-agent-per-org model"
```

---

## Task 5: Regenerate gqlgen

**Files:**
- Auto-modified: `internal/graph/model/models_gen.go`, `internal/graph/generated.go`
- May modify: `internal/gql/testdata/client_operations/*.graphql` (gqlgen sometimes regenerates these)

**Interfaces:**
- Consumes: SDL from Task 4
- Produces: regenerated `models_gen.go` + `generated.go`. Tasks 6 onward compile against this.

- [ ] **Step 1: Regenerate gqlgen**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go run -mod=mod github.com/99designs/gqlgen generate
```

Expected: command exits 0; may print "created …" lines.

- [ ] **Step 2: Verify generated code compiles in isolation**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go build ./internal/graph/model/...
```

Expected: exit 0. (Other graph packages will fail because they still reference the old `Alias` / `UserID` fields — that's expected and fixed in Tasks 6/7.)

- [ ] **Step 3: Inspect diff size**

```bash
cd /Users/gary/code/agent-platform-backend
git diff --stat internal/graph/generated.go internal/graph/model/models_gen.go
```

Expected: large diff (this is normal for any GraphQL change). The PR description should call out "generated.go + models_gen.go are auto-regenerated; reviewers should focus on `schema/virtualkey.graphql` and `internal/graph/virtualkey.resolvers.go`."

- [ ] **Step 4: gofmt**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 gofmt -l .
```

Expected: empty output.

- [ ] **Step 5: Commit**

```bash
cd /Users/gary/code/agent-platform-backend
git add internal/graph/generated.go internal/graph/model/models_gen.go internal/gql/testdata/ 2>/dev/null || true
git add -A internal/gql/
git commit -m "chore(virtualkey): regenerate gqlgen for per-agent-per-org schema"
```

---

## Task 6: Rewrite `toModelVirtualKey`

**Files:**
- Modify: `internal/graph/mappers.go` (only the `toModelVirtualKey` function, lines ~115-199)

**Interfaces:**
- Consumes: regenerated `*ent.VirtualKey` (from Task 3) + regenerated `*model.VirtualKey` (from Task 5)
- Produces: updated mapper. Task 7's resolvers depend on this.

- [ ] **Step 1: Read the current mapper**

```bash
sed -n '110,210p' /Users/gary/code/agent-platform-backend/internal/graph/mappers.go
```

Note: after Task 3, the ent field `Alias` and `UserID` are gone. After Task 5, the model fields `Alias`, `UserID`, `TeamID` are gone. The mapper must be rewritten to match.

- [ ] **Step 2: Find any other callers of `toModelVirtualKey`**

```bash
cd /Users/gary/code/agent-platform-backend
grep -rn "toModelVirtualKey" --include="*.go"
```

Expected: only references in `mappers.go` (definition) and `virtualkey.resolvers.go` (callers). Note these for Task 7.

- [ ] **Step 3: Replace `toModelVirtualKey` (signature `(ctx, r, k)`)**

In `internal/graph/mappers.go`, replace the entire `toModelVirtualKey` function (and its doc comment) with:

```go
// formatRemainingDuration returns a human-friendly "remaining lifetime" string
// for display on the console (e.g. "30d", "12h", ""). Pure projection —
// not persisted. Empty when expiresAt is nil.
func formatRemainingDuration(expiresAt *time.Time) string {
    if expiresAt == nil {
        return ""
    }
    d := time.Until(*expiresAt)
    if d <= 0 {
        return "expired"
    }
    days := int(d.Hours() / 24)
    if days >= 1 {
        return fmt.Sprintf("%dd", days)
    }
    hours := int(d.Hours())
    if hours >= 1 {
        return fmt.Sprintf("%dh", hours)
    }
    return fmt.Sprintf("%dm", int(d.Minutes()))
}

// modelsOrEmpty / tagsOrEmpty / routesOrEmpty: nil → []string{} so the
// GraphQL non-null array contract holds even when the ent column is empty.
func modelsOrEmpty(s []string) []string  { if s == nil { return []string{} }; return s }
func tagsOrEmpty(s []string) []string    { if s == nil { return []string{} }; return s }
func routesOrEmpty(s []string) []string  { if s == nil { return []string{} }; return s }

// toModelVirtualKey maps an ent.VirtualKey to the GraphQL model.
//
// Signature is (ctx, r, k) — NOT (k) — because the VirtualKey.modelGateway
// nested object requires a database lookup. The lookup + nested model
// construction happens in this mapper; `r` is the resolver (for Ent
// access) and `ctx` is the request context.
//
// The ModelGateway nested object is set in a second pass (after this
// function returns the bare model) by the caller — Task 6 only sets the
// primitive fields here. See Task 11 for `buildModelGatewayFromConn`.
func toModelVirtualKey(ctx context.Context, r *Resolver, k *ent.VirtualKey) (*model.VirtualKey, error) {
    mg, err := lookupModelGateway(ctx, r, k.ModelGatewayID)
    if err != nil {
        return nil, err
    }
    return &model.VirtualKey{
        ID:             k.ID.String(),
        Name:           k.Name,
        MaskedKey:      k.MaskedKey,
        OrganizationID: k.OrganizationID,
        ModelGateway:   buildModelGatewayFromConn(mg),
        AgentID:        uuidOrNil(k.AgentID),
        Models:         modelsOrEmpty(k.Models),
        Tags:           tagsOrEmpty(k.Tags),
        AllowedRoutes:  routesOrEmpty(k.AllowedRoutes),
        Blocked:        k.Blocked,
        KeyType:        k.KeyType,
        AutoRotate:     k.AutoRotate,
        Spend:          float64(k.Spend),
        Status:         model.VirtualKeyStatus(string(k.Status)),
        CreatedAt:      k.CreatedAt,
        UpdatedAt:      k.UpdatedAt,
        Duration:       formatRemainingDuration(k.ExpiresAt),
        // Optional/nullable pointers via existing ent helpers:
        MaxParallelRequests: k.MaxParallelRequests,
        TpmLimit:            k.TpmLimit,
        RpmLimit:            k.RpmLimit,
        RpmLimitType:        k.RpmLimitType,
        TpmLimitType:        k.TpmLimitType,
        BudgetDuration:      k.BudgetDuration,
        ExpiresAt:           k.ExpiresAt,
        RotationInterval:    k.RotationInterval,
        LastActiveAt:        k.LastActiveAt,
        MaxBudget:           k.MaxBudget,
    }, nil
}

// lookupModelGateway fetches the GatewayConnection that issued this key.
// NotFound is converted to a gqlerror so callers can surface a clean 404.
func lookupModelGateway(ctx context.Context, r *Resolver, id uuid.UUID) (*ent.GatewayConnection, error) {
    c, err := r.Ent.GatewayConnection.Get(ctx, id)
    if err != nil {
        if ent.IsNotFound(err) {
            return nil, gqlerror.Errorf("model gateway not found")
        }
        return nil, err
    }
    return c, nil
}

// uuidOrNil returns the UUID as a string, or nil if the pointer is nil.
// Used to map ent nullable FK pointers to GraphQL optional ID fields.
func uuidOrNil(id *uuid.UUID) *string {
    if id == nil {
        return nil
    }
    s := id.String()
    return &s
}
```

Note: `formatRemainingDuration` uses `time.Time` and `fmt.Sprintf`; both must be imported. `gqlerror` and `ent` packages must also be imported.

- [ ] **Step 4: Add missing imports if go vet fails**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go vet ./internal/graph/... 2>&1 | head -30
```

If it complains about missing `time`, `fmt`, `gqlerror`, or `ent`, add them to the import block. (All stdlib except `gqlerror`/`ent` — those are already in this package's import set.)

- [ ] **Step 5: Commit**

```bash
cd /Users/gary/code/agent-platform-backend
git add internal/graph/mappers.go
git commit -m "refactor(virtualkey): rewrite toModelVirtualKey (ctx,r,k) for per-agent-per-org + ModelGateway nested"
```

---

## Task 7: Rewrite `virtualkey.resolvers.go`

**Files:**
- Modify: `internal/graph/virtualkey.resolvers.go` (all 5 functions: `IssueVirtualKey`, `RegenerateVirtualKey`, `AssociateVirtualKeyAgent` (new), `VirtualKeys`, plus `RevokeVirtualKey` / `SetVirtualKeyEnabled` to compile against the new schema)
- Read first: `internal/graph/gateway_resolve.go` (`buildGatewayKeyClient`, `modelGatewayClientForVK` after rename in Task 8)

**Interfaces:**
- Consumes: mapper from Task 6; new GraphQL types from Task 5; new ent fields from Task 3; `redactKey` from Task 1
- Produces: all 5 resolver functions. The server cannot start without these.

- [ ] **Step 1: Read the current resolver file to understand the layout**

```bash
sed -n '1,40p' /Users/gary/code/agent-platform-backend/internal/graph/virtualkey.resolvers.go
```

Confirm the file's package and imports.

- [ ] **Step 2: Replace the whole file**

Overwrite `internal/graph/virtualkey.resolvers.go` with the following (use `git mv` first if desired; otherwise just `Write` the new content):

```go
package graph

import (
    "context"
    "fmt"
    "log"
    "regexp"
    "strconv"
    "time"

    "github.com/VMware-AI/agent-platform-backend/ent"
    "github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
    "github.com/VMware-AI/agent-platform-backend/internal/auth"
    "github.com/VMware-AI/agent-platform-backend/internal/gateway"
    "github.com/VMware-AI/agent-platform-backend/internal/graph/model"
    "github.com/google/uuid"
    "github.com/vektah/gqlparser/v2/gqlerror"
)

// durationRE matches forms like "30d", "12h", "2w", "90m".
var durationRE = regexp.MustCompile(`^(\d+)([dhwm])$`)

// parseDuration returns time.Duration for "Nd"/"Nh"/"Nw"/"Nm" forms.
// Returns (0, false) when the input does not match.
func parseDuration(s string) (time.Duration, bool) {
    m := durationRE.FindStringSubmatch(s)
    if m == nil {
        return 0, false
    }
    n, _ := strconv.Atoi(m[1])
    switch m[2] {
    case "d":
        return time.Duration(n) * 24 * time.Hour, true
    case "h":
        return time.Duration(n) * time.Hour, true
    case "w":
        return time.Duration(n) * 7 * 24 * time.Hour, true
    case "m":
        return time.Duration(n) * 30 * 24 * time.Hour, true
    }
    return 0, false
}

// derefBool returns *p if non-nil, else def.
func derefBool(p *bool, def bool) bool {
    if p == nil {
        return def
    }
    return *p
}

// derefStr returns *p if non-nil+non-empty, else def.
func derefStr(p *string, def string) string {
    if p == nil || *p == "" {
        return def
    }
    return *p
}

// IssueVirtualKey creates a new LiteLLM key for an organization, bound to
// exactly one modelGateway (the one the operator picked). The plaintext
// secret is returned ONCE in the IssuedVirtualKey wrapper; subsequent
// reads see only maskedKey.
//
// Routing path: input.ModelGateway → GatewayConnection.Get → build client.
// Step 7 performs a real-time cross-check against the gateway's live model
// list before mint — this is the model↔modelGateway strong-coupling
// invariant. Compensating gw.DeleteKey on DB persist failure prevents
// orphans at the gateway side.
func (r *mutationResolver) IssueVirtualKey(ctx context.Context, input model.IssueVirtualKeyInput) (*model.IssuedVirtualKey, error) {
    if input.OrganizationID == "" {
        return nil, gqlerror.Errorf("organizationId is required")
    }
    if input.Name == "" {
        return nil, gqlerror.Errorf("name is required")
    }
    if input.ModelGateway == "" {
        return nil, gqlerror.Errorf("modelGateway is required")
    }

    // 1) Direct gateway lookup by id (replaces the prior
    //    team→department→gateway derivation).
    mgID, err := uuid.Parse(input.ModelGateway)
    if err != nil {
        return nil, gqlerror.Errorf("invalid modelGateway")
    }
    gwConn, err := r.Ent.GatewayConnection.Get(ctx, mgID)
    if err != nil {
        if ent.IsNotFound(err) {
            return nil, gqlerror.Errorf("model gateway not found")
        }
        return nil, err
    }
    gw := r.buildGatewayKeyClient(ctx, gwConn)
    if gw == nil {
        return nil, gqlerror.Errorf("model gateway client unavailable for gateway %q", input.ModelGateway)
    }

    // 2) Optional agent pre-bind (1:1 invariant checked here; DB partial
    //    unique index is the authoritative race gate).
    var agentID *uuid.UUID
    if input.AgentID != nil {
        aID, err := uuid.Parse(*input.AgentID)
        if err != nil {
            return nil, gqlerror.Errorf("invalid agentId")
        }
        existing, qerr := r.Ent.VirtualKey.Query().
            Where(virtualkey.AgentIDEQ(aID), virtualkey.StatusNEQ(virtualkey.StatusRevoked)).
            First(ctx)
        if qerr == nil && existing != nil {
            return nil, gqlerror.Errorf("agent %s already has active key %s", aID, existing.ID)
        }
        if qerr != nil && !ent.IsNotFound(qerr) {
            return nil, qerr
        }
        agentID = &aID
    }

    // 3) Resolve expiry: duration > expiresAt (logged once).
    var expiresAt *time.Time
    if input.Duration != nil && *input.Duration != "" {
        d, ok := parseDuration(*input.Duration)
        if !ok {
            return nil, gqlerror.Errorf("invalid duration format %q (expected <n>d|h|w|m)", *input.Duration)
        }
        t := time.Now().Add(d)
        expiresAt = &t
    } else if input.ExpiresAt != nil {
        expiresAt = input.ExpiresAt
    }

    // 4) Cross-check: every model in input.Models must be in the live
    //    model list of the gateway (gatewayAvailableModels, real-time, no
    //    cache). Backend-call 400s when the operator passed stale names.
    if len(input.Models) > 0 {
        available, lerr := gw.ListAvailableModels(ctx)
        if lerr != nil {
            return nil, fmt.Errorf("list available models from gateway: %w", lerr)
        }
        known := make(map[string]struct{}, len(available))
        for _, m := range available {
            known[m] = struct{}{}
        }
        var stale []string
        for _, m := range input.Models {
            if _, ok := known[m]; !ok {
                stale = append(stale, m)
            }
        }
        if len(stale) > 0 {
            return nil, gqlerror.Errorf("models not available on modelGateway %s: %v", input.ModelGateway, stale)
        }
    }

    // 5) Build gateway request.
    gReq := gateway.GenerateKeyRequest{
        OrganizationID:    input.OrganizationID,
        Models:            input.Models,
        MaxBudget:         input.MaxBudget,
        BudgetDuration:    input.BudgetDuration,
        MaxParallelRequests: input.MaxParallelRequests,
        RpmLimit:          input.RpmLimit,
        TpmLimit:          input.TpmLimit,
        RpmLimitType:      input.RpmLimitType,
        TpmLimitType:      input.TpmLimitType,
        AllowedRoutes:     input.AllowedRoutes,
        Tags:              input.Tags,
        Blocked:           input.Blocked,
        KeyType:           input.KeyType,
        AutoRotate:        input.AutoRotate,
        RotationInterval:  input.RotationInterval,
    }
    if agentID != nil {
        gReq.AgentID = agentID.String()
    }
    if expiresAt != nil {
        gReq.ExpiresAt = expiresAt
    }

    resp, err := gw.GenerateKey(ctx, gReq)
    if err != nil {
        r.audit(ctx, "key.issue", "virtual_key", "", false, actorID(auth.FromContext(ctx)))
        return nil, fmt.Errorf("gateway mint: %w", err)
    }

    // 6) Persist governance row.
    create := r.Ent.VirtualKey.Create().
        SetLitellmKey(resp.Key).
        SetLitellmToken(resp.Token).
        SetMaskedKey(redactKey(resp.Key)).
        SetName(input.Name).
        SetOrganizationID(input.OrganizationID).
        SetModelGatewayID(mgID).
        SetModels(input.Models).
        SetMaxBudget(input.MaxBudget).
        SetMaxParallelRequests(input.MaxParallelRequests).
        SetTpmLimit(input.TpmLimit).
        SetRpmLimit(input.RpmLimit).
        SetTpmLimitType(input.TpmLimitType).
        SetRpmLimitType(input.RpmLimitType).
        SetBudgetDuration(input.BudgetDuration).
        SetTags(input.Tags).
        SetAllowedRoutes(input.AllowedRoutes).
        SetBlocked(derefBool(input.Blocked, false)).
        SetKeyType(derefStr(input.KeyType, "default")).
        SetAutoRotate(derefBool(input.AutoRotate, false)).
        SetRotationInterval(derefStr(input.RotationInterval, ""))
    if expiresAt != nil {
        create = create.SetExpiresAt(*expiresAt)
    }
    if agentID != nil {
        create = create.SetAgentID(*agentID)
    }

    vk, err := create.Save(ctx)
    if err != nil {
        // Compensating revoke on gateway (detached context: original may
        // already be canceled/timed-out).
        cctx := context.WithoutCancel(ctx)
        if derr := gw.DeleteKey(cctx, resp.Key); derr != nil {
            log.Printf("compensating gw.DeleteKey failed: %v", derr)
        }
        r.audit(ctx, "key.issue", "virtual_key", "", false, actorID(auth.FromContext(ctx)))
        return nil, fmt.Errorf("persist virtual_key: %w", err)
    }

    r.audit(ctx, "key.issue", "virtual_key", vk.ID.String(), true, actorID(auth.FromContext(ctx)))

    mapped, merr := toModelVirtualKey(ctx, r, vk)
    if merr != nil {
        return nil, merr
    }
    return &model.IssuedVirtualKey{
        VirtualKey: mapped,
        Secret:     resp.Key,
    }, nil
}

// RevokeVirtualKey disables a key at the gateway FIRST, then flips the DB
// row to status=revoked (terminal). See spec §1.1 invariant #3.
func (r *mutationResolver) RevokeVirtualKey(ctx context.Context, id string) (bool, error) {
    vkID, err := uuid.Parse(id)
    if err != nil {
        return false, gqlerror.Errorf("invalid id")
    }
    vk, err := r.Ent.VirtualKey.Get(ctx, vkID)
    if err != nil {
        return false, err
    }
    gw := r.modelGatewayClientForVK(ctx, vk)
    if gw == nil {
        return false, gqlerror.Errorf("model gateway is not configured; key not revoked at gateway")
    }
    if err := gw.DeleteKey(ctx, vk.LitellmKey); err != nil {
        return false, fmt.Errorf("gateway: %w", err)
    }
    if _, err := r.Ent.VirtualKey.UpdateOne(vk).SetStatus(virtualkey.StatusRevoked).Save(ctx); err != nil {
        return false, err
    }
    r.audit(ctx, "key.revoke", "virtual_key", vkID.String(), true, actorID(auth.FromContext(ctx)))
    return true, nil
}

// RegenerateVirtualKey rotates the secret at the gateway AND keeps the
// governance row intact. The masked_key is rewritten in lock-step with the
// new secret (spec §3.2). The new plaintext is returned ONCE. Persist runs
// under context.WithoutCancel so a client cancel cannot drop the rotation.
func (r *mutationResolver) RegenerateVirtualKey(ctx context.Context, id string) (*model.IssuedVirtualKey, error) {
    vkID, err := uuid.Parse(id)
    if err != nil {
        return nil, gqlerror.Errorf("invalid id")
    }
    vk, err := r.Ent.VirtualKey.Get(ctx, vkID)
    if err != nil {
        return nil, err
    }
    if vk.Status == virtualkey.StatusRevoked {
        return nil, gqlerror.Errorf("key is revoked and cannot be regenerated")
    }
    gw := r.modelGatewayClientForVK(ctx, vk)
    if gw == nil {
        return nil, gqlerror.Errorf("model gateway is not configured; key not regenerated at gateway")
    }
    resp, err := gw.RegenerateKey(ctx, vk.LitellmKey)
    if err != nil {
        r.audit(ctx, "key.regenerate", "virtual_key", vkID.String(), false, actorID(auth.FromContext(ctx)))
        return nil, fmt.Errorf("gateway regenerate: %w", err)
    }
    updated, err := r.Ent.VirtualKey.UpdateOneID(vkID).
        SetLitellmKey(resp.Key).
        SetLitellmToken(resp.Token).
        SetMaskedKey(redactKey(resp.Key)).
        Save(context.WithoutCancel(ctx))
    if err != nil {
        r.audit(ctx, "key.regenerate", "virtual_key", vkID.String(), false, actorID(auth.FromContext(ctx)))
        return nil, fmt.Errorf("persist regenerated virtual_key: %w", err)
    }
    r.audit(ctx, "key.regenerate", "virtual_key", vkID.String(), true, actorID(auth.FromContext(ctx)))
    mapped, merr := toModelVirtualKey(ctx, r, updated)
    if merr != nil {
        return nil, merr
    }
    return &model.IssuedVirtualKey{
        VirtualKey: mapped,
        Secret:     resp.Key,
    }, nil
}

// SetVirtualKeyEnabled toggles active/disabled (NOT revoke — see spec
// §1.1 invariant). Gateway-first ordering preserved.
func (r *mutationResolver) SetVirtualKeyEnabled(ctx context.Context, id string, enabled bool) (*model.VirtualKey, error) {
    vkID, err := uuid.Parse(id)
    if err != nil {
        return nil, gqlerror.Errorf("invalid id")
    }
    vk, err := r.Ent.VirtualKey.Get(ctx, vkID)
    if err != nil {
        return nil, err
    }
    if vk.Status == virtualkey.StatusRevoked {
        return nil, gqlerror.Errorf("key is revoked and cannot be re-enabled")
    }
    gw := r.modelGatewayClientForVK(ctx, vk)
    if gw == nil {
        return nil, gqlerror.Errorf("model gateway is not configured; key not updated at gateway")
    }
    blocked := !enabled
    if err := gw.UpdateKey(ctx, vk.LitellmKey, gateway.UpdateKeyRequest{Blocked: &blocked}); err != nil {
        return nil, fmt.Errorf("gateway update: %w", err)
    }
    status := virtualkey.StatusActive
    if !enabled {
        status = virtualkey.StatusDisabled
    }
    updated, err := r.Ent.VirtualKey.UpdateOne(vk).SetStatus(status).Save(ctx)
    if err != nil {
        return nil, err
    }
    r.audit(ctx, "key.set_enabled", "virtual_key", vkID.String(), true, actorID(auth.FromContext(ctx)))
    return toModelVirtualKey(ctx, r, updated)
}

// AssociateVirtualKeyAgent binds (or rebinds) an existing key to an agent.
// Enforces the 1:1 active-key-per-agent invariant (DB partial unique index
// is the authoritative race gate; pre-check provides a clean 409).
func (r *mutationResolver) AssociateVirtualKeyAgent(ctx context.Context, virtualKeyId string, agentId string) (*model.VirtualKey, error) {
    vkID, err := uuid.Parse(virtualKeyId)
    if err != nil {
        return nil, gqlerror.Errorf("invalid virtualKeyId")
    }
    aID, err := uuid.Parse(agentId)
    if err != nil {
        return nil, gqlerror.Errorf("invalid agentId")
    }
    if _, err := r.Ent.VirtualKey.Get(ctx, vkID); err != nil {
        return nil, err
    }
    existing, qerr := r.Ent.VirtualKey.Query().
        Where(virtualkey.AgentIDEQ(aID), virtualkey.StatusNEQ(virtualkey.StatusRevoked)).
        First(ctx)
    if qerr == nil && existing != nil && existing.ID != vkID {
        return nil, gqlerror.Errorf("agent %s already has active key %s", aID, existing.ID)
    }
    if qerr != nil && !ent.IsNotFound(qerr) {
        return nil, qerr
    }
    updated, err := r.Ent.VirtualKey.UpdateOneID(vkID).SetAgentID(aID).Save(ctx)
    if err != nil {
        return nil, err
    }
    r.audit(ctx, "key.associate_agent", "virtual_key", vkID.String(), true, actorID(auth.FromContext(ctx)))
    return toModelVirtualKey(ctx, r, updated)
}

// VirtualKeys lists keys with three optional filters: organizationId,
// agentId, modelGateway. All null → all keys in current tenant. Multiple
// set → intersection. Secrets are never returned (only maskedKey on each
// row).
func (r *queryResolver) VirtualKeys(ctx context.Context, organizationId *string, agentId *string, modelGateway *string) ([]model.VirtualKey, error) {
    q := r.Ent.VirtualKey.Query()
    if organizationId != nil {
        q = q.Where(virtualkey.OrganizationIDEQ(*organizationId))
    }
    if agentId != nil {
        aID, err := uuid.Parse(*agentId)
        if err != nil {
            return nil, gqlerror.Errorf("invalid agentId")
        }
        q = q.Where(virtualkey.AgentIDEQ(aID))
    }
    if modelGateway != nil {
        mgID, err := uuid.Parse(*modelGateway)
        if err != nil {
            return nil, gqlerror.Errorf("invalid modelGateway")
        }
        q = q.Where(virtualkey.ModelGatewayIDEQ(mgID))
    }
    // TODO(follow-up): replace this denyAll fallback with a real
    // organizationId → tenant resolution (see spec §3.4 + §7 follow-ups).
    if d := tenantScopeFor(ctx); d.apply && d.denyAll {
        q = q.Where(virtualkey.OrganizationIDEQ("")) // impossible match
    }
    keys, err := q.Order(orderNewest).All(ctx)
    if err != nil {
        return nil, err
    }
    out := make([]model.VirtualKey, 0, len(keys))
    for _, k := range keys {
        mapped, merr := toModelVirtualKey(ctx, r, k)
        if merr != nil {
            return nil, merr
        }
        out = append(out, *mapped)
    }
    return out, nil
}

// GatewayAvailableModels returns the live model list advertised by the
// given modelGateway. Real-time: every call hits LiteLLM /model/list on
// demand (no cache). Used by the operator-console issue form to populate
// the "Models" multi-select after the operator picks a modelGateway.
func (r *queryResolver) GatewayAvailableModels(ctx context.Context, gatewayConnectionId string) ([]string, error) {
    mgID, err := uuid.Parse(gatewayConnectionId)
    if err != nil {
        return nil, gqlerror.Errorf("invalid gatewayConnectionId")
    }
    conn, err := r.Ent.GatewayConnection.Get(ctx, mgID)
    if err != nil {
        if ent.IsNotFound(err) {
            return nil, gqlerror.Errorf("model gateway not found")
        }
        return nil, err
    }
    gw := r.buildGatewayKeyClient(ctx, conn)
    if gw == nil {
        return nil, gqlerror.Errorf("model gateway client unavailable")
    }
    return gw.ListAvailableModels(ctx)
}
```

Notes on the snippet:
- Imports must include `strconv` (used by `parseDuration`).
- The old `deptIDFromOrg` and `gatewayKeyClientForVK` paths are gone. Instead:
  - `r.modelGatewayClientForVK(ctx, vk)` is the renamed `gatewayKeyClientForVK` helper (Task 8 renames both).
  - `r.buildGatewayKeyClient(ctx, conn)` is unchanged from the existing internal helper.
- `toModelVirtualKey` signature is now `(ctx, r *Resolver, k *ent.VirtualKey) (*model.VirtualKey, error)` because the new ModelGateway nested object needs both ctx and the resolver to look up the gateway (Task 6).
- The `TODO` block in `VirtualKeys` matches the spec's §3.4 / §7 follow-up.

- [ ] **Step 3: gofmt + go vet**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 gofmt -l internal/graph/virtualkey.resolvers.go
GOTOOLCHAIN=go1.25.0 go vet ./internal/graph/...
```

Expected: empty `gofmt` output; vet either clean or with errors only in resolvers that haven't been migrated yet (Task 8 covers those, and Task 11 supplies the new client method).

- [ ] **Step 4: Commit**

```bash
cd /Users/gary/code/agent-platform-backend
git add internal/graph/virtualkey.resolvers.go
git commit -m "refactor(virtualkey): rewrite resolvers — Issue(modelGateway direct + /model/list cross-check) / Regenerate / AssociateAgent / VirtualKeys(organizationId, agentId, modelGateway) / GatewayAvailableModels"
```
        return time.Duration(n) * time.Hour, true
## Task 8: Remove `revokeUserKeys` + update `DeleteUser` + rename `gatewayKeyClientForVK` → `modelGatewayClientForVK`

**Files:**
- Modify: `internal/graph/gateway_resolve.go` (rename helper; clean up dead `deptIDFromTeam` since the per-agent-per-org refactor no longer routes via team→department)
- Modify: `internal/graph/tenant_scope.go` (comment update for the new org-scoped tenant invariant)
- Modify: `internal/graph/account_helpers.go` (delete `revokeUserKeys`)
- Modify: `internal/graph/account.resolvers.go` (`DeleteUser` no longer calls `revokeUserKeys`)
- Modify: 3 call sites of `gatewayKeyClientForVK` (the deploy + department resolvers + Task 7's resolver)

**Interfaces:**
- Consumes: nothing new
- Produces: clean helper naming; tenant scope comment updated; `DeleteUser` no longer cascading.

- [ ] **Step 1: Rename `gatewayKeyClientForVK` → `modelGatewayClientForVK`**

In `internal/graph/gateway_resolve.go`:

```bash
cd /Users/gary/code/agent-platform-backend
grep -rn "gatewayKeyClientForVK\|gatewayKeyClientForConn" --include="*.go"
```

For each hit:
- Rename the function definition in `gateway_resolve.go` from `gatewayKeyClientForVK` to `modelGatewayClientForVK`.
- Rename `gatewayKeyClientForConn` to `modelGatewayClientForConn`.
- Update all callers (3 places: `deploy.resolvers.go`, `deploy_targets.go`, `department.resolvers.go`, plus the new `virtualkey.resolvers.go` which calls `modelGatewayClientForVK` per Task 7).
- Update the doc comment to mention "modelGateway" rather than "gateway_connection_id".

- [ ] **Step 2: Drop the dead `deptIDFromTeam` helper**

The per-agent-per-org refactor routes directly through `modelGateway` (the user's input) — there is no longer a "team → department → gateway" derivation. `deptIDFromTeam` (in `gateway_resolve.go`) is now unused.

```bash
cd /Users/gary/code/agent-platform-backend
grep -rn "deptIDFromTeam\|deptIDFromOrg" --include="*.go"
```

Expected: zero hits (Task 7's `IssueVirtualKey` does NOT call it; we route by `modelGateway` directly via `r.Ent.GatewayConnection.Get`).

If confirmed unused, delete `deptIDFromTeam` from `gateway_resolve.go` along with its doc comment.

- [ ] **Step 3: Update tenant_scope.go comment**

In `internal/graph/tenant_scope.go`, find any line that mentions "virtual key" or "user" in the context of tenant isolation, and update the comment. Specifically:

```bash
cd /Users/gary/code/agent-platform-backend
grep -n "virtual key\|user.*tenant\|tenant.*user" internal/graph/tenant_scope.go
```

Replace any such comment with the new invariant: "A virtual key belongs to its organization; the organization's tenant is the unit of isolation. P0 follow-up: wire organization→tenant resolution (see plan §11 follow-ups)."

- [ ] **Step 4: Delete `revokeUserKeys`**

Open `internal/graph/account_helpers.go`. The function is at line 24. Delete it entirely (lines 24–47 plus any imports that become unused).

Verify no other caller:

```bash
cd /Users/gary/code/agent-platform-backend
grep -rn "revokeUserKeys" --include="*.go"
```

Expected: zero hits after deletion. If `DeleteUser` still references it, fix in Step 5.

- [ ] **Step 5: Update `DeleteUser`**

In `internal/graph/account.resolvers.go` (around line 113):

- Remove the call to `r.revokeUserKeys(ctx, uid)` (around line 126).
- Update the surrounding comment (around line 121-125) to:
  ```go
  // Virtual keys are NOT cascade-revoked on user delete — keys belong to
  // the organization, not the user. The administrator must explicitly
  // revoke them. (See plan / spec §4.)
  ```

- [ ] **Step 6: Build the entire graph package**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go build ./...
```

Expected: exit 0. If `*_test.go` files fail to compile (e.g. `virtualkey_policy_test.go`, `e2e_perms_test.go`), that's **expected per CLAUDE.md §1** — note the failures in the PR description but do NOT fix them in this PR.

- [ ] **Step 7: gofmt**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 gofmt -l .
```

Expected: empty output.

- [ ] **Step 8: Commit**

```bash
cd /Users/gary/code/agent-platform-backend
git add internal/graph/gateway_resolve.go internal/graph/tenant_scope.go internal/graph/account_helpers.go internal/graph/account.resolvers.go internal/graph/deploy.resolvers.go internal/graph/deploy_targets.go internal/graph/department.resolvers.go
git commit -m "refactor(virtualkey): drop revokeUserKeys cascade, rename gatewayKeyClientForVK→modelGatewayClientForVK, drop deptIDFromTeam"
```

---

## Task 9: Sync docs + postman fixtures + `make docs`

**Files:**
- Modify: `docs/api/virtual-keys.md`
- Modify: `postman/*.json` (postman collection)
- Modify: `internal/gql/testdata/client_operations/*.graphql` (gqlgen client fixtures, if not auto-regenerated in Task 5)
- Auto-modified: `docs/schema.graphql` (from `make docs`)

**Interfaces:**
- Consumes: everything in Tasks 1–8
- Produces: human-readable docs + postman collection matches the new schema.

- [ ] **Step 1: Run `make docs`**

```bash
cd /Users/gary/code/agent-platform-backend
make docs
```

Expected: command exits 0; `docs/schema.graphql` is regenerated. Diff it:

```bash
cd /Users/gary/code/agent-platform-backend
git diff --stat docs/schema.graphql
```

Expected: large diff (mirroring the SDL change).

- [ ] **Step 2: Rewrite `docs/api/virtual-keys.md`**

Open `docs/api/virtual-keys.md`. Replace its entire content (or at minimum: the type tables, the query/mutation list, and the input field tables) to match the new schema. Key points:

- Replace the `VirtualKey` field table to include `name`, `maskedKey`, `organizationId`, `duration`, `updatedAt` and **drop** `alias`, `userId`, `teamId`.
- Replace the `IssueVirtualKeyInput` field table to mark `organizationId` and `name` as required, drop `userId` / `teamId` / `alias`, add `duration`.
- Update the `virtualKeys` query signature to `(organizationId: ID, agentId: ID)`.
- Add a new section for `associateVirtualKeyAgent` mutation with its description.
- Note: "Secrets are returned ONCE via `IssuedVirtualKey.secret`; `VirtualKey.maskedKey` is the persistent preview shown on the operator console."

Suggested template:

```markdown
# Virtual Keys API

## Overview

Per-agent-per-org LiteLLM virtual keys. Each key belongs to an organization
and may be optionally bound to an agent via `associateVirtualKeyAgent`.
Secrets are returned ONCE (on issue / regenerate) and never re-readable.

## Types

### VirtualKey
| Field | Type | Notes |
| --- | --- | --- |
| id | ID! | primary key |
| name | String! | human-readable label |
| maskedKey | String! | persistent preview of the secret |
| organizationId | String! | tenant scope + LiteLLM team |
| agentId | ID | optional, bound via associateVirtualKeyAgent |
| models | [String!]! | allowed models |
| status | VirtualKeyStatus! | active / disabled / revoked |
| duration | String | human-readable remaining lifetime |
| ... | ... | (full table mirrors SDL) |

### IssuedVirtualKey
| Field | Type | Notes |
| --- | --- | --- |
| virtualKey | VirtualKey! | includes maskedKey |
| secret | String! | plaintext, returned ONCE |

## Queries
- `virtualKeys(organizationId: ID, agentId: ID): [VirtualKey!]!`

## Mutations
- `issueVirtualKey(input: IssueVirtualKeyInput!): IssuedVirtualKey!`
- `revokeVirtualKey(id: ID!): Boolean!`
- `regenerateVirtualKey(id: ID!): IssuedVirtualKey!`
- `setVirtualKeyEnabled(id: ID!, enabled: Boolean!): VirtualKey!`
- `associateVirtualKeyAgent(virtualKeyId: ID!, agentId: ID!): VirtualKey!` — bind/rebind a key to an agent

## Inputs
### IssueVirtualKeyInput
| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| organizationId | String! | yes | tenant + LiteLLM team |
| name | String! | yes | human-readable label |
| agentId | ID | no | pre-bind at issue time |
| duration | String | no | "<n>d\|h\|w\|m" — server converts to expiresAt |
| expiresAt | Time | no | absolute expiry (overridden by duration if both given) |
| ... | ... | ... | (full table mirrors SDL) |
```

(Fill in the full table from the SDL.)

- [ ] **Step 3: Update postman fixtures**

```bash
cd /Users/gary/code/agent-platform-backend
grep -rn "alias\|userId\|teamId" postman/ 2>/dev/null | head -20
```

For each match in `*VirtualKey*.json` (or whichever postman files reference the old fields), update:
- Replace `alias` with `name`.
- Remove `userId`, `teamId` fields from sample bodies.
- Add `organizationId`, `maskedKey` to sample responses (where applicable).
- Add an example body for `associateVirtualKeyAgent`.

- [ ] **Step 4: Update client_operations fixtures (if they exist)**

```bash
cd /Users/gary/code/agent-platform-backend
ls internal/gql/testdata/client_operations/ 2>/dev/null
```

If there are `IssueVirtualKey.graphql`, `RegenerateVirtualKey.graphql`, `SetVirtualKeyEnabled.graphql`, `VirtualKeys.graphql` fixtures, update their bodies to match the new schema. Re-run `gqlgen generate` if needed (Step 5 in Task 5 should have done this; verify).

- [ ] **Step 5: Re-run `make docs` (catch-up)**

```bash
cd /Users/gary/code/agent-platform-backend
make docs
GOTOOLCHAIN=go1.25.0 gofmt -l .
```

Expected: both commands exit cleanly.

- [ ] **Step 6: Commit**

```bash
cd /Users/gary/code/agent-platform-backend
git add docs/api/virtual-keys.md docs/schema.graphql postman/ internal/gql/testdata/
git commit -m "docs(virtualkey): sync docs + postman fixtures with per-agent-per-org schema"
```

---

## Task 10: Final verification sweep

**Files:**
- All tasks' outputs

**Interfaces:**
- Verifies: CI gates per CLAUDE.md §2

- [ ] **Step 1: gofmt**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 gofmt -l .
```

Expected: empty. (If non-empty: `GOTOOLCHAIN=go1.25.0 gofmt -w .` then re-check.)

- [ ] **Step 2: go vet**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go vet ./...
```

Expected: clean on production code. **Test files** may fail (e.g. `virtualkey_policy_test.go`, `e2e_perms_test.go`) — that's the explicit CLAUDE.md §1 exception. Note in the PR description which test files fail.

- [ ] **Step 3: go build**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go build ./...
```

Expected: exit 0. (Same caveat as Step 2 about tests.)

- [ ] **Step 4: docs-check**

```bash
cd /Users/gary/code/agent-platform-backend
make docs
```

Expected: clean. If it produces a diff, you missed something in Task 9.

- [ ] **Step 5: migration-drift (already produced in Task 3; verify nothing else drifted)**

```bash
cd /Users/gary/code/agent-platform-backend
git status ent/migrate/
```

Expected: only the migration file added in Task 3 is present; no other untracked migrations.

- [ ] **Step 6: Final commit (amend if needed)**

If any formatting or doc drift was fixed in Steps 1–5:

```bash
cd /Users/gary/code/agent-platform-backend
git add -A
git commit -m "chore(virtualkey): final cleanup — gofmt/vet/docs post-merge"
```

Otherwise skip.

- [ ] **Step 7: PR description**

Write a PR description with:
- Title: `refactor(virtualkey): per-agent-per-org rewrite (LLD-04, 2026-07)`
- Summary: 2–3 lines linking to the spec at `docs/superpowers/specs/2026-07-06-virtualkey-per-agent-org-design.md`.
- List of behavior changes (drop user_id, drop team_id, new maskedKey, new mutation, no DeleteUser cascade, new modelGateway field with strong-coupling cross-check, new gatewayAvailableModels query).
- List of generated artifacts reviewers can skim (`ent/`, `internal/graph/generated.go`, `internal/graph/model/models_gen.go`, `docs/schema.graphql`).
- Note CLAUDE.md §1 exception for tests.
- P0 follow-ups: `tenantScopeFor` ↔ `organizationId` alignment (spec §7); frontend issue-form refactor to use `modelGateway` + `gatewayAvailableModels` instead of ModelRoute multi-select (out of scope per spec §7).

---

## Task 11: Add `gw.ListAvailableModels` client method + `GatewayAvailableModels` resolver + `buildModelGatewayFromConn` helper

**Files:**
- Modify: `internal/gateway/client.go` (add `ListAvailableModels` method on `HTTPClient` + extend the `Client` interface)
- Modify: `internal/gateway/client.go` (low-level `get` already exists — reuse it; only add the typed response struct)
- Modify: `internal/graph/modelgateway.resolvers.go` (extract `buildModelGatewayFromConn` helper)
- Modify: `internal/graph/mappers.go` (use `buildModelGatewayFromConn` in `toModelVirtualKey`)

**Interfaces:**
- Consumes: `*HTTPClient` (Task 7's IssueVirtualKey and GatewayAvailableModels already call it).
- Produces: `gw.ListAvailableModels(ctx) ([]string, error)` — calls LiteLLM `/model/list`, returns just the model name strings.
- Produces: `buildModelGatewayFromConn(*ent.GatewayConnection) *model.ModelGateway` — single source of truth for the ent→GraphQL field mapping (currently inlined in `ModelGatewayById`).

- [ ] **Step 1: Read the existing `ListKeys` method to confirm the GET path**

```bash
sed -n '278,310p' /Users/gary/code/agent-platform-backend/internal/gateway/client.go
```

This is the closest analogue — same shape (GET request, JSON array of items).

- [ ] **Step 2: Add `ListAvailableModels` to the `Client` interface**

In `internal/gateway/client.go`, find the `Client` interface (around line 132). Add:

```go
    // ListAvailableModels enumerates the models the gateway currently
    // advertises (GET /model/list). Used by gatewayAvailableModels query
    // and by IssueVirtualKey's pre-mint cross-check. Real-time; no cache.
    ListAvailableModels(ctx context.Context) ([]string, error)
```

- [ ] **Step 3: Add the HTTPClient implementation**

Append to `internal/gateway/client.go`, after the `ListTeams` method:

```go
// ListAvailableModels enumerates the gateway's model catalog via
// GET /model/list. LiteLLM returns a top-level array of objects whose
// `id` field is the model name used by /key/generate's `models` param.
func (c *HTTPClient) ListAvailableModels(ctx context.Context) ([]string, error) {
    var raw []struct {
        ID string `json:"id"`
    }
    if err := c.get(ctx, "/model/list", &raw); err != nil {
        return nil, err
    }
    models := make([]string, 0, len(raw))
    for _, m := range raw {
        if m.ID == "" {
            continue // unidentifiable entry
        }
        models = append(models, m.ID)
    }
    return models, nil
}
```

- [ ] **Step 4: Build to confirm**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go build ./internal/gateway/...
```

Expected: exit 0.

- [ ] **Step 5: Extract `buildModelGatewayFromConn` helper**

In `internal/graph/modelgateway.resolvers.go`, locate the `ModelGatewayById` resolver (search for `func.*ModelGatewayById`). Extract the field-by-field mapping (id, name, provider, endpoint, backendModelCount, loadBalancingStrategy, lastSyncAt, lastSyncStatus, lastSyncMessage, createdAt, updatedAt) into a package-level helper:

```go
// buildModelGatewayFromConn constructs the GraphQL ModelGateway from an
// ent.GatewayConnection. Used by both ModelGatewayById and
// toModelVirtualKey (which needs the same field mapping for the
// VirtualKey.modelGateway nested object — see spec §3.7).
func buildModelGatewayFromConn(c *ent.GatewayConnection) *model.ModelGateway {
    return &model.ModelGateway{
        ID:                   c.ID.String(),
        Name:                 c.Name,
        Provider:             model.ModelGatewayProvider(c.Provider),
        Endpoint:             c.Endpoint,
        BackendModelCount:    c.BackendModelCount,
        LoadBalancingStrategy: loadBalancingStrategyPtr(c.LoadBalancingStrategy),
        LastSyncAt:           c.LastSyncAt,
        LastSyncStatus:       model.ModelGatewaySyncState(c.LastSyncStatus),
        LastSyncMessage:      c.LastSyncMessage,
        CreatedAt:            c.CreatedAt,
        UpdatedAt:            c.UpdatedAt,
    }
}
```

Replace the inline mapping in `ModelGatewayById` with a single call to this helper.

- [ ] **Step 6: Verify `buildModelGatewayFromConn` is used by `toModelVirtualKey`**

By Task 6, `toModelVirtualKey` already calls `buildModelGatewayFromConn(mg)` (Task 6 step 3 inlines that call). Verify it compiles in isolation. If Task 6's mapper imports `modelgateway.resolvers.go` package indirectly, this should already work; otherwise, the helper function may need to live in `mappers.go` rather than `modelgateway.resolvers.go`. Pragmatic: put `buildModelGatewayFromConn` in `modelgateway.resolvers.go` (where it naturally lives alongside the existing logic), then `mappers.go` calls it across package boundaries. If that's awkward, move the helper into a shared `internal/graph/modelgateway_helpers.go` file.

- [ ] **Step 7: Build + vet**

```bash
cd /Users/gary/code/agent-platform-backend
GOTOOLCHAIN=go1.25.0 go build ./...
GOTOOLCHAIN=go1.25.0 go vet ./internal/graph/... ./internal/gateway/...
GOTOOLCHAIN=go1.25.0 gofmt -l .
```

Expected: all exit 0 / empty output. (Test files may still fail per CLAUDE.md §1 — note in PR.)

- [ ] **Step 8: Commit**

```bash
cd /Users/gary/code/agent-platform-backend
git add internal/gateway/client.go internal/graph/modelgateway.resolvers.go
git commit -m "feat(virtualkey): add gw.ListAvailableModels + extract buildModelGatewayFromConn"
```

---

## Self-Review

1. **Spec coverage:**
   - §1.1 invariants: covered by Tasks 1 (redactKey format), 6 (mapper never reads litellm_key), 7 (gateway-first ordering, regenerate masked_key rewrite, DB 1:1 unique index check).
   - §2 schema: Tasks 2 + 3.
   - §2.4 GraphQL types: Tasks 4 + 5.
   - §3.1 IssueVirtualKey: Task 7.
   - §3.2 regenerate rewrite: Task 7.
   - §3.3 associateAgent: Task 7.
   - §3.4 VirtualKeys query: Task 7 (with TODO follow-up).
   - §3.5 mapper: Task 6.
   - §4 removals: Task 8.
   - §5 error handling: Task 7 includes parseDuration, 1:1 conflict, gw errors.
   - §6 verification: Task 10.
   - §7 out-of-scope: explicitly deferred (tenant alignment + DeployedAgent.virtualKeySecret maskedKey).

2. **Placeholder scan:** No TBD/TODO in the plan except the explicit §3.4 follow-up comment in Task 7 — that's the documented P0 follow-up, not a placeholder.

3. **Type consistency:**
   - `redactKey` (Task 1) → used in Task 7 (IssueVirtualKey + RegenerateVirtualKey). Same signature: `func redactKey(plain string) string`.
   - `formatRemainingDuration` (Task 6) → used by `toModelVirtualKey`. Same signature.
   - `parseDuration` (Task 7) → used only inside Task 7. Same signature.
   - `derefBool` / `derefStr` (Task 7) → local helpers in resolver file. Only used there.
   - `uuidOrNil` (Task 6) → used in `toModelVirtualKey`. Only used there.
   - `modelGatewayClientForVK` (Task 8) → renamed from `gatewayKeyClientForVK`. Used by IssueVirtualKey (none — replaced with direct GatewayConnection.Get), RevokeVirtualKey, RegenerateVirtualKey, SetVirtualKeyEnabled, and the 3 deploy/department helpers. Same signature.
   - `toModelVirtualKey` (Task 6) → signature `(ctx, r *Resolver, k *ent.VirtualKey) (*model.VirtualKey, error)` after Task 6; callers in Task 7 (4 resolvers + 1 query) updated to pass `ctx, r`.
   - `gw.ListAvailableModels` (Task 11) → new client method. Used by Task 7's IssueVirtualKey cross-check + GatewayAvailableModels query.
   - `buildModelGatewayFromConn` (Task 11) → extracted helper from modelgateway.resolvers.go's ModelGatewayById. Used by Task 6's `toModelVirtualKey`.

4. **Notes for implementer:**
   - Task 7's IssueVirtualKey calls `gw.ListAvailableModels` (Task 11). The two tasks must land in order, OR Task 7 leaves a stubbed error path until Task 11 ships.
   - Task 7's `toModelVirtualKey(ctx, r, k)` requires the model-package helper `buildModelGatewayFromConn` (Task 11). Same ordering constraint as above.
   - All `??` and `deptIDFromOrg` placeholders were folded into Task 7's final form (the previous intermediate steps were merged). No further placeholder work needed.