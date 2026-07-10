package agentmgr

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/agentenrollment"
)

// DeleteEnrollment removes the enrollment row entirely (deploy-rollback cleanup):
// after deletion the credential no longer authenticates and no row remains. It is
// idempotent — deleting a missing enrollment is a no-op (used on every failed
// deploy, including ones where IssueEnrollment never ran).
func TestDeleteEnrollment_RemovesRowAndIdempotent(t *testing.T) {
	svc, _, done := newTestService(t)
	defer done()
	ctx := context.Background()

	agentID, vmID, vmToken := enrolled(t, svc)

	// Sanity: the active enrollment authenticates before deletion.
	if _, err := svc.Authenticate(ctx, vmID, vmToken); err != nil {
		t.Fatalf("pre-delete auth failed: %v", err)
	}

	if err := svc.DeleteEnrollment(ctx, agentID); err != nil {
		t.Fatalf("DeleteEnrollment: %v", err)
	}

	// The row is gone: no enrollment for that agent id, and the token no longer
	// authenticates (vm_id lookup misses).
	if n := svc.Ent.AgentEnrollment.Query().Where(agentenrollment.AgentID(agentID)).CountX(ctx); n != 0 {
		t.Fatalf("expected 0 enrollment rows after delete, got %d", n)
	}
	if _, err := svc.Authenticate(ctx, vmID, vmToken); !errors.Is(err, ErrAuth) {
		t.Fatalf("post-delete auth must be ErrAuth, got %v", err)
	}

	// Idempotent: deleting again (and deleting a never-seen agent) is a clean no-op.
	if err := svc.DeleteEnrollment(ctx, agentID); err != nil {
		t.Fatalf("second DeleteEnrollment should be a no-op, got %v", err)
	}
	if err := svc.DeleteEnrollment(ctx, uuid.New()); err != nil {
		t.Fatalf("DeleteEnrollment on missing enrollment should be a no-op, got %v", err)
	}
}
