package agentmgr

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/rotationcommand"
)

// #8: at most ONE in-flight rotation per (agent, kind). createCommand is the
// shared insert behind both applyMaxAge and RequestRotation; calling it twice
// (simulating two callers that both passed the EXISTS pre-check — the TOCTOU)
// must NOT create a duplicate: the partial unique index rejects the second and
// createCommand returns (nil, nil) so the caller no-ops.
func TestCreateCommand_UniqueIndexBlocksDuplicateInFlight(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	agentID := uuid.New()

	c1, err := svc.createCommand(ctx, agentID, rotationcommand.KindRotateUIPassword, "max_age")
	if err != nil || c1 == nil {
		t.Fatalf("first createCommand should succeed: cmd=%v err=%v", c1, err)
	}
	// Lost-the-race insert: must be treated as benign (already in flight), not an error.
	c2, err := svc.createCommand(ctx, agentID, rotationcommand.KindRotateUIPassword, "max_age")
	if err != nil {
		t.Fatalf("duplicate createCommand must treat the constraint as benign, got err: %v", err)
	}
	if c2 != nil {
		t.Fatal("duplicate in-flight createCommand should return nil (index-rejected)")
	}

	n, err := svc.Ent.RotationCommand.Query().
		Where(rotationcommand.AgentID(agentID), rotationcommand.KindEQ(rotationcommand.KindRotateUIPassword)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 rotation row for (agent, kind), got %d", n)
	}
}

// A DIFFERENT kind is allowed concurrently (the uniqueness is per (agent, kind)).
func TestCreateCommand_DifferentKindAllowed(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	agentID := uuid.New()

	if _, err := svc.createCommand(ctx, agentID, rotationcommand.KindRotateUIPassword, "manual"); err != nil {
		t.Fatalf("ui rotation: %v", err)
	}
	c, err := svc.createCommand(ctx, agentID, rotationcommand.KindRotateOsPassword, "manual")
	if err != nil || c == nil {
		t.Fatalf("a different kind should be allowed in flight: cmd=%v err=%v", c, err)
	}
}

// After a rotation reaches a terminal state, a NEW one is allowed (the partial
// index only covers in-flight states).
func TestCreateCommand_AllowedAfterTerminal(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	agentID := uuid.New()

	c1, err := svc.createCommand(ctx, agentID, rotationcommand.KindRotateUIPassword, "max_age")
	if err != nil || c1 == nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := svc.Ent.RotationCommand.UpdateOne(c1).SetStatus(rotationcommand.StatusCompleted).Save(ctx); err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	c2, err := svc.createCommand(ctx, agentID, rotationcommand.KindRotateUIPassword, "max_age")
	if err != nil || c2 == nil {
		t.Fatalf("a new rotation should be allowed after the prior one completed: cmd=%v err=%v", c2, err)
	}
}
