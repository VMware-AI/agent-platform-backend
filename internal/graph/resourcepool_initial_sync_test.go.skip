package graph

import (
	"context"
	"testing"
	"time"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
	"github.com/vmware/govmomi/simulator"
)

// TestResourcePool_FireAndForgetFirstSync: CreateResourcePool with stored
// credentials must fire the first sync in the background; querying the
// pool shortly after must show syncStatus = SYNCED with a populated
// inventory tree.
func TestResourcePool_FireAndForgetFirstSync(t *testing.T) {
	mdl := simulator.VPX()
	if err := mdl.Create(); err != nil {
		t.Fatalf("vcsim: %v", err)
	}
	srv := mdl.Service.NewServer()
	defer srv.Close()
	defer mdl.Remove()

	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.VCenterConnect = func(ctx context.Context, endpoint, user, pass string, insecure bool) (VCenterClient, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}
	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://sync-oc": {Username: "u", Password: "p"},
	})

	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	ctx := adminCtx()
	ref := "vault://sync-oc"
	created, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{
		Name: "faf-oc", Endpoint: srv.URL.String(), SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Poll for sync to complete (fire-and-forget goroutine, max 5s).
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := qr.ResourcePool(ctx, created.Pool.ID)
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if p.SyncStatus == model.ResourcePoolSyncStateSynced {
			if len(p.Datacenters) == 0 {
				t.Fatalf("synced pool but empty datacenters: %+v", p)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fire-and-forget sync did not finish in 5s; pool = %+v", p)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
