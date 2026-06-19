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
	p, err := mr.RegisterResourcePool(ctx, model.RegisterResourcePoolInput{
		Name: "vCenter_OC1", Endpoint: "https://vcenter.internal", SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("RegisterResourcePool: %v", err)
	}
	if p.Name != "vCenter_OC1" || p.Status != model.ResourcePoolStatusDisconnected {
		t.Fatalf("unexpected pool: %+v", p)
	}

	pools, err := qr.ResourcePools(ctx)
	if err != nil {
		t.Fatalf("ResourcePools: %v", err)
	}
	if len(pools) != 1 || pools[0].Endpoint != "https://vcenter.internal" {
		t.Fatalf("unexpected list: %+v", pools)
	}

	ok, err := mr.DeleteResourcePool(ctx, p.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteResourcePool: ok=%v err=%v", ok, err)
	}
	pools, _ = qr.ResourcePools(ctx)
	if len(pools) != 0 {
		t.Fatalf("pool should be deleted, got %d", len(pools))
	}
}
