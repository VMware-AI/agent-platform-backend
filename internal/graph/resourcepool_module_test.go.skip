package graph

import (
	"context"
	"testing"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent/resourcepool"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// syncStatus is derived: never synced → NEVER; last sync ok → SYNCED; status=error
// → FAILED. lastSyncedAt mirrors the column.
func TestResourcePool_SyncStatusProjection(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()

	fresh := r.Ent.ResourcePool.Create().SetName("fresh").SetEndpoint("https://f").SaveX(ctx)
	if m := toModelResourcePool(fresh); m.SyncStatus != model.ResourcePoolSyncStateNever || m.LastSyncedAt != nil {
		t.Fatalf("fresh pool: syncStatus=%v lastSyncedAt=%v, want NEVER/nil", m.SyncStatus, m.LastSyncedAt)
	}

	now := time.Now()
	synced := r.Ent.ResourcePool.Create().SetName("ok").SetEndpoint("https://o").
		SetStatus(resourcepool.StatusConnected).SetLastSyncedAt(now).SaveX(ctx)
	if m := toModelResourcePool(synced); m.SyncStatus != model.ResourcePoolSyncStateSynced || m.LastSyncedAt == nil {
		t.Fatalf("synced pool: syncStatus=%v lastSyncedAt=%v, want SYNCED/set", m.SyncStatus, m.LastSyncedAt)
	}

	failed := r.Ent.ResourcePool.Create().SetName("bad").SetEndpoint("https://b").
		SetStatus(resourcepool.StatusError).SaveX(ctx)
	if m := toModelResourcePool(failed); m.SyncStatus != model.ResourcePoolSyncStateFailed {
		t.Fatalf("errored pool: syncStatus=%v, want FAILED", m.SyncStatus)
	}
}

// mkPool registers a pool via the resolver and returns it (重蒙皮 P3 helper).
func mkPool(t *testing.T, mr *mutationResolver, ctx context.Context, name, endpoint string) *model.ResourcePool {
	t.Helper()
	created, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{Name: name, Endpoint: endpoint})
	if err != nil {
		t.Fatalf("CreateResourcePool(%s): %v", name, err)
	}
	return created.Pool
}

// resourcePools is a filtered/sorted/paged connection (console 资源池 page).
func TestResourcePools_Connection(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	mkPool(t, mr, ctx, "alpha-dc", "https://vc-a.internal")
	mkPool(t, mr, ctx, "beta-dc", "https://vc-b.internal")
	mkPool(t, mr, ctx, "gamma-prod", "https://vc-a.external")

	// nameKeyword
	byName := mustPoolConn(t, qr, ctx, &model.ResourcePoolFilter{NameKeyword: ptr("dc")}, nil)
	if byName.TotalCount != 2 {
		t.Fatalf("nameKeyword dc: %d, want 2", byName.TotalCount)
	}
	// endpointKeyword
	byEp := mustPoolConn(t, qr, ctx, &model.ResourcePoolFilter{EndpointKeyword: ptr("vc-a")}, nil)
	if byEp.TotalCount != 2 {
		t.Fatalf("endpointKeyword vc-a: %d, want 2", byEp.TotalCount)
	}
	// syncStatus filter: all freshly-created pools are NEVER (never synced)
	never := mustPoolConn(t, qr, ctx, &model.ResourcePoolFilter{SyncStatus: statusStatePtr(model.ResourcePoolSyncStateNever)}, nil)
	if never.TotalCount != 3 {
		t.Fatalf("never-synced: %d, want 3", never.TotalCount)
	}
	synced := mustPoolConn(t, qr, ctx, &model.ResourcePoolFilter{SyncStatus: statusStatePtr(model.ResourcePoolSyncStateSynced)}, nil)
	if synced.TotalCount != 0 {
		t.Fatalf("synced: %d, want 0", synced.TotalCount)
	}

	// sort by NAME asc + every node is NEVER (freshly created)
	asc := mustPoolConn(t, qr, ctx, nil, &model.ResourcePoolSort{Field: model.ResourcePoolSortFieldName, Direction: model.SortDirectionAsc})
	if asc.TotalCount != 3 || asc.Nodes[0].Name != "alpha-dc" || asc.Nodes[2].Name != "gamma-prod" {
		t.Fatalf("sort: total=%d %s..%s", asc.TotalCount, asc.Nodes[0].Name, asc.Nodes[2].Name)
	}
	if asc.PageInfo == nil || asc.PageInfo.TotalPages != 1 {
		t.Fatalf("pageInfo wrong: %+v", asc.PageInfo)
	}
	if asc.Nodes[0].SyncStatus != model.ResourcePoolSyncStateNever {
		t.Fatalf("fresh pool syncStatus = %v, want NEVER", asc.Nodes[0].SyncStatus)
	}
	// sort by NAME desc (covers the desc branch)
	desc := mustPoolConn(t, qr, ctx, nil, &model.ResourcePoolSort{Field: model.ResourcePoolSortFieldName, Direction: model.SortDirectionDesc})
	if desc.Nodes[0].Name != "gamma-prod" || desc.Nodes[2].Name != "alpha-dc" {
		t.Fatalf("sort NAME desc: %s..%s", desc.Nodes[0].Name, desc.Nodes[2].Name)
	}
	// sort by a non-name field (endpoint asc)
	epSort := mustPoolConn(t, qr, ctx, nil, &model.ResourcePoolSort{Field: model.ResourcePoolSortFieldEndpoint, Direction: model.SortDirectionAsc})
	if epSort.Nodes[0].Endpoint > epSort.Nodes[2].Endpoint {
		t.Fatalf("sort ENDPOINT asc not ordered: %s..%s", epSort.Nodes[0].Endpoint, epSort.Nodes[2].Endpoint)
	}
}

// resourcePool(id) returns one pool, or nil for a missing id (no oracle).
func TestResourcePool_Single(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	p := mkPool(t, mr, ctx, "solo", "https://vc.solo")
	got, err := qr.ResourcePool(ctx, p.ID)
	if err != nil || got == nil || got.ID != p.ID {
		t.Fatalf("ResourcePool(%s): %+v / %v", p.ID, got, err)
	}
	missing, err := qr.ResourcePool(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil || missing != nil {
		t.Fatalf("missing pool should be nil, no error: %+v / %v", missing, err)
	}
}

func mustPoolConn(t *testing.T, qr *queryResolver, ctx context.Context, f *model.ResourcePoolFilter, s *model.ResourcePoolSort) *model.ResourcePoolConnection {
	t.Helper()
	c, err := qr.ResourcePools(ctx, f, nil, s)
	if err != nil {
		t.Fatalf("ResourcePools: %v", err)
	}
	return c
}

func statusStatePtr(s model.ResourcePoolSyncState) *model.ResourcePoolSyncState { return &s }
