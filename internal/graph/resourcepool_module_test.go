package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

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
	// connectionStatus: all freshly-created pools are DISCONNECTED (never synced)
	disc := mustPoolConn(t, qr, ctx, &model.ResourcePoolFilter{ConnectionStatus: poolStatusPtr(model.PoolConnectionStatusDisconnected)}, nil)
	if disc.TotalCount != 3 {
		t.Fatalf("disconnected: %d, want 3", disc.TotalCount)
	}
	conn := mustPoolConn(t, qr, ctx, &model.ResourcePoolFilter{ConnectionStatus: poolStatusPtr(model.PoolConnectionStatusConnected)}, nil)
	if conn.TotalCount != 0 {
		t.Fatalf("connected: %d, want 0", conn.TotalCount)
	}

	// sort by NAME asc + connection shape + every node DISCONNECTED
	asc := mustPoolConn(t, qr, ctx, nil, &model.ResourcePoolSort{Field: model.ResourcePoolSortFieldName, Direction: model.SortDirectionAsc})
	if asc.TotalCount != 3 || asc.Nodes[0].Name != "alpha-dc" || asc.Nodes[2].Name != "gamma-prod" {
		t.Fatalf("sort: total=%d %s..%s", asc.TotalCount, asc.Nodes[0].Name, asc.Nodes[2].Name)
	}
	if asc.PageInfo == nil || asc.PageInfo.TotalPages != 1 {
		t.Fatalf("pageInfo wrong: %+v", asc.PageInfo)
	}
	if asc.Nodes[0].ConnectionStatus != model.PoolConnectionStatusDisconnected {
		t.Fatalf("fresh pool status = %v, want DISCONNECTED", asc.Nodes[0].ConnectionStatus)
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

func poolStatusPtr(s model.PoolConnectionStatus) *model.PoolConnectionStatus { return &s }
