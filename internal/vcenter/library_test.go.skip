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
// datastore (vcsim) so ListContentLibraries returns at least one entry.
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

// ListContentLibraries returns the seeded library when the vCenter has one.
func TestListContentLibraries_Found(t *testing.T) {
	c, name, cleanup := withSimLibrary(t, "tkg")
	defer cleanup()

	libs, err := c.ListContentLibraries(context.Background())
	if err != nil {
		t.Fatalf("ListContentLibraries: %v", err)
	}
	found := false
	for _, l := range libs {
		if l == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected seeded library %q in %v", name, libs)
	}
}

// ListContentLibraries must not report a library that was never created.
func TestListContentLibraries_ExcludesAbsent(t *testing.T) {
	c, _, cleanup := withSimLibrary(t, "tkg")
	defer cleanup()

	libs, err := c.ListContentLibraries(context.Background())
	if err != nil {
		t.Fatalf("ListContentLibraries: %v", err)
	}
	for _, l := range libs {
		if l == "does-not-exist" {
			t.Fatalf("library %q was never seeded but appears in %v", "does-not-exist", libs)
		}
	}
}
