# VirtualKey user_id Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `user_id` field to the VirtualKey GraphQL type, `IssueVirtualKeyInput` GraphQL input, the `ent.VirtualKey` table, and the `gateway.GenerateKeyRequest` wire body — front-end supplies the value, back-end just pipes it through (no defaulting).

**Architecture:** Schema-first edit (`schema/virtualkey.graphql` + `ent/schema/virtualkey.go`), then `make generate` to regenerate gqlgen + ent code, then plumb `input.UserID` through `IssueVirtualKey` (validator + ent Set + gateway wire). Docs regenerated via `make docs`. No versioned migration (CLAUDE.md §2 dev-stage adjustment — `ent.Client.Schema.Create()` covers the dev DB).

**Tech Stack:** Go 1.25 (CI version, see CLAUDE.md §3 — use `GOTOOLCHAIN=go1.25.0`), ent (FK-less `String` field), gqlgen, GraphQL SDL split across `schema/*.graphql`, internal `gateway` package.

## Global Constraints

- Go toolchain pin (CLAUDE.md §3): every `go` / `gofmt` command MUST use `GOTOOLCHAIN=go1.25.0`.
- Regeneration command (CLAUDE.md §3): `GOTOOLCHAIN=go1.25.0 go generate ./ent/...` + `go run github.com/99designs/gqlgen generate`.
- Branch scope (CLAUDE.md §4): one PR, one module. Don't touch anything outside virtualkey + virtualkey plumbing.
- Tests: do NOT write or run unit tests (CLAUDE.md §1 dev-stage). Existing tests stay as-is.
- Migration: do NOT write versioned SQL (CLAUDE.md §2 dev-stage adjustment). Dev DB is bootstrapped by `ent.Client.Schema.Create()`.
- File-comment density: match surrounding code (existing resolvers carry `// step N ...` style notes).

---

### Task 1: Add `user_id` column to ent schema

**Files:**
- Modify: `ent/schema/virtualkey.go:24-72` (inside the `Fields()` slice)

**Interfaces:**
- Consumes: nothing (this is the first change)
- Produces: a `user_id` column on `ent.virtual_key` named `user_id` (FieldUserID = "user_id"). After Task 2's regen, downstream tasks consume `ent.VirtualKey.Create().SetUserID(string)` and `ent/virtualkey.UserIDEQ(uuid.UUID)` etc.

- [ ] **Step 1: Edit `ent/schema/virtualkey.go`**

Open `ent/schema/virtualkey.go`. Inside `func (VirtualKey) Fields() []ent.Field { return []ent.Field { ... } }`, add a new line right after the existing `field.String("rotation_interval").Optional(), // "30d" etc` line:

```go
		// 前端传入的 user_id;与 gateway.GenerateKeyRequest.UserID 直传。
		// NotEmpty(dev-only:历史行没这个列,prod ALTER 会失败 — 后续 prod
		// 数据迁移是另一个工作,见 spec out-of-scope)。
		field.String("user_id").NotEmpty(),
```

Order does not matter for ent generation, but match the convention of placing it near other governance/audit-ish fields. The shape to add verbatim:

```go
		field.String("user_id").NotEmpty(),
```

with the 3-line comment above it. Do not touch `Indexes()`, `Edges()` (none today), or `Mixin()`.

- [ ] **Step 2: Sanity-check formatting**

Run:
```bash
GOTOOLCHAIN=go1.25.0 gofmt -l ent/schema/virtualkey.go
```
Expected: no output (file already formatted). If a filename is printed, run `GOTOOLCHAIN=go1.25.0 gofmt -w ent/schema/virtualkey.go` and re-check.

- [ ] **Step 3: Commit**

```bash
git add ent/schema/virtualkey.go
git commit -m "feat(virtualkey): add user_id column to ent schema"
```

---

### Task 2: Regenerate ent code

