# ModelGateway: make `loadBalancingStrategy` read-only, populated by live probe

Date: 2026-06-29
Branch: `feat/modelgateway-strategy-readonly-live-probe`
Status: design — pending approval

## Context

Today, on the model-gateway page (the `modelgateway.graphql` façade), `loadBalancingStrategy` is hard-coded to `ROUND_ROBIN` in the projection helper and is required on the input — but the resolver silently drops it. The user-facing experience is misleading:

- The console form *asks* the user to pick a strategy, but the choice is ignored.
- The row *displays* `ROUND_ROBIN` regardless of what the underlying LiteLLM instance is actually configured to do.

The legacy routing page (`gateway-routing.graphql`) has a real 5-value enum (`simple_shuffle`, `latency`, `usage_v2`, `least_busy`, `cost`) and stores a value in the `load_balance_strategy` ent column, but **that value is never pushed to LiteLLM** anywhere in the codebase — the column is purely a local label.

This change scopes the fix to the model-gateway page only. The intended outcome:

- `CreateModelGateway` / `UpdateModelGateway` inputs no longer accept `loadBalancingStrategy`.
- The `ModelGateway.loadBalancingStrategy` type field becomes nullable, populated only by a live probe against LiteLLM.
- The user clicks "Test Connection" on the create form; the backend then reports the gateway's current routing strategy as observed by probing `<endpoint>/config/router`.
- The legacy routing page and its 5-value enum are untouched.

## Decisions locked during brainstorming

1. **Source of the strategy value:** live probe against LiteLLM at test time. No new server-side storage on the model-gateway path.
2. **Probe trigger:** always run on every test-connection click (including from the existing `testModelGatewayConnection` mutation and any future UI entry point on the create form).
3. **Endpoint:** a single known LiteLLM endpoint — `GET <endpoint>/config/router` with bearer auth. No multi-endpoint fallback.
4. **Schema shape:** nullable `loadBalancingStrategy: LoadBalancingStrategy` on the `ModelGateway` type and on `ModelGatewayTestResult`; widen the existing single-value enum to five values.
5. **Legacy routing page:** out of scope.

## Architecture

### New `ModelManager.GetRoutingStrategy`

Add a method to the `gateway.ModelManager` interface, alongside the existing `TestConnection`/`NewModel`/`DeleteModel`/`UpsertComplexityRouter` methods.

```go
// internal/gateway/models.go
type ModelManager interface {
    TestConnection(ctx context.Context) error
    GetRoutingStrategy(ctx context.Context) (RoutingStrategy, error)
    NewModel(ctx context.Context, params NewModelParams) error
    DeleteModel(ctx context.Context, name string) error
    UpsertComplexityRouter(ctx context.Context, cfg ComplexityRouterConfig) error
}

type RoutingStrategy string

const (
    RoutingStrategyRoundRobin    RoutingStrategy = "simple_shuffle"
    RoutingStrategyLatencyBased  RoutingStrategy = "latency"
    RoutingStrategyUsageBasedV2  RoutingStrategy = "usage_v2"
    RoutingStrategyLeastBusy     RoutingStrategy = "least_busy"
    RoutingStrategyCostBased     RoutingStrategy = "cost"
)
```

The `HTTPClient` implementation calls `GET <baseURL>/config/router` (15s timeout, same retry/backoff as other reads), parses the JSON response's `routing_strategy` field as a string, and maps it to one of the five `RoutingStrategy` constants. Unknown values return a sentinel `ErrUnknownRoutingStrategy` so the caller can decide to log-and-skip rather than fail the test.

The existing `models_test.go` test fake gets a stub `GetRoutingStrategy` returning `(RoutingStrategyRoundRobin, nil)`. A new test exercises the real HTTP path with an `httptest.Server`.

### Schema changes — `schema/modelgateway.graphql`

