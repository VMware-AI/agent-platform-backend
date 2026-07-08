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
	created, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{
		Name: "oc1", Endpoint: vsrv.URL.String(), SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pool := created.Pool

	// sync inventory from vcsim
	syncedPayload, err := mr.SyncResourcePool(ctx, pool.ID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	synced := syncedPayload.Pool
	// A real sync stamps status=connected and lastSyncedAt → projects to SYNCED.
	if synced.LastSyncedAt == nil || synced.SyncStatus != model.ResourcePoolSyncStateSynced {
		t.Fatalf("real sync should set lastSyncedAt + SYNCED: %v / %v", synced.LastSyncedAt, synced.SyncStatus)
	}
	// The full inventory tree must have at least one DC + one host.
	if len(synced.Datacenters) == 0 {
		t.Fatalf("expected at least one datacenter, got 0")
	}
	dc0 := synced.Datacenters[0]
	if len(dc0.Clusters) == 0 {
		t.Fatalf("expected DC0 to have at least one cluster, got 0")
	}
	if len(dc0.Clusters[0].EsxiHosts) == 0 {
		t.Fatalf("expected DC0 first cluster to have at least one ESXi host")
	}
	if syncedPayload.SyncedAt.IsZero() {
		t.Fatal("syncedAt not set")
	}

	// test connection: the credential-less pre-save probe TCP-dials the endpoint.
	// vcsim's URL is reachable, so the probe should report ok=true.
	tested, err := mr.TestResourcePoolConnection(ctx, model.TestResourcePoolConnectionInput{
		Name: "oc1", Endpoint: vsrv.URL.String(),
	})
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if !tested.Ok {
		t.Fatalf("test probe should be ok for a reachable endpoint: %s", tested.Message)
	}

	// an unreachable / malformed endpoint reports ok=false (not an error).
	bad, err := mr.TestResourcePoolConnection(ctx, model.TestResourcePoolConnectionInput{
		Name: "oc1", Endpoint: "",
	})
	if err != nil {
		t.Fatalf("test connection (bad): unexpected error %v", err)
	}
	if bad.Ok {
		t.Fatal("empty endpoint should report ok=false")
	}

	// update
	newName := "oc1-renamed"
	upd, err := mr.UpdateResourcePool(ctx, pool.ID, model.UpdateResourcePoolInput{Name: &newName})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Pool.Name != "oc1-renamed" {
		t.Fatalf("name not updated: %s", upd.Pool.Name)
	}
}
