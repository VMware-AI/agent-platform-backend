package catalog

import (
	"context"
	"fmt"
	"log"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/ovatemplatefamily"
	"github.com/VMware-AI/agent-platform-backend/ent/ovatemplateversion"
)

// DefaultOpenCodeOvaIdentifier is the content-library / VM template name the
// OpenCode marketplace entry must clone from.
const DefaultOpenCodeOvaIdentifier = "builder-opencode-temp-v7"

// RepairOvaTemplates fixes known OVA catalog drift without disturbing the rest
// of the marketplace. Today that means ensuring the OpenCode family points at
// the dedicated OpenCode template instead of the Hermes template that was
// mistakenly copied into the catalog row.
func RepairOvaTemplates(ctx context.Context, client *ent.Client) error {
	affected, err := client.OvaTemplateVersion.Update().
		Where(
			ovatemplateversion.HasFamilyWith(ovatemplatefamily.TypeEQ("OPENCODE")),
			ovatemplateversion.OvaIdentifierNEQ(DefaultOpenCodeOvaIdentifier),
		).
		SetOvaIdentifier(DefaultOpenCodeOvaIdentifier).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("repair opencode ova template: %w", err)
	}
	if affected > 0 {
		log.Printf("catalog: repaired %d opencode ova template version(s)", affected)
	}
	return nil
}
