package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/rotationcommand"
	"github.com/VMware-AI/agent-platform-backend/internal/agentmgr"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

func TestRequestRotationAndRevoke_Authz(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.AgentMgr = &agentmgr.Service{Ent: r.Ent, Secrets: secrets.NewStaticResolver(nil)}
	mr := &mutationResolver{r}
	ctx := context.Background()

	owner, _ := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "rot", Email: "rot@x.io", Password: "RotatePass12", Role: model.RoleUser,
	})
	ownerCtx := userCtx(owner.ID, "user")
	seedActiveTemplate(t, r, "goose")
	ag, err := mr.CreateAgent(ownerCtx, model.CreateAgentInput{Name: "rot-goose", AgentType: "goose"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// owner can request a rotation → a pending command is created
	ok, err := mr.RequestRotation(ownerCtx, ag.ID, model.RotationKindRotateUIPassword)
	if err != nil || !ok {
		t.Fatalf("RequestRotation(owner): ok=%v err=%v", ok, err)
	}
	aid := uuid.MustParse(ag.ID)
	if n := r.Ent.RotationCommand.Query().Where(rotationcommand.AgentID(aid)).CountX(ctx); n != 1 {
		t.Fatalf("expected 1 rotation command, got %d", n)
	}

	// a stranger (non-owner, non-admin) is denied (404-style via getOwnedAgent)
	stranger := userCtx("33333333-3333-3333-3333-333333333333", "user")
	if _, err := mr.RequestRotation(stranger, ag.ID, model.RotationKindRotateUIPassword); err == nil {
		t.Fatal("non-owner should be denied requestRotation")
	}
	if _, err := mr.RevokeAgentEnrollment(stranger, ag.ID); err == nil {
		t.Fatal("non-owner should be denied revokeAgentEnrollment")
	}

	// owner can revoke (idempotent even with no enrollment row yet)
	if ok, err := mr.RevokeAgentEnrollment(ownerCtx, ag.ID); err != nil || !ok {
		t.Fatalf("RevokeAgentEnrollment(owner): ok=%v err=%v", ok, err)
	}
}

func TestRequestRotation_NotConfigured(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	// AgentMgr nil → mutation reports not-configured rather than panicking
	mr := &mutationResolver{r}
	if _, err := mr.RequestRotation(adminCtx(), uuid.NewString(), model.RotationKindRotateUIPassword); err == nil {
		t.Fatal("expected error when agent-manager is not configured")
	}
}
