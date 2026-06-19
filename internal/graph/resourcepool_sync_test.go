package graph

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

func TestResourcePool_SyncTestUpdate(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

	mdl := simulator.VPX()
	if err := mdl.Create(); err != nil {
		t.Fatalf("vcsim: %v", err)
	}
	vsrv := mdl.Service.NewServer()
	defer vsrv.Close()
	defer mdl.Remove()

	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://oc": {Username: "u", Password: "p"},
	})
	r.VCenterConnect = func(ctx context.Context, endpoint, user, pass string, insecure bool) (VCenterClient, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}

	ctx := adminCtx()
	mr := &mutationResolver{r}
	ref := "vault://oc"
	pool, err := mr.RegisterResourcePool(ctx, model.RegisterResourcePoolInput{
		Name: "oc1", Endpoint: vsrv.URL.String(), SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// sync inventory from vcsim
	synced, err := mr.SyncResourcePool(ctx, pool.ID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if synced.Status != model.ResourcePoolStatusConnected {
		t.Fatalf("status = %v, want connected", synced.Status)
	}
	if synced.HostCount == 0 || synced.VMCount == 0 {
		t.Fatalf("inventory counts not populated: hosts=%d vms=%d", synced.HostCount, synced.VMCount)
	}

	// test connection
	tested, err := mr.TestResourcePoolConnection(ctx, pool.ID)
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if tested.Status != model.ResourcePoolStatusConnected {
		t.Fatalf("test status = %v", tested.Status)
	}

	// update
	newName := "oc1-renamed"
	upd, err := mr.UpdateResourcePool(ctx, pool.ID, model.UpdateResourcePoolInput{Name: &newName})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "oc1-renamed" {
		t.Fatalf("name not updated: %s", upd.Name)
	}
}