```graphql
enum LoadBalancingStrategy {
    ROUND_ROBIN
    LATENCY_BASED
    USAGE_BASED_V2
    LEAST_BUSY
    COST_BASED
}

type ModelGateway {
    id: ID!
    name: String!
    provider: ModelGatewayProvider!
    endpoint: String!
    status: ModelGatewayStatus!
    backendModelCount: Int!
    loadBalancingStrategy: LoadBalancingStrategy          # was: LoadBalancingStrategy!
    adminUrl: String
    lastSyncAt: Time
    lastSyncStatus: ModelGatewaySyncState
    lastSyncMessage: String
    latencyMs: Int
    createdAt: Time!
    updatedAt: Time!
}

type ModelGatewayTestResult {
    success: Boolean!
    status: ModelGatewayStatus!
    latencyMs: Int!
    message: String!
    testedAt: Time!
    gateway: ModelGateway!
    loadBalancingStrategy: LoadBalancingStrategy          # NEW
}

input ModelGatewayInput {                                # loadBalancingStrategy dropped
    name: String!
    provider: ModelGatewayProvider!
    endpoint: String!
    adminUrl: String
    masterKey: String!
}
```

The bundled published schema at `docs/schema.graphql` is regenerated from the source schemas so client codegen sees the same surface. The four `testdata/client_operations/*.graphql` fixtures under the model-gateway directory are updated:

- `CreateModelGateway.graphql`, `UpdateModelGateway.graphql` — drop `loadBalancingStrategy` from the `$input` literal.
- `ModelGateways.graphql`, `TestModelGatewayConnection.graphql` — add `loadBalancingStrategy` to the `ModelGatewayFields` fragment selection set (the field is now nullable, but selecting it is still legal).

### Resolver flow — `TestModelGatewayConnection`

```text
1. loadGatewayByID(ctx, id)                           // existing
2. buildGatewayModels(ctx, g) → ModelManager          // existing
3. t0 = time.Now()
4. if err := mgr.TestConnection(ctx); err != nil      // existing: GET /models
       return success=false, status=ERROR, latencyMs, message="connection failed"
5. strategy, stratErr := mgr.GetRoutingStrategy(ctx)  // NEW: GET /config/router
6. if stratErr != nil
       log.Warn(...)
       strategy = nil                                 // don't fail the test
7. applyGatewayTestResult(g, status=connected)        // existing
8. audit(model_gateway.test, ok=true)                 // existing
9. return ModelGatewayTestResult{
       success: true,
       status:  CONNECTED,
       latencyMs,
       message: "connection ok",
       testedAt,
       gateway:           toModelGateway(g, strategy),
       loadBalancingStrategy: strategy,
   }
```

Step 6 is the key correctness decision: a routing-strategy probe failure must not downgrade a successful connectivity test to a failure. The user can save a gateway whose strategy probe fails; the field will simply be `null` until the next test succeeds.

### Projection helper signature

`toModelGateway` currently hard-codes `LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin` at `internal/graph/gateway_facade.go:92`. After the change:

```go
func toModelGateway(g *ent.GatewayConnection, strategy *model.LoadBalancingStrategy) *model.ModelGateway
```

Call sites:
- Query paths (`ModelGateways`, `ModelGatewaySyncSummary`, the projected `gateway` inside non-test mutations) — pass `nil`.
- `TestModelGatewayConnection` — pass the probed value (also nil when the probe fails).

The legacy `toGatewayConnection` projection used by the routing page is unchanged.

### Input drops — `CreateModelGateway` / `UpdateModelGateway`

The resolvers already ignore `input.LoadBalancingStrategy` (it was a no-op). After the input field is deleted from the schema, the Go resolver code loses the corresponding `*string` dereference and the regenerated model package no longer has the field on `ModelGatewayInput`. No behavioural change beyond removing the dead code.

## Critical files

| File | Change |
|---|---|
| `schema/modelgateway.graphql` | widen enum; drop input field; type field nullable; add test-result field |
| `docs/schema.graphql` | regenerated to mirror above |
| `internal/gateway/models.go` | add `RoutingStrategy` type, `GetRoutingStrategy` interface method, `ErrUnknownRoutingStrategy` |
| `internal/gateway/client.go` | implement `HTTPClient.GetRoutingStrategy` with retry/backoff |
| `internal/gateway/models_test.go` | update fake; add httptest-backed round-trip test |
| `internal/graph/gateway_facade.go` | change `toModelGateway` signature; thread nullable strategy |
| `internal/graph/modelgateway.resolvers.go` | `TestModelGatewayConnection` calls probe and attaches result |
| `internal/graph/modelgateway_test.go` | drop strategy from create/update inputs; assert nullable on list and test-result |
| `internal/graph/testdata/client_operations/CreateModelGateway.graphql` | drop `loadBalancingStrategy` from `$input` |
| `internal/graph/testdata/client_operations/UpdateModelGateway.graphql` | drop `loadBalancingStrategy` from `$input` |
| `internal/graph/testdata/client_operations/ModelGateways.graphql` | add `loadBalancingStrategy` to fragment |
| `internal/graph/testdata/client_operations/TestModelGatewayConnection.graphql` | add `loadBalancingStrategy` to fragment and to `ModelGatewayTestResult` selection |
| `internal/graph/model/models_gen.go` | regenerated — no hand edits |

