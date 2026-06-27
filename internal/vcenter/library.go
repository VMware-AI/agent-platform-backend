package vcenter

import (
	"context"
	"fmt"

	"github.com/vmware/govmomi/vapi/library"
	"github.com/vmware/govmomi/vapi/rest"
)

// ContentLibraryInfo summarizes a content library for the 资源池接入表单. Found
// reports whether a library with the requested name exists on the vCenter;
// ItemCount is the number of items in it (templates/ISOs), 0 when empty.
type ContentLibraryInfo struct {
	Found     bool
	ItemCount int
}

// VerifyContentLibrary logs into the vCenter vAPI (REST) endpoint and checks
// that a content library with the given name exists, returning its item count.
//
// It is used by the resource-pool onboarding 接入表单 to validate the operator's
// contentLibraryName against the real vCenter before the pool is committed —
// catching a typo'd library name at form time instead of at deploy time (when a
// missing library would otherwise fail an OVA clone). A missing library is a
// normal negative result (Found=false, nil error), NOT a transport error; only
// network/auth failures return an error.
func (c *Client) VerifyContentLibrary(ctx context.Context, name string) (ContentLibraryInfo, error) {
	rc := rest.NewClient(c.vc.Client)
	if err := rc.Login(ctx, c.userinfo); err != nil {
		return ContentLibraryInfo{}, fmt.Errorf("vcenter: rest login: %w", err)
	}
	defer func() { _ = rc.Logout(ctx) }()

	m := library.NewManager(rc)
	// FindLibrary returns matching ids ([] when none) — distinguishes a genuinely
	// missing library from a transport error, unlike GetLibraryByName (which errors
	// on not-found).
	ids, err := m.FindLibrary(ctx, library.Find{Name: name})
	if err != nil {
		return ContentLibraryInfo{}, fmt.Errorf("vcenter: find library %q: %w", name, err)
	}
	if len(ids) == 0 {
		return ContentLibraryInfo{Found: false}, nil
	}
	items, err := m.ListLibraryItems(ctx, ids[0])
	if err != nil {
		return ContentLibraryInfo{}, fmt.Errorf("vcenter: list items of library %q: %w", name, err)
	}
	return ContentLibraryInfo{Found: true, ItemCount: len(items)}, nil
}
