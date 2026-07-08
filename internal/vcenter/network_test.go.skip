package vcenter

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// ListNetworks must return, as Path, a managed-object reference that resolves
// UNAMBIGUOUSLY to a network — this is what buildNetworkDevice consumes. A bare
// name/inventory-path would be ambiguous for same-named standard portgroups across
// hosts (find.MultipleFoundError) and fail the clone; the moref is unique.
func TestListNetworks_PathsResolveByMoref(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()

	nets, err := c.ListNetworks(context.Background())
	if err != nil {
		t.Fatalf("ListNetworks: %v", err)
	}
	if len(nets) == 0 {
		t.Fatal("vcsim should expose at least one network")
	}
	for _, n := range nets {
		var ref types.ManagedObjectReference
		if !ref.FromString(n.Path) {
			t.Errorf("network %q: Path %q is not a parseable moref", n.Name, n.Path)
			continue
		}
		if _, ok := object.NewReference(c.vc.Client, ref).(object.NetworkReference); !ok {
			t.Errorf("network %q: Path %q does not resolve to a NetworkReference", n.Name, n.Path)
		}
	}
}
