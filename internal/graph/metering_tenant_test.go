package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// TestRecordTokenUsage_StampsTenantFromUser pins the C1 metering write-side fix
// (#30): RecordTokenUsage must stamp tenant_id derived from the attributed user.
// The gateway usage callback carries no caller tenant, so without this every
// metering row is NULL-tenant and a tenant-admin (whose reads go through
// tenantScopeFor) sees nothing.
func TestRecordTokenUsage_StampsTenantFromUser(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	tid := uuid.New()
	u := r.Ent.User.Create().
		SetUsername("metered").SetEmail("m@x.io").SetPasswordHash("h").
		SetTenantID(tid).SaveX(ctx)

	tu, err := mr.RecordTokenUsage(ctx, model.RecordTokenUsageInput{
		UserID: u.ID.String(), Model: "gpt-4o", InputTokens: 10, OutputTokens: 5,
	})
	if err != nil {
		t.Fatalf("RecordTokenUsage: %v", err)
	}

	// the row must carry the user's tenant so tenant-scoped reads see it.
	row := r.Ent.TokenUsage.GetX(ctx, uuid.MustParse(tu.ID))
	if row.TenantID == nil || *row.TenantID != tid {
		t.Fatalf("token usage tenant_id = %v, want %v", row.TenantID, tid)
	}
}
