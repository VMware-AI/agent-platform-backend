package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// #36 coverage: rollbackDeployCreate compensates a failed create-from-OVA deploy
// AFTER the VM + key exist — it must destroy the VM, revoke the key through the
// passed-in gateway (NOT a nil r.Gateway), and DELETE the freshly-created agent
// row (rather than marking it exception).
func TestRollbackDeployCreate_DestroysVMRevokesKeyDeletesRow(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	// r.Gateway deliberately nil (prod after LLD-13): rollback must revoke through
	// the gateway passed in, else it silently leaks the key.
	fg := &fakeGateway{}
	ctx := context.Background()
	mr := &mutationResolver{r}

	owner, err := mr.CreateUser(ctx, model.CreateUserInput{Username: "o", DisplayName: "o", Email: "o@x.io", RoleID: string(model.RoleNameUser), PasswordMode: model.PasswordModeCustom, CustomPassword: ptr("OwnerPass123")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	seedActiveTemplate(t, r, "goose")
	ag, err := mr.CreateAgent(userCtx(owner.User.ID, "user"), model.CreateAgentInput{Name: "a", AgentType: "goose"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	aid := uuid.MustParse(ag.ID)
	agRow := r.Ent.Agent.GetX(ctx, aid)

	fvc := &fakeVCenter{}
	r.rollbackDeployCreate(ctx, fvc, fg, agRow, "vm-xyz", "sk-live-key")

	if len(fvc.destroyed) != 1 || fvc.destroyed[0] != "vm-xyz" {
		t.Errorf("VM not destroyed: %v", fvc.destroyed)
	}
	if len(fg.deleted) != 1 || fg.deleted[0] != "sk-live-key" {
		t.Errorf("gateway key not revoked through passed-in gateway: %v", fg.deleted)
	}
	// create-from-OVA rollback DELETES the row (it never went live), rather than marking it exception.
	if _, err := r.Ent.Agent.Get(ctx, aid); err == nil {
		t.Error("agent row should be deleted by rollbackDeployCreate")
	}
}

// revokeDeployKey with no gateway must not panic and must not pretend to revoke
// (the orphan-key honesty branch — logged, never silent).
func TestRevokeDeployKey_NilGatewayNoPanic(t *testing.T) {
	// Pure function, nil gateway: must return cleanly (logs an orphan warning).
	revokeDeployKey(context.Background(), nil, "sk-orphan", "agent-1")
}
