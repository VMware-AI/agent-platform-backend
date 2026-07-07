package vcenter

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/vapi/library"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// OVFProperty represents a single user-configurable vApp/OVF property from a
// VM template's vAppConfig. It mirrors the OVF PropertySection element, decoupled
// from govmomi's types so the GraphQL layer does not import vCenter internals.
type OVFProperty struct {
	Key          string   `json:"key"`          // e.g. "guestinfo.hostname"
	Label        string   `json:"label"`        // Display name
	Type         string   `json:"type"`         // "string" | "password" | "boolean" | "int" | "real" | "enum"
	DefaultValue string   `json:"defaultValue"` // Pre-filled value
	Description  string   `json:"description"`
	Required     bool     `json:"required"` // userConfigurable==false → read-only, not surfaced in UI
	Password     bool     `json:"password"` // Mask in UI
	Values       []string `json:"values"`   // enum choices, empty for non-enum
	Category     string   `json:"category"` // OVF category grouping
}

// GetTemplateVAppProperties reads the vAppConfig from a deployed VM template and
// returns its user-configurable OVF properties. Returns an empty slice (not error)
// when the template has no vAppConfig or no properties.
func (c *Client) GetTemplateVAppProperties(ctx context.Context, templateName string) ([]OVFProperty, error) {
	finder := find.NewFinder(c.vc.Client, true)
	dc, err := finder.DefaultDatacenter(ctx)
	if err != nil {
		return nil, fmt.Errorf("vcenter: default datacenter: %w", err)
	}
	finder.SetDatacenter(dc)

	vm, err := finder.VirtualMachine(ctx, templateName)
	if err != nil {
		return nil, fmt.Errorf("vcenter: find template %q: %w", templateName, err)
	}

	var mvm mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"config.vAppConfig"}, &mvm); err != nil {
		return nil, fmt.Errorf("vcenter: read vAppConfig for %q: %w", templateName, err)
	}

	vapp := mvm.Config.VAppConfig
	if vapp == nil {
		return []OVFProperty{}, nil
	}
	vci, ok := vapp.(*types.VmConfigInfo)
	if !ok || len(vci.Property) == 0 {
		return []OVFProperty{}, nil
	}

	out := make([]OVFProperty, 0, len(vci.Property))
	for _, p := range vci.Property {
		userCfg := p.UserConfigurable != nil && *p.UserConfigurable
		required := !userCfg
		def := p.DefaultValue
		label := p.Label
		if label == "" {
			label = p.Id
		}

		isPassword := strings.HasPrefix(p.Type, "password")

		out = append(out, OVFProperty{
			Key:          p.Id,
			Label:        label,
			Type:         p.Type,
			DefaultValue: def,
			Description:  p.Description,
			Required:     required,
			Password:     isPassword,
			Values:       nil,
			Category:     p.Category,
		})
	}
	return out, nil
}

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
