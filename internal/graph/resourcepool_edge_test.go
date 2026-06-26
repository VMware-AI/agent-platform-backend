package graph

import (
	"context"
	"net"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// rpeMkPool registers a pool via the resolver and returns it. Suffixed name to
// avoid collisions with mkPool in resourcepool_module_test.go.
func rpeMkPool(t *testing.T, mr *mutationResolver, ctx context.Context, name, endpoint string) *model.ResourcePool {
	t.Helper()
	created, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{Name: name, Endpoint: endpoint})
	if err != nil {
		t.Fatalf("CreateResourcePool(%s): %v", name, err)
	}
	return created.Pool
}

const rpeNilUUID = "00000000-0000-0000-0000-000000000000"

// resourcePools on an empty store returns an empty (non-nil) connection with a
// well-formed pageInfo and zero counts — not nil and not an error.
func TestResourcePools_Empty_Edge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	qr := &queryResolver{r}

	conn, err := qr.ResourcePools(ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("ResourcePools(empty): %v", err)
	}
	if conn == nil {
		t.Fatal("connection should never be nil")
	}
	if conn.TotalCount != 0 {
		t.Fatalf("empty store totalCount = %d, want 0", conn.TotalCount)
	}
	if conn.Nodes == nil {
		t.Fatal("nodes should be an empty slice, not nil")
	}
	if len(conn.Nodes) != 0 {
		t.Fatalf("empty store len(nodes) = %d, want 0", len(conn.Nodes))
	}
	// default page params apply even on an empty store.
	if conn.PageInfo == nil {
		t.Fatal("pageInfo should be set")
	}
	if conn.PageInfo.Page != 1 || conn.PageInfo.PageSize != 20 {
		t.Fatalf("default pageInfo = page %d size %d, want 1/20", conn.PageInfo.Page, conn.PageInfo.PageSize)
	}
	// (total + pageSize - 1) / pageSize == 0 when there are no rows.
	if conn.PageInfo.TotalPages != 0 {
		t.Fatalf("empty store totalPages = %d, want 0", conn.PageInfo.TotalPages)
	}
}

// resourcePools with a filter that matches nothing returns an empty connection,
// not the full list — proves the keyword filter is actually applied.
func TestResourcePools_FilterNoMatch_Edge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	rpeMkPool(t, mr, ctx, "prod-east", "https://vc-east.internal")
	rpeMkPool(t, mr, ctx, "prod-west", "https://vc-west.internal")

	noMatch, err := qr.ResourcePools(ctx, &model.ResourcePoolFilter{NameKeyword: ptr("nonexistent-xyz")}, nil, nil)
	if err != nil {
		t.Fatalf("ResourcePools(no-match): %v", err)
	}
	if noMatch.TotalCount != 0 || len(noMatch.Nodes) != 0 {
		t.Fatalf("no-match filter returned total=%d nodes=%d, want 0/0", noMatch.TotalCount, len(noMatch.Nodes))
	}
	// sanity: the same store with no filter still has both pools (filter was the
	// only thing suppressing them).
	all, err := qr.ResourcePools(ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("ResourcePools(all): %v", err)
	}
	if all.TotalCount != 2 {
		t.Fatalf("unfiltered totalCount = %d, want 2", all.TotalCount)
	}
}

// resourcePool(id): a syntactically valid but absent id returns (nil, nil) — no
// existence oracle, no panic, no error.
func TestResourcePool_NotFound_Edge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	qr := &queryResolver{r}

	got, err := qr.ResourcePool(ctx, rpeNilUUID)
	if err != nil {
		t.Fatalf("ResourcePool(absent): unexpected error %v", err)
	}
	if got != nil {
		t.Fatalf("ResourcePool(absent) = %+v, want nil", got)
	}
}

// resourcePool(id): a malformed (non-UUID) id is rejected with an error and a nil
// pool — it must not panic.
func TestResourcePool_BadID_Edge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	qr := &queryResolver{r}

	got, err := qr.ResourcePool(ctx, "not-a-uuid")
	if err == nil {
		t.Fatal("malformed id should return an error")
	}
	if got != nil {
		t.Fatalf("malformed id should return a nil pool, got %+v", got)
	}
}

