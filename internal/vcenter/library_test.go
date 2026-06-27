package vcenter

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vapi/library"
	"github.com/vmware/govmomi/vapi/rest"
	_ "github.com/vmware/govmomi/vapi/simulator" // registers vAPI REST endpoints with vcsim
)

// withSimLibrary spins a vcsim that ALSO serves the vAPI (REST) content-library
// endpoints, seeds one library, and returns a connected Client plus the seeded
// library name. Mirrors withSim but enables REST endpoint registration.
func withSimLibrary(t *testing.T, libName string) (*Client, string, func()) {
	t.Helper()
	model := simulator.VPX()
	if err := model.Create(); err != nil {
		t.Fatalf("vcsim model create: %v", err)
	}
	model.Service.RegisterEndpoints = true // serve vapi/simulator REST handlers

	srv := model.Service.NewServer()
	c, err := Connect(context.Background(), srv.URL.String(), "user", "pass", true)
	if err != nil {
		srv.Close()
		model.Remove()
		t.Fatalf("connect: %v", err)
	}

	seedLibrary(t, c, libName)

	cleanup := func() {
		_ = c.Logout(context.Background())
		srv.Close()
		model.Remove()
	}
	return c, libName, cleanup
}

// seedLibrary creates one LOCAL content library named name on the default
// datastore (vcsim) so VerifyContentLibrary has something to find.
func seedLibrary(t *testing.T, c *Client, name string) {
	t.Helper()
	ctx := context.Background()
	rc := rest.NewClient(c.vc.Client)
	if err := rc.Login(ctx, c.userinfo); err != nil {
		t.Fatalf("rest login: %v", err)
	}
	ds, err := find.NewFinder(c.vc.Client).DefaultDatastore(ctx)
	if err != nil {
		t.Fatalf("default datastore: %v", err)
	}
	m := library.NewManager(rc)
	if _, err := m.CreateLibrary(ctx, library.Library{
		Name: name,
		Type: "LOCAL",
		Storage: []library.StorageBacking{{
			DatastoreID: ds.Reference().Value,
			Type:        "DATASTORE",
		}},
	}); err != nil {
		t.Fatalf("seed library: %v", err)
	}
}

func TestVerifyContentLibrary_Found(t *testing.T) {
	c, name, cleanup := withSimLibrary(t, "tkg")
	defer cleanup()

	info, err := c.VerifyContentLibrary(context.Background(), name)
	if err != nil {
		t.Fatalf("VerifyContentLibrary: %v", err)
	}
	if !info.Found {
		t.Fatalf("expected library %q to be found", name)
	}
	if info.ItemCount < 0 {
		t.Fatalf("itemCount must be non-negative, got %d", info.ItemCount)
	}
}

func TestVerifyContentLibrary_NotFound(t *testing.T) {
	c, _, cleanup := withSimLibrary(t, "tkg")
	defer cleanup()

	info, err := c.VerifyContentLibrary(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("VerifyContentLibrary should not error on a missing library: %v", err)
	}
	if info.Found {
		t.Fatal("expected Found=false for a missing library")
	}
}
