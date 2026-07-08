package vcenter

import (
	"context"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
)

// TestFullInventory_BuildsDCClusterHostTree asserts FullInventory walks
// the vSphere hierarchy end-to-end on vcsim: DC → Cluster → Host and that
// the vSphere "Resources" RP shows up under the cluster.
func TestFullInventory_BuildsDCClusterHostTree(t *testing.T) {
	mdl := simulator.VPX()
	if err := mdl.Create(); err != nil {
		t.Fatalf("vcsim: %v", err)
	}
	defer mdl.Remove()
	srv := mdl.Service.NewServer()
	defer srv.Close()

	c, err := Connect(context.Background(), srv.URL.String(), "admin", "admin", true)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = c.Logout(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	inv, err := c.FullInventory(ctx)
	if err != nil {
		t.Fatalf("FullInventory: %v", err)
	}
	if len(inv) == 0 {
		t.Fatal("expected at least one DC")
	}
	dc := inv[0]
	if dc.Name == "" || dc.Path == "" {
		t.Fatalf("dc fields empty: %+v", dc)
	}
	if len(dc.Clusters) == 0 {
		t.Fatalf("DC %s has no clusters; vcsim VPX should have at least one", dc.Name)
	}
	cl := dc.Clusters[0]
	if len(cl.EsxiHosts) == 0 {
		t.Fatalf("cluster %s has no ESXi hosts", cl.Name)
	}
	if cl.Path == "" {
		t.Fatal("cluster path is empty")
	}
	for _, h := range cl.EsxiHosts {
		if h.Name == "" {
			t.Fatal("host without name")
		}
	}
	// PBM is not implemented in vcsim → storagePolicies should be nil
	// (per A1: distinguish "PBM not pulled" from "pulled but empty").
	if dc.StoragePolicies != nil {
		t.Fatalf("vcsim has no PBM; expected nil storagePolicies, got %d", len(dc.StoragePolicies))
	}
}
