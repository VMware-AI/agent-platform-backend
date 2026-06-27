package vcenter

import (
	"context"
	"fmt"

	"github.com/vmware/govmomi/vapi/library"
	"github.com/vmware/govmomi/vapi/rest"
)

// ListContentLibraries returns the names of all content libraries on the
// vCenter. The REST session is opened and closed within this call; the caller
// supplies only the govmomi client already holding a SOAP session.
//
// An empty slice (not an error) is returned when the vCenter has no content
// libraries configured.
func (c *Client) ListContentLibraries(ctx context.Context) ([]string, error) {
	rc := rest.NewClient(c.vc.Client)
	if err := rc.Login(ctx, c.userinfo); err != nil {
		return nil, fmt.Errorf("vcenter: rest login: %w", err)
	}
	defer func() { _ = rc.Logout(ctx) }()

	m := library.NewManager(rc)
	// GetLibraries returns all libraries in one request (name + metadata).
	libs, err := m.GetLibraries(ctx)
	if err != nil {
		return nil, fmt.Errorf("vcenter: get libraries: %w", err)
	}
	names := make([]string, 0, len(libs))
	for _, lib := range libs {
		names = append(names, lib.Name)
	}
	return names, nil
}