**Files:**
- Modify (regenerated): `ent/virtualkey/virtualkey.go`, `ent/virtualkey/where.go`, `ent/virtualkey_create.go`, `ent/virtualkey_update.go`, `ent/migrate/schema.go`, `ent/runtime.go`, `ent/migrate/migrations/atlas.sum` (if present), plus any other files ent writes
- This step does not hand-edit anything; it commits whatever ent writes

**Interfaces:**
- Consumes: the `FieldUserID` declared in Task 1
- Produces: `ent.VirtualKey.Create().SetUserID(string)`, `ent/virtualkey.UserIDEQ(uuid.UUID)` predicate (etc.) — Task 4 references these

- [ ] **Step 1: Run ent generate**

Run:
```bash
GOTOOLCHAIN=go1.25.0 go generate ./ent/...
```
Expected: completes with no errors. `go generate` invokes ent's code-generation. If ent writes atlas migration artifacts (SQL + atlas.sum), that is expected — do not delete them. (The CI `migration-drift` job is OFF in dev, per CLAUDE.md §2; these files are local-state only.)

- [ ] **Step 2: Confirm new symbols exist**

Run:
```bash
grep -n "UserID\b\|user_id" ent/virtualkey/virtualkey.go ent/virtualkey/where.go | head -20
```
Expected: at least these symbols — `FieldUserID = "user_id"`, `ByUserID`, `UserID(v uuid.UUID) predicate.ProviderModel`, `UserIDEQ(v uuid.UUID)`. (The last predicate's return type is `predicate.VirtualKey`; the `providermodel` token may appear in grep noise from neighbouring ent files but the symbols above MUST show up under `ent/virtualkey/`.)

- [ ] **Step 3: Build to make sure regen compiles**

Run:
```bash
GOTOOLCHAIN=go1.25.0 go build ./ent/...
```
Expected: no output (clean build).

- [ ] **Step 4: Commit the regenerated ent code**

```bash
git add ent/
git commit -m "chore(virtualkey): regenerate ent for user_id column"
```

---

### Task 3: Add `userId` to GraphQL SDL

**Files:**
- Modify: `schema/virtualkey.graphql` — `VirtualKey` type and `IssueVirtualKeyInput` input

**Interfaces:**
- Consumes: nothing (Task 2's regen does not yet know about GraphQL fields — gqlgen regen happens in Task 4)
- Produces: the SDL that Task 4 will feed to `gqlgen generate`

- [ ] **Step 1: Add `userId` field to the `VirtualKey` type**

Open `schema/virtualkey.graphql`. In the `type VirtualKey { ... }` block, find the line that ends with `lastActiveAt: Time` (currently the last field of the type). Add the following lines immediately after it (so the new field is at the bottom of the type, matching the codebase's convention of appending new fields):

```graphql
  # 前端传入的 user_id,LiteLLM gateway 也用这个值作为 user_id。
  # 必填,IssueVirtualKeyInput 强制要求前端传值,后端不做默认。
  userId: String!
```

The full snippet to append verbatim:

```graphql
  # 前端传入的 user_id,LiteLLM gateway 也用这个值作为 user_id。
  # 必填,IssueVirtualKeyInput 强制要求前端传值,后端不做默认。
  userId: String!
```

- [ ] **Step 2: Add `userId` field to `IssueVirtualKeyInput`**

In the same file, inside `input IssueVirtualKeyInput { ... }`, find the `rotationInterval: String` line (currently the last field). Append the following right after it:

```graphql
  # Required. LiteLLM /key/generate 现在校验 user_id 非空。
  # 前端必须传一个非空字符串;后端透传到 ent.VirtualKey.user_id
  # 和 gateway.GenerateKeyRequest.UserID,不做默认值兜底。
  userId: String!
```

- [ ] **Step 3: Confirm the SDL is syntactically intact**

Run:
```bash
GOTOOLCHAIN=go1.25.0 go run github.com/99designs/gqlgen generate 2>&1 | tail -5
```
Expected: clean output, no errors. (This will regen the GraphQL Go code in Task 4's territory — that is fine; we commit it in Task 4.)

- [ ] **Step 4: Commit the SDL change**

```bash
git add schema/virtualkey.graphql
git commit -m "feat(virtualkey): add userId to VirtualKey type and IssueVirtualKeyInput"
```

---

### Task 4: Regenerate gqlgen code

**Files:**
- Modify (regenerated): `internal/graph/generated.go`, `internal/graph/model/models_gen.go`
- This step does not hand-edit; it commits whatever gqlgen writes

**Interfaces:**
- Consumes: the SDL from Task 3
- Produces:
  - `model.IssueVirtualKeyInput.UserID string`
  - `model.VirtualKey.UserID string`
  - These are referenced in Task 5

- [ ] **Step 1: Run gqlgen generate**

Run:
```bash
GOTOOLCHAIN=go1.25.0 go run github.com/99designs/gqlgen generate 2>&1 | tail -10
```
Expected: clean output.

- [ ] **Step 2: Confirm the new Go fields exist**

Run:
```bash
grep -n "UserID\b" internal/graph/model/models_gen.go | head -10
```
Expected: lines for both `IssueVirtualKeyInput.UserID` and `VirtualKey.UserID`. The shape (verify by `grep -n -A1 "type IssueVirtualKeyInput"` and `grep -n -A1 "type VirtualKey struct"` in the same file) is:

```go
type IssueVirtualKeyInput struct {
    ...
    UserID string `json:"userId"`  // gqlgen emits this
    ...
}
```

(The exact JSON tag name is `userId` — that matches the GraphQL field name and is what gqlgen always emits for `userId: String!`.)

- [ ] **Step 3: Verify the whole module still builds**

Run:
```bash
GOTOOLCHAIN=go1.25.0 go build ./...
```
Expected: no output. If gqlgen left any stale reference (e.g. resolver signature drift), it will surface here. (None expected — `IssueVirtualKey` resolver still has the same external signature; the new field is on the input struct.)

- [ ] **Step 4: Commit**

```bash
git add internal/graph/generated.go internal/graph/model/models_gen.go
git commit -m "chore(virtualkey): regenerate gqlgen for userId field"
```

---

### Task 5: Wire `userId` through `IssueVirtualKey` resolver

**Files:**
- Modify: `internal/graph/virtualkey.resolvers.go:34-191` — specifically inside `func (r *mutationResolver) IssueVirtualKey(...)`

**Interfaces:**
- Consumes:
  - `model.IssueVirtualKeyInput.UserID string` (from Task 4)
  - `ent.VirtualKey.Create().SetUserID(string)` (from Task 2)
  - `gateway.GenerateKeyRequest.UserID string` (existing, unchanged)
- Produces:
  - Validation: `input.UserID == ""` → 400 (defence-in-depth behind GraphQL `!`)
  - `gReq.UserID = input.UserID`
  - `create.SetUserID(input.UserID)`
  - Persisted `ent.virtual_key.user_id` column is populated; gateway `/key/generate` body carries `user_id`

- [ ] **Step 1: Add the empty-string validation**

In `internal/graph/virtualkey.resolvers.go`, at the top of `IssueVirtualKey`, immediately after the existing `input.Name == ""` and `input.ModelGateway == ""` checks (around line 35-40), add a third check that mirrors the same shape:

```go
	if input.UserID == "" {
		return nil, gqlerror.Errorf("userId is required")
	}
```

The three pre-conditions now read (in order):

```go
	if input.Name == "" {
		return nil, gqlerror.Errorf("name is required")
	}
	if input.ModelGateway == "" {
		return nil, gqlerror.Errorf("modelGateway is required")
	}
	if input.UserID == "" {
		return nil, gqlerror.Errorf("userId is required")
	}
```

- [ ] **Step 2: Plumb `UserID` into the gateway request**

In the same file, inside the `gReq := gateway.GenerateKeyRequest{ ... }` block (currently around line 117-131), add a new line at the top of the struct literal:

```go
	gReq := gateway.GenerateKeyRequest{
		UserID:              input.UserID,
		Models:              input.Models,
		MaxBudget:           input.MaxBudget,
		...
```

Match the existing field-aligned style. The full block becomes:

```go
	gReq := gateway.GenerateKeyRequest{
		UserID:              input.UserID,
		Models:              input.Models,
		MaxBudget:           input.MaxBudget,
		BudgetDuration:      vkDerefStr(input.BudgetDuration, ""),
		MaxParallelRequests: input.MaxParallelRequests,
		RPMLimit:            input.RpmLimit,
		TPMLimit:            input.TpmLimit,
		RPMLimitType:        vkDerefStr(input.RpmLimitType, ""),
		TPMLimitType:        vkDerefStr(input.TpmLimitType, ""),
		AllowedRoutes:       input.AllowedRoutes,
		Metadata:            input.Metadata,
		KeyType:             vkDerefStr(input.KeyType, ""),
		AutoRotate:          input.AutoRotate,
		RotationInterval:    vkDerefStr(input.RotationInterval, ""),
	}
```

(Replace the existing struct literal verbatim with the above; only the first line is new.)

- [ ] **Step 3: Plumb `UserID` into the ent create**

In the same file, inside the `create := r.Ent.VirtualKey.Create().SetLitellmKey(...)...` chain (currently around line 144-162), add a new chained call. Pick the spot right after `SetMaskedKey(redactKey(resp.Key))` (line ~147) for readability — the chain order does not matter functionally:

```go
	create := r.Ent.VirtualKey.Create().
		SetLitellmKey(resp.Key).
		SetLitellmToken(resp.Token).
		SetMaskedKey(redactKey(resp.Key)).
		SetUserID(input.UserID).
		SetName(input.Name).
		SetModelGatewayID(mgID).
		...
```

The exact insertion is a new line `SetUserID(input.UserID).` between `SetMaskedKey(redactKey(resp.Key)).` and `SetName(input.Name).`. All other lines in the chain stay as-is.

- [ ] **Step 4: Format check**

Run:
```bash
GOTOOLCHAIN=go1.25.0 gofmt -l internal/graph/virtualkey.resolvers.go
```
Expected: no output. If a filename prints, run `GOTOOLCHAIN=go1.25.0 gofmt -w internal/graph/virtualkey.resolvers.go` and re-check.

- [ ] **Step 5: Vet + build**

Run:
```bash
GOTOOLCHAIN=go1.25.0 go vet ./internal/graph/... && GOTOOLCHAIN=go1.25.0 go build ./...
```
Expected: no output from either command.

- [ ] **Step 6: Commit**

```bash
git add internal/graph/virtualkey.resolvers.go
git commit -m "feat(virtualkey): plumb UserID through IssueVirtualKey (validation + gateway + ent)"
```

---

### Task 6: Regenerate docs

**Files:**
- Modify (regenerated): `docs/schema.graphql`, `docs/api/virtual-keys.md`

**Interfaces:**
- Consumes: the SDL change from Task 3
- Produces: docs that match `schema/*.graphql`. The CI `docs-check` job runs `make docs` and fails if working tree drifted from generated — this task makes sure the committed docs match the new SDL.

- [ ] **Step 1: Run `make docs`**

Run:
```bash
make docs 2>&1 | tail -10
```
Expected: writes `docs/schema.graphql` (merged SDL) and re-runs `tools/apidocs/gen.py` to write the per-module `.md` files. Output should include "Wrote: docs/api/virtual-keys.md" (since `virtualkey.graphql` is one of the modules).

- [ ] **Step 2: Confirm `userId` shows up in the generated docs**

Run:
```bash
grep -n "userId\b" docs/schema.graphql docs/api/virtual-keys.md
```
Expected: at least one match per file. The SDL should contain `userId: String!` in both the `IssueVirtualKeyInput` and `VirtualKey` blocks; the API doc should contain a row in the input / type table.

- [ ] **Step 3: Verify `make docs-check` is now clean**

Run:
```bash
make docs-check 2>&1 | tail -10
```
Expected: NO `docs/ is stale vs schema/*.graphql` line, NO `Error 1`. The only output is the regeneration output and a clean exit. If the check still fails, it means a hand-edit on `docs/` is missing — re-run `make docs` and re-grep until the diff is empty.

- [ ] **Step 4: Commit**

```bash
git add docs/schema.graphql docs/api/virtual-keys.md
git commit -m "docs(virtualkey): regenerate for userId field"
```

---

### Task 7: Final CI-gate pass

**Files:**
- Modify: none (verification only)

**Interfaces:**
- Consumes: the full chain from Tasks 1-6
- Produces: confidence that the dev-stage CI gate (gofmt + vet + build + docs-check) will pass on the PR

- [ ] **Step 1: gofmt clean across the whole tree**

Run:
```bash
GOTOOLCHAIN=go1.25.0 gofmt -l .
```
Expected: no output. If any filename is printed, run `GOTOOLCHAIN=go1.25.0 gofmt -w <filename>` for each, then re-run.

- [ ] **Step 2: vet clean across the whole tree**

Run:
```bash
GOTOOLCHAIN=go1.25.0 go vet ./...
```
Expected: no output.

- [ ] **Step 3: build clean across the whole tree**

Run:
```bash
GOTOOLCHAIN=go1.25.0 go build ./...
```
Expected: no output.

- [ ] **Step 4: docs-check clean**

Run:
```bash
make docs-check 2>&1 | tail -5
```
Expected: no `docs/ is stale` line, exit code 0.

- [ ] **Step 5: Confirm the working tree contains exactly the expected changes**

Run:
```bash
git status --short
git log --oneline -8
```
Expected: clean working tree (no uncommitted changes — Tasks 1-6 each committed their slice). The 8-commit log should show the 6 commits from this plan (Tasks 1, 2, 3, 4, 5, 6 — Task 7 is verification-only) sitting on top of the prior HEAD.

- [ ] **Step 6: Push the branch**

Run:
```bash
git push origin 修改virutalkey
```
Expected: pushes the 6 new commits. (If push is denied by the harness's auto-mode classifier, the operator runs `git push origin 修改virutalkey` themselves — see plan's earlier note about CLAUDE.md §4 PR scope.)

---

## Self-Review

**Spec coverage:**
- GraphQL input field on `IssueVirtualKeyInput` → Task 3
- GraphQL output field on `VirtualKey` → Task 3
- `String!` non-null type → Task 3
- ent column with `NotEmpty` → Task 1
- No default value (front-end responsible) → Task 5 (no `SetUserID("admin")` anywhere; just `input.UserID` straight pass-through)
- Resolver validator → Task 5 step 1
- `gateway.GenerateKeyRequest.UserID` populated → Task 5 step 2
- ent `SetUserID` on create → Task 5 step 3
- Regenerate ent + gqlgen code → Tasks 2, 4
- `make docs` keeps `docs-check` green → Task 6
- Out-of-scope items (UUID FK, prod backfill, tests, migration, defaults) — explicitly excluded, not missing tasks

**Placeholder scan:** no `TBD` / `TODO` / "add appropriate error handling" / "similar to Task N" — every step shows the actual code or command.

**Type consistency:**
- `ent.VirtualKey.Create().SetUserID(string)` (Task 5 step 3) matches what Task 2 regen produces (ent always emits `Set<Field>(string)` for a `String` field).
- `model.IssueVirtualKeyInput.UserID string` (Task 5 step 1) matches what Task 4 regen produces for a `String!` field — gqlgen emits a non-pointer Go string for required GraphQL fields, so the `input.UserID == ""` check is the right idiom (not `input.UserID == nil`).
- `gateway.GenerateKeyRequest.UserID string` (Task 5 step 2) matches the existing struct (see `internal/gateway/client.go:169`); the field is a non-pointer string already today.

Plan is internally consistent.