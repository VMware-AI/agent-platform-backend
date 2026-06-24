# Client contract fixtures

Each `*.graphql` file here is a **point-in-time snapshot of one
agent-platform-console GraphQL operation** (with its fragments inlined). They are
the contract the backend promises the frontend.

`TestClientOperationsMatchSchema` (../../client_contract_test.go) validates every
file against the backend schema (`schema/*.graphql`). A backend change that breaks
the frontend — a renamed field, a dropped query, a changed arg/nullability — fails
that test in CI, before it reaches the running console.

## Refresh after the frontend changes its operations

From the backend repo root, with the console checked out alongside:

```sh
make client-fixtures            # console at ../agent-platform-console
# or:
node tools/genclientfixtures/main.mjs /path/to/agent-platform-console
```

Then run the test and commit the diff:

```sh
go test ./internal/graph/ -run TestClientOperationsMatchSchema
```

The generator reads `src/api/graphql/queries/*.ts`, resolves the `${FRAGMENT}`
interpolations, and writes one complete operation document per file. It is a
faithful snapshot — do not hand-edit these files.

## Limitation

These snapshots are only as current as the last `make client-fixtures` run. The
test validates **snapshot-vs-backend**, not **live-frontend-vs-backend** — if the
console changes an operation and nobody re-snapshots here, the test keeps passing
against the stale copy and the real drift ships undetected. Refresh whenever the
console's operations change (ideally wire `make client-fixtures` into a pre-merge
check so the snapshot can't silently rot).