// testResourcePoolConnection on an unreachable / malformed endpoint must report
// ok=false with a non-empty message and NOT return a Go error. It must not create
// any pool row (a pre-save probe is side-effect free on the pool table).
func TestTestResourcePoolConnection_Unreachable_Edge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	// Deterministic, network-independent failures: the probe must soft-fail (ok=false,
	// no Go error) for an empty endpoint and for a malformed URL. (A non-routable
	// host's dial behavior varies by sandbox, so it is intentionally not asserted
	// here to keep the test hermetic.)
	cases := []struct {
		name     string
		endpoint string
	}{
		{"empty", ""},
		{"malformed-url", "://::::"},
		{"no-host", "https://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := mr.TestResourcePoolConnection(ctx, model.TestResourcePoolConnectionInput{
				Name: "probe-" + tc.name, Endpoint: tc.endpoint, ContentLibraryName: "lib",
			})
			if err != nil {
				t.Fatalf("probe returned a Go error (should be a soft failure): %v", err)
			}
			if res == nil {
				t.Fatal("probe result should never be nil")
			}
			if res.Ok {
				t.Fatalf("endpoint %q should report ok=false", tc.endpoint)
			}
			if res.Message == "" {
				t.Fatal("a failed probe must carry a human-readable message")
			}
		})
	}

	// the probe is side-effect-free: no pool row was created by any of the calls.
	conn, err := qr.ResourcePools(ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("ResourcePools after probes: %v", err)
	}
	if conn.TotalCount != 0 {
		t.Fatalf("probe must not create pool rows, got %d", conn.TotalCount)
	}
}

// testResourcePoolConnection on a reachable endpoint reports ok=true and returns a
// best-effort detail block: vSphereVersion is "" and itemCount is 0 because the
// credential-less probe cannot inventory the content library.
func TestTestResourcePoolConnection_ReachableDetail_Edge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	// A loopback listener gives us a guaranteed-reachable TCP endpoint without
	// needing the vcenter simulator.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	endpoint := "https://" + ln.Addr().String()

	res, err := mr.TestResourcePoolConnection(ctx, model.TestResourcePoolConnectionInput{
		Name: "reachable", Endpoint: endpoint, ContentLibraryName: "lib",
	})
	if err != nil {
		t.Fatalf("probe(reachable): %v", err)
	}
	if !res.Ok {
		t.Fatalf("reachable endpoint should report ok=true: %s", res.Message)
	}
	if res.Message == "" {
		t.Fatal("ok probe should still carry a message")
	}
	if res.Detail == nil {
		t.Fatal("ok probe should return a (best-effort) detail block")
	}
	// credential-less probe → no authenticated version, no library inventory.
	if res.Detail.VSphereVersion != "" {
		t.Fatalf("credential-less probe should not derive a version, got %q", res.Detail.VSphereVersion)
	}
	if res.Detail.ItemCount != 0 {
		t.Fatalf("credential-less probe itemCount = %d, want 0", res.Detail.ItemCount)
	}
}

// syncResourcePool with a malformed id is rejected up front (uuid.Parse) — error,
// no panic.
func TestSyncResourcePool_BadID_Edge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	payload, err := mr.SyncResourcePool(ctx, "not-a-uuid")
	if err == nil {
		t.Fatal("malformed id should return an error")
	}
	if payload != nil {
		t.Fatalf("malformed id should return a nil payload, got %+v", payload)
	}
}

// syncResourcePool against an absent (valid-shape) id returns an error without
// panicking — there is no pool to sync.
func TestSyncResourcePool_NotFound_Edge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	payload, err := mr.SyncResourcePool(ctx, rpeNilUUID)
	if err == nil {
		t.Fatal("syncing an absent pool should return an error")
	}
	if payload != nil {
		t.Fatalf("absent pool should return a nil payload, got %+v", payload)
	}
}

// syncResourcePool when the connector is unconfigured fails the connect step and
// stamps the pool's status to error — which the model projects to FAILED sync
// state. Verifies the error-path side effect, not just the returned error.
func TestSyncResourcePool_ConnectFailureStampsError_Edge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	// no Secrets / VCenterConnect configured → connectPool fails.
	pool := rpeMkPool(t, mr, ctx, "unconfigured", "https://vc.unconfigured")

	// fresh pool projects to NEVER before any sync attempt.
	if pool.SyncStatus != model.ResourcePoolSyncStateNever {
		t.Fatalf("pre-sync syncStatus = %v, want NEVER", pool.SyncStatus)
	}

	payload, err := mr.SyncResourcePool(ctx, pool.ID)
	if err == nil {
		t.Fatal("sync with no connector configured should fail")
	}
	if payload != nil {
		t.Fatalf("failed sync should return a nil payload, got %+v", payload)
	}

	// the connect failure persisted status=error → re-reading projects to FAILED.
	got, err := qr.ResourcePool(ctx, pool.ID)
	if err != nil {
		t.Fatalf("re-read pool: %v", err)
	}
	if got == nil {
		t.Fatal("pool should still exist after a failed sync")
	}
	if got.SyncStatus != model.ResourcePoolSyncStateFailed {
		t.Fatalf("after failed sync syncStatus = %v, want FAILED", got.SyncStatus)
	}
	if got.ConnectionStatus != model.PoolConnectionStatusDisconnected {
		t.Fatalf("errored pool connectionStatus = %v, want DISCONNECTED", got.ConnectionStatus)
	}
}

// resourcePools pagination: a page size of 2 over 5 pools yields the right slice
// sizes, total count, and computed totalPages — and an out-of-range page returns
// an empty slice while still reporting the true total.
func TestResourcePools_Pagination_Edge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	names := []string{"p-a", "p-b", "p-c", "p-d", "p-e"}
	for _, n := range names {
		rpeMkPool(t, mr, ctx, n, "https://vc-"+n)
	}

	sortAsc := &model.ResourcePoolSort{Field: model.ResourcePoolSortFieldName, Direction: model.SortDirectionAsc}

	// page 1, size 2 → first two by name asc.
	p1, err := qr.ResourcePools(ctx, nil, &model.Pagination{Page: 1, PageSize: 2}, sortAsc)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if p1.TotalCount != 5 {
		t.Fatalf("page1 totalCount = %d, want 5", p1.TotalCount)
	}
	if len(p1.Nodes) != 2 {
		t.Fatalf("page1 len(nodes) = %d, want 2", len(p1.Nodes))
	}
	if p1.Nodes[0].Name != "p-a" || p1.Nodes[1].Name != "p-b" {
		t.Fatalf("page1 nodes = %s,%s, want p-a,p-b", p1.Nodes[0].Name, p1.Nodes[1].Name)
	}
	// ceil(5/2) == 3.
	if p1.PageInfo.TotalPages != 3 {
		t.Fatalf("page1 totalPages = %d, want 3", p1.PageInfo.TotalPages)
	}
	if p1.PageInfo.Page != 1 || p1.PageInfo.PageSize != 2 {
		t.Fatalf("page1 pageInfo = %d/%d, want 1/2", p1.PageInfo.Page, p1.PageInfo.PageSize)
	}

	// page 3, size 2 → the trailing single element.
	p3, err := qr.ResourcePools(ctx, nil, &model.Pagination{Page: 3, PageSize: 2}, sortAsc)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(p3.Nodes) != 1 || p3.Nodes[0].Name != "p-e" {
		t.Fatalf("page3 nodes = %+v, want [p-e]", poolNamesEdge(p3))
	}
	if p3.TotalCount != 5 {
		t.Fatalf("page3 totalCount = %d, want 5", p3.TotalCount)
	}

	// page 1 and page 2 must not overlap (offset is honored).
	p2, err := qr.ResourcePools(ctx, nil, &model.Pagination{Page: 2, PageSize: 2}, sortAsc)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(p2.Nodes) != 2 || p2.Nodes[0].Name != "p-c" || p2.Nodes[1].Name != "p-d" {
		t.Fatalf("page2 nodes = %+v, want [p-c p-d]", poolNamesEdge(p2))
	}

	// out-of-range page → empty slice, true total still reported.
	pOut, err := qr.ResourcePools(ctx, nil, &model.Pagination{Page: 99, PageSize: 2}, sortAsc)
	if err != nil {
		t.Fatalf("page99: %v", err)
	}
	if len(pOut.Nodes) != 0 {
		t.Fatalf("out-of-range page should be empty, got %d nodes", len(pOut.Nodes))
	}
	if pOut.TotalCount != 5 {
		t.Fatalf("out-of-range totalCount = %d, want 5", pOut.TotalCount)
	}
}

func poolNamesEdge(c *model.ResourcePoolConnection) []string {
	out := make([]string, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		out = append(out, n.Name)
	}
	return out
}
