package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

func TestRegisterAndListResourcePool(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	ref := "vault://pools/oc1"
	created, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{
		Name: "vCenter_OC1", Endpoint: "https://vcenter.internal", SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("CreateResourcePool: %v", err)
	}
	p := created.Pool
	if p.Name != "vCenter_OC1" || p.SyncStatus != model.ResourcePoolSyncStateNever {
		t.Fatalf("unexpected pool: %+v", p)
	}

	conn, err := qr.ResourcePools(ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("ResourcePools: %v", err)
	}
	if conn.TotalCount != 1 || len(conn.Nodes) != 1 || conn.Nodes[0].Endpoint != "https://vcenter.internal" {
		t.Fatalf("unexpected list: %+v", conn.Nodes)
	}

	del, err := mr.DeleteResourcePool(ctx, p.ID)
	if err != nil || del.ID != p.ID || del.DeletedName != "vCenter_OC1" {
		t.Fatalf("DeleteResourcePool: %+v err=%v", del, err)
	}
	conn, _ = qr.ResourcePools(ctx, nil, nil, nil)
	if conn.TotalCount != 0 {
		t.Fatalf("pool should be deleted, got %d", conn.TotalCount)
	}
}
