package vcenter

import (
	"context"
	"fmt"

	"github.com/vmware/govmomi/pbm"
	"github.com/vmware/govmomi/pbm/types"
)

// listStorageProfiles queries PBM for the vCenter's storage profiles and
// returns them as PlacementRef entries. Name is the display label; Path is
// the profile UniqueID so callers can disambiguate when profiles share a
// name (rare, but allowed in vSphere).
//
// Returns a non-nil error when PBM is unreachable, the session fails, or
// any list/retrieve call faults. The caller (FullInventory) treats any error
// as a graceful empty — storagePolicies stays nil on each DC, which the
// frontend distinguishes from "pulled but empty" via null vs [].
func (c *Client) listStorageProfiles(ctx context.Context) ([]PlacementRef, error) {
	pc, err := pbm.NewClient(ctx, c.vc.Client)
	if err != nil {
		return nil, fmt.Errorf("vcenter: pbm client: %w", err)
	}

	// QueryProfile(rtype, category): rtype.ResourceType is a string from
	// PbmProfileResourceTypeEnum ("STORAGE" today). Empty resource type
	// means "all kinds"; empty category skips the category filter.
	ids, err := pc.QueryProfile(ctx,
		types.PbmProfileResourceType{ResourceType: "STORAGE"}, "")
	if err != nil {
		return nil, fmt.Errorf("vcenter: pbm query profile: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	contents, err := pc.RetrieveContent(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("vcenter: pbm retrieve content: %w", err)
	}
	if len(contents) == 0 {
		return nil, nil
	}
	out := make([]PlacementRef, 0, len(contents))
	for _, base := range contents {
		// BasePbmProfile is the boxed interface; cast to *PbmProfile for
		// the fields we need. Profiles that don't unwrap cleanly are
		// skipped rather than failing the whole sync.
		profile := base.GetPbmProfile()
		if profile == nil {
			continue
		}
		out = append(out, PlacementRef{
			Name: profile.Name,
			Path: string(profile.ProfileId.UniqueId),
		})
	}
	return out, nil
}