package graph

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// These tests lock in tenant read-isolation for the windowed / aggregated
// observability + metering paths (LLD-10 B-class). They mirror the direct-resolver
// style of cross_tenant_read_test.go: seed two tenants (A and B) each with their
// own users + request/usage rows, then assert a tenant-admin of A sees ONLY A's
// data — across the row list (requestLogs), the bucketed+summary aggregation
// (requestMetrics), and the metering rollup (meteringOverview). A misconfigured
// tenant-admin (empty / unparseable tenant id) must fail CLOSED to empty, never
// leak everything.
//
// RequestLog carries no tenant_id of its own; it is scoped via its user's (or
// agent's) tenant (tenantMemberIDs in helpers.go). So request logs are attached to
// tenant-scoped users seeded via ent. TokenUsage is tenant-stamped directly.

// seedTenantRequestLog inserts a request log owned by userID at a controlled time
// (RequestMetrics windows on created_at; the RecordRequestLog mutation uses now()).
func seedTenantRequestLog(t *testing.T, c *ent.Client, reqID string, userID uuid.UUID, at time.Time, status, latency, in, out int) {
	t.Helper()
	if _, err := c.RequestLog.Create().
		SetRequestID(reqID).
		SetUserID(userID).
		SetStatusCode(status).
		SetLatencyMs(latency).
		SetInputTokens(in).
		SetOutputTokens(out).
		SetCreatedAt(at).
		Save(context.Background()); err != nil {
		t.Fatalf("seed tenant request log %s: %v", reqID, err)
	}
}

// twoTenantFixture seeds tenants A and B, one user each, and per-tenant request
// logs (in a single hour window) + token-usage rows. It returns the tenant ids and
// the window bounds covering all seeded request logs.
type twoTenantFixture struct {
	tA, tB   uuid.UUID
	from, to time.Time
	uA, uB   uuid.UUID
}

func seedTwoTenants(t *testing.T, r *Resolver) twoTenantFixture {
	t.Helper()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()

	uA := r.Ent.User.Create().SetUsername("uA").SetEmail("ua@x.io").
		SetPasswordHash("h").SetTenantID(tA).SaveX(ctx)
	uB := r.Ent.User.Create().SetUsername("uB").SetEmail("ub@x.io").
		SetPasswordHash("h").SetTenantID(tB).SaveX(ctx)

	from := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)

	// Tenant A: 2 request logs (1 ok + 1 error) inside the window.
	seedTenantRequestLog(t, r.Ent, "A-ok", uA.ID, from.Add(5*time.Minute), 200, 100, 10, 20)
	seedTenantRequestLog(t, r.Ent, "A-err", uA.ID, from.Add(15*time.Minute), 500, 200, 30, 40)
	// Tenant B: 3 request logs (would inflate A's metrics if isolation leaked).
	seedTenantRequestLog(t, r.Ent, "B-1", uB.ID, from.Add(6*time.Minute), 200, 999, 7, 7)
	seedTenantRequestLog(t, r.Ent, "B-2", uB.ID, from.Add(7*time.Minute), 200, 999, 7, 7)
	seedTenantRequestLog(t, r.Ent, "B-3", uB.ID, from.Add(8*time.Minute), 500, 999, 7, 7)

	// TokenUsage rows (created_at defaults to now() → within meteringOverview's
	// rolling 7-day window). One for A, two for B.
	r.Ent.TokenUsage.Create().SetUserID(uA.ID).SetModel("m").SetTenantID(tA).
		SetInputTokens(11).SetOutputTokens(22).SaveX(ctx)
	r.Ent.TokenUsage.Create().SetUserID(uB.ID).SetModel("m").SetTenantID(tB).
		SetInputTokens(500).SetOutputTokens(500).SaveX(ctx)
	r.Ent.TokenUsage.Create().SetUserID(uB.ID).SetModel("m").SetTenantID(tB).
		SetInputTokens(500).SetOutputTokens(500).SaveX(ctx)

	return twoTenantFixture{tA: tA, tB: tB, from: from, to: to, uA: uA.ID, uB: uB.ID}
}

// TestCrossTenant_RequestLogs_ScopedToTenantA: a tenant-admin of A sees only A's
// two request logs, none of B's three.
func TestCrossTenant_RequestLogs_ScopedToTenantA(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	f := seedTwoTenants(t, r)
	qr := &queryResolver{r}

	logs, err := qr.RequestLogs(tenantAdminCtx(uuid.NewString(), f.tA.String()), nil, nil)
	if err != nil {
		t.Fatalf("RequestLogs: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("tenant-admin A must see exactly A's 2 logs, got %d", len(logs))
	}
	for _, l := range logs {
		if l.RequestID != "A-ok" && l.RequestID != "A-err" {
			t.Fatalf("tenant A leaked a non-A request log: %q", l.RequestID)
		}
	}
}

// TestCrossTenant_RequestMetrics_BucketsAndSummaryExcludeB asserts BOTH the
// per-bucket aggregation and the window summary count only tenant A's traffic
// (2 requests, 1 error), never B's much larger/noisier set.
func TestCrossTenant_RequestMetrics_BucketsAndSummaryExcludeB(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	f := seedTwoTenants(t, r)
	qr := &queryResolver{r}

	res, err := qr.RequestMetrics(
		tenantAdminCtx(uuid.NewString(), f.tA.String()),
		f.from, f.to, model.RequestMetricsBucketGranularityHour, nil)
	if err != nil {
		t.Fatalf("RequestMetrics: %v", err)
	}

	// One hour bucket; it must reflect ONLY tenant A (2 reqs, 1 error).
	if len(res.Buckets) != 1 {
		t.Fatalf("expected 1 hour bucket, got %d", len(res.Buckets))
	}
	b := res.Buckets[0]
	if b.RequestCount != 2 || b.ErrorCount != 1 {
		t.Fatalf("bucket leaked B's traffic: req=%d err=%d (want 2/1)", b.RequestCount, b.ErrorCount)
	}
	if b.InputTokensTotal != 40 || b.OutputTokensTotal != 60 {
		t.Fatalf("bucket tokens leaked: in=%d out=%d (want 40/60)", b.InputTokensTotal, b.OutputTokensTotal)
	}

	// Summary must also be tenant-A only.
	s := res.Summary
	if s.TotalRequests != 2 || s.TotalErrors != 1 {
		t.Fatalf("summary leaked B's traffic: req=%d err=%d (want 2/1)", s.TotalRequests, s.TotalErrors)
	}
	if s.TotalInputTokens != 40 || s.TotalOutputTokens != 60 {
		t.Fatalf("summary tokens leaked: in=%d out=%d (want 40/60)", s.TotalInputTokens, s.TotalOutputTokens)
	}
}

// TestCrossTenant_MeteringOverview_ScopedToTenantA asserts the metering rollup
// (totals + per-model breakdown) reflects only A's single usage row, not B's two.
func TestCrossTenant_MeteringOverview_ScopedToTenantA(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	f := seedTwoTenants(t, r)
	qr := &queryResolver{r}

	ov, err := qr.MeteringOverview(tenantAdminCtx(uuid.NewString(), f.tA.String()), nil, nil)
	if err != nil {
		t.Fatalf("MeteringOverview: %v", err)
	}
	if ov.TotalRequests != 1 {
		t.Fatalf("overview leaked B's usage: totalRequests=%d (want 1)", ov.TotalRequests)
	}
	if ov.TotalInputTokens != 11 || ov.TotalOutputTokens != 22 {
		t.Fatalf("overview tokens leaked: in=%d out=%d (want 11/22)", ov.TotalInputTokens, ov.TotalOutputTokens)
	}
}

// TestCrossTenant_FailClosed_DenyAll asserts the misconfigured-tenant-admin branch
// (empty / unparseable tenant id → denyAll) returns EMPTY across every scoped read,
// never the whole table. This is the critical fail-closed contract: a tenant-admin
// whose tenant cannot be resolved must see NOTHING, not everything.
func TestCrossTenant_FailClosed_DenyAll(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	f := seedTwoTenants(t, r)
	qr := &queryResolver{r}

	for _, badTenant := range []string{"", "not-a-uuid"} {
		ctx := tenantAdminCtx(uuid.NewString(), badTenant)
		label := "empty"
		if badTenant != "" {
			label = "malformed"
		}

		logs, err := qr.RequestLogs(ctx, nil, nil)
		if err != nil {
			t.Fatalf("[%s] RequestLogs: %v", label, err)
		}
		if len(logs) != 0 {
			t.Fatalf("[%s] denyAll RequestLogs must be empty, got %d", label, len(logs))
		}

		res, err := qr.RequestMetrics(ctx, f.from, f.to, model.RequestMetricsBucketGranularityHour, nil)
		if err != nil {
			t.Fatalf("[%s] RequestMetrics: %v", label, err)
		}
		// Gap-fill still emits one zeroed hour bucket; the point is zero traffic.
		if res.Summary.TotalRequests != 0 || res.Summary.TotalErrors != 0 {
			t.Fatalf("[%s] denyAll RequestMetrics summary must be zero, got req=%d err=%d",
				label, res.Summary.TotalRequests, res.Summary.TotalErrors)
		}
		for _, b := range res.Buckets {
			if b.RequestCount != 0 {
				t.Fatalf("[%s] denyAll RequestMetrics bucket leaked traffic: %+v", label, b)
			}
		}

		ov, err := qr.MeteringOverview(ctx, nil, nil)
		if err != nil {
			t.Fatalf("[%s] MeteringOverview: %v", label, err)
		}
		if ov.TotalRequests != 0 || ov.TotalInputTokens != 0 || ov.TotalOutputTokens != 0 {
			t.Fatalf("[%s] denyAll MeteringOverview must be zero, got req=%d in=%d out=%d",
				label, ov.TotalRequests, ov.TotalInputTokens, ov.TotalOutputTokens)
		}
	}
}
