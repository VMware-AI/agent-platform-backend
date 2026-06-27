package graph

import (
	"context"
	"net/url"
	"testing"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vapi/library"
	"github.com/vmware/govmomi/vapi/rest"
	_ "github.com/vmware/govmomi/vapi/simulator" // registers vAPI REST endpoints

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// seedSimLibrary creates a LOCAL content library on the vcsim default datastore so
// the authenticated TestResourcePoolConnection probe has something to verify. It
// opens its own govmomi client (the vcenter.Client wrapper keeps its vim25 client
// private) purely to seed REST state.
func seedSimLibrary(t *testing.T, srvURL, name string) {
	t.Helper()
	ctx := context.Background()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.User = simulator.DefaultLogin
	gc, err := govmomi.NewClient(ctx, u, true)
	if err != nil {
		t.Fatalf("seed connect: %v", err)
	}
	rc := rest.NewClient(gc.Client)
	if err := rc.Login(ctx, simulator.DefaultLogin); err != nil {
		t.Fatalf("rest login: %v", err)
	}
	ds, err := find.NewFinder(gc.Client).DefaultDatastore(ctx)
	if err != nil {
		t.Fatalf("datastore: %v", err)
	}
	if _, err := library.NewManager(rc).CreateLibrary(ctx, library.Library{
		Name: name, Type: "LOCAL",
		Storage: []library.StorageBacking{{DatastoreID: ds.Reference().Value, Type: "DATASTORE"}},
	}); err != nil {
		t.Fatalf("seed library: %v", err)
	}
}

// TestTestResourcePoolConnection_Authenticated covers the credentialed probe path:
// a real vcsim login reports the vSphere version and verifies the content library.
func TestTestResourcePoolConnection_Authenticated(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

	mdl := simulator.VPX()
	if err := mdl.Create(); err != nil {
		t.Fatalf("vcsim: %v", err)
	}
	mdl.Service.RegisterEndpoints = true // serve REST (content library) endpoints
	vsrv := mdl.Service.NewServer()
	defer vsrv.Close()
	defer mdl.Remove()

	seedSimLibrary(t, vsrv.URL.String(), "tkg")

	r.VCenterConnect = func(ctx context.Context, endpoint, user, pass string, insecure bool) (VCenterClient, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}
	insecure := true
	user, pass := "u", "p"

	// Existing library → ok + version + found.
	ok, err := mr.TestResourcePoolConnection(ctx, model.TestResourcePoolConnectionInput{
		Name: "oc", Endpoint: vsrv.URL.String(), ContentLibraryName: "tkg",
		Username: &user, Password: &pass, Insecure: &insecure,
	})
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if !ok.Ok {
		t.Fatalf("expected ok=true, got %s", ok.Message)
	}
	if ok.Detail == nil || !ok.Detail.ContentLibraryFound {
		t.Fatalf("expected contentLibraryFound=true, detail=%+v", ok.Detail)
	}
	if ok.Detail.VSphereVersion == "" {
		t.Fatal("expected a non-empty vSphereVersion from the authenticated probe")
	}

	// Missing library → ok=false, found=false, but no transport error.
	miss, err := mr.TestResourcePoolConnection(ctx, model.TestResourcePoolConnectionInput{
		Name: "oc", Endpoint: vsrv.URL.String(), ContentLibraryName: "does-not-exist",
		Username: &user, Password: &pass, Insecure: &insecure,
	})
	if err != nil {
		t.Fatalf("test connection (missing lib): %v", err)
	}
	if miss.Ok {
		t.Fatal("expected ok=false for a missing content library")
	}
	if miss.Detail == nil || miss.Detail.ContentLibraryFound {
		t.Fatalf("expected contentLibraryFound=false, detail=%+v", miss.Detail)
	}
}
