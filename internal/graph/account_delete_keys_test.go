package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// TestDeleteUser_RevokesUserVirtualKeys pins the orphan-key fix (#29): deleting a
// user must revoke its live litellm keys at the gateway and mark the rows revoked
// — not leave them live & billable. virtual_keys has no FK cascade on user_id, so
// before the fix DeleteUser dropped only the user row and left the keys ungoverned
// (and undetectable by reconcile once the row was gone).
func TestDeleteUser_RevokesUserVirtualKeys(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	fg := &fakeGateway{}
	r.Gateway = fg
	mr := &mutationResolver{r}

	owner := mkUser(t, mr, ctx, "leaver", "lv@x.io", model.RoleNameUser)
	if _, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{UserID: owner.ID}); err != nil {
		t.Fatalf("IssueVirtualKey: %v", err)
	}

	if _, err := mr.DeleteUser(ctx, owner.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// (1) the user's live litellm key was revoked at the gateway, not orphaned.
	if len(fg.deleted) != 1 {
		t.Fatalf("user's key must be revoked at the gateway on delete, got %d (%v)", len(fg.deleted), fg.deleted)
	}
	// (2) the row is marked revoked, not left active/ungoverned.
	if vk := r.Ent.VirtualKey.Query().OnlyX(ctx); vk.Status != virtualkey.StatusRevoked {
		t.Fatalf("vk status = %v, want revoked", vk.Status)
	}
}
