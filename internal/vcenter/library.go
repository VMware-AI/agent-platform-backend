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

// LibraryItem is a single item inside a content library (typically an OVF/OVA
// package). Name matches the VM template name created when the OVA is deployed,
// so it is used directly as the ovaIdentifier stored in OvaTemplateVersion.
type LibraryItem struct {
	Name string
	Type string // "ovf", "iso", etc.
}

// ListContentLibraryItems returns all items in the named content library.
// An empty slice (not an error) is returned when the library exists but is empty
// or the named library is not found.
func (c *Client) ListContentLibraryItems(ctx context.Context, libraryName string) ([]LibraryItem, error) {
	rc := rest.NewClient(c.vc.Client)
	if err := rc.Login(ctx, c.userinfo); err != nil {
		return nil, fmt.Errorf("vcenter: rest login: %w", err)
	}
	defer func() { _ = rc.Logout(ctx) }()

	m := library.NewManager(rc)
	libs, err := m.GetLibraries(ctx)
	if err != nil {
		return nil, fmt.Errorf("vcenter: get libraries: %w", err)
	}

	var libID string
	for _, lib := range libs {
		if lib.Name == libraryName {
			libID = lib.ID
			break
		}
	}
	if libID == "" {
		return []LibraryItem{}, nil
	}

	items, err := m.GetLibraryItems(ctx, libID)
	if err != nil {
		return nil, fmt.Errorf("vcenter: get library items: %w", err)
	}
	out := make([]LibraryItem, 0, len(items))
	for _, item := range items {
		out = append(out, LibraryItem{Name: item.Name, Type: item.Type})
	}
	return out, nil
}