## Reuse

- Existing HTTP-client retry/backoff: `gatewayHTTPTimeout`, `gatewayRetryBackoff`, `maxAttempts` in `internal/gateway/client.go:19-21, 240-256`.
- Existing audit log entry: `model_gateway.test` at `internal/graph/modelgateway.resolvers.go:139` — unchanged.
- Existing persist helper: `applyGatewayTestResult` at `internal/graph/gateway_facade.go:150-156` — unchanged.
- Existing per-row client builder: `buildGatewayModels` at `internal/graph/gateway_facade.go:137-143` — unchanged.

## Error handling

| Condition | Behaviour |
|---|---|
| `GET /models` fails | Test fails (existing). `loadBalancingStrategy` is null. |
| `GET /models` succeeds, `GET /config/router` returns 404 | Litellm version mismatch. Log warning. Test still succeeds. Field null. |
| `GET /config/router` returns 2xx with unknown `routing_strategy` | `ErrUnknownRoutingStrategy` returned. Log warning. Test still succeeds. Field null. |
| `GET /config/router` returns 5xx after retries | Same as 404: log, field null. |
| Transport error (timeout, EOF) after retries | Same as 404: log, field null. |

The key invariant: **a connectivity test only fails on `GET /models` failure**. The strategy probe is best-effort.

## Testing

Unit tests:
- `TestHTTPClient_GetRoutingStrategy` — `httptest.Server` returning canned `/config/router` JSON for each of the five values; asserts mapping.
- `TestHTTPClient_GetRoutingStrategy_Unknown` — server returns `"routing_strategy": "vendor_custom"`; asserts `ErrUnknownRoutingStrategy`.
- `TestHTTPClient_GetRoutingStrategy_5xx` — server returns 500; asserts error after retries.
- Updated fake `GetRoutingStrategy` in `models_test.go` returns `(ROUND_ROBIN, nil)` to keep existing graph tests happy.

Graph tests (`internal/graph/modelgateway_test.go`):
- `TestCreateModelGateway` / `TestUpdateModelGateway`: the `$input` literal no longer contains `loadBalancingStrategy`.
- `TestModelGateways`: assert `loadBalancingStrategy` is `nil` (query path never probes).
- `TestTestModelGatewayConnection_StrategyProbed`: stub `GetRoutingStrategy` to return `LATENCY_BASED`; assert the result includes `loadBalancingStrategy = LATENCY_BASED` and the projected `gateway.loadBalancingStrategy` matches.
- `TestTestModelGatewayConnection_StrategyProbeFails`: stub `GetRoutingStrategy` to return an error; assert the test still reports `success=true` and the field is null.

End-to-end verification:
1. `go generate ./...` (regenerates the GraphQL model package and `docs/schema.graphql`).
2. `go test ./...` — all packages green.
3. Manual: hit `createModelGateway` (input without `loadBalancingStrategy`) and `testModelGatewayConnection` against a real LiteLLM instance; observe `loadBalancingStrategy` reflecting the gateway's actual `routing_strategy` setting, or null when the version doesn't expose `/config/router`.

## Out of scope

- The legacy routing page (`schema/gateway-routing.graphql`, `internal/graph/gateway-routing.resolvers.go`, `load_balance_strategy` ent column) — left untouched.
- UI changes — the console repo is separate. The schema change here is sufficient for the console to drop the strategy field from the form and surface it as a read-only value after test.
- Pushing strategy values back to LiteLLM (no `POST /config/router`).
- Caching probed strategy on the ent row — explicitly rejected during brainstorming.