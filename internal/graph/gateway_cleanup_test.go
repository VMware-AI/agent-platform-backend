package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

// #29 gateway cleanup: delete/rotate guards + secret-store hygiene + key-toggle
// propagation. Verifies a gateway can't be orphaned, secrets don't leak, and a
// disabled key is actually blocked at the gateway.

func litellmGatewayInput(name, endpoint string, masterKey *string) model.ModelGatewayInput {
	return model.ModelGatewayInput{
		Name:                  name,
		Endpoint:              endpoint,
		Provider:              model.ModelGatewayProviderLitellm,
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin,
		MasterKey:             masterKey,
	}
}

// The default gateway can't be deleted (the platform must always keep one).
func TestDeleteModelGateway_RefusedWhenDefault(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	g, err := r.Ent.GatewayConnection.Create().SetName("def").SetEndpoint("http://x:4000").SetIsDefault(true).Save(ctx)
	if err != nil {
		t.Fatalf("seed gateway: %v", err)
	}
	if _, err := mr.DeleteModelGateway(ctx, g.ID.String()); err == nil {
		t.Fatal("must refuse to delete the default gateway")
	}
}

// A gateway still referenced by a department can't be deleted (soft FK).
func TestDeleteModelGateway_RefusedWhenDepartmentReferences(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	g, err := r.Ent.GatewayConnection.Create().SetName("gw").SetEndpoint("http://x:4000").Save(ctx)
	if err != nil {
		t.Fatalf("seed gateway: %v", err)
	}
	if _, err := r.Ent.Department.Create().SetName("research").SetGatewayConnectionID(g.ID).Save(ctx); err != nil {
		t.Fatalf("seed dept: %v", err)
	}
	if _, err := mr.DeleteModelGateway(ctx, g.ID.String()); err == nil {
		t.Fatal("must refuse to delete a gateway a department still references")
	}
}

// Deleting a gateway removes its master-key secret from the store (no orphan).
func TestDeleteModelGateway_DeletesMasterKeySecret(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Secrets = secrets.NewStaticResolver(nil)
	ctx := adminCtx()
	mr := &mutationResolver{r}

	key := "sk-master-xyz"
	g, err := mr.CreateModelGateway(ctx, litellmGatewayInput("gw1", "http://lite:4000", &key))
	if err != nil {
		t.Fatalf("CreateModelGateway: %v", err)
	}
	ref := r.Ent.GatewayConnection.GetX(ctx, uuid.MustParse(g.ID)).MasterKeyRef
	if ref == "" {
		t.Fatal("master-key ref not stored")
	}
	if _, err := r.Secrets.Resolve(ctx, ref); err != nil {
		t.Fatalf("secret should be present before delete: %v", err)
	}

	if _, err := mr.DeleteModelGateway(ctx, g.ID); err != nil {
		t.Fatalf("DeleteModelGateway: %v", err)
	}
	if _, err := r.Secrets.Resolve(ctx, ref); err == nil {
		t.Fatal("master-key secret should be deleted (no orphan) after gateway delete")
	}
}

// Rotating the master key retires the prior secret (no orphan on rename/rotate).
func TestUpdateModelGateway_RotationDeletesPriorSecret(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Secrets = secrets.NewStaticResolver(nil)
	ctx := adminCtx()
	mr := &mutationResolver{r}

	k1 := "sk-key-1"
	g, err := mr.CreateModelGateway(ctx, litellmGatewayInput("gw", "http://x:4000", &k1))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gid := uuid.MustParse(g.ID)
	ref1 := r.Ent.GatewayConnection.GetX(ctx, gid).MasterKeyRef

	k2 := "sk-key-2"
	if _, err := mr.UpdateModelGateway(ctx, g.ID, litellmGatewayInput("gw", "http://x:4000", &k2)); err != nil {
		t.Fatalf("update: %v", err)
	}
	ref2 := r.Ent.GatewayConnection.GetX(ctx, gid).MasterKeyRef
	if ref2 == "" || ref2 == ref1 {
		t.Fatalf("expected a rotated ref, got ref1=%q ref2=%q", ref1, ref2)
	}
	if _, err := r.Secrets.Resolve(ctx, ref1); err == nil {
		t.Fatal("prior master-key secret should be deleted after rotation")
	}
	if _, err := r.Secrets.Resolve(ctx, ref2); err != nil {
		t.Fatalf("new master-key secret should be present: %v", err)
	}
}

// Revoking with no gateway configured must FAIL, not silently mark a still-live
// key revoked (which would hide it from the reconciler and keep it billing).
func TestRevokeVirtualKey_NoGatewayDoesNotMarkRevoked(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	vk, err := r.Ent.VirtualKey.Create().
		SetLitellmKey("sk-live").SetUserID(uuid.New()).SetStatus(virtualkey.StatusActive).Save(ctx)
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
	if _, err := mr.RevokeVirtualKey(ctx, vk.ID.String()); err == nil {
		t.Fatal("revoke with no gateway must fail, not silently revoke the live key")
	}
	if got := r.Ent.VirtualKey.GetX(ctx, vk.ID); got.Status == virtualkey.StatusRevoked {
		t.Fatal("key must NOT be marked revoked when the gateway delete never happened")
	}
}

// Toggling a key enabled/disabled propagates to the gateway as blocked=true/false
// (litellm /key/update) — not just a DB status flip.
func TestSetVirtualKeyEnabled_PropagatesBlockToGateway(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	ctx := context.Background()
	mr := &mutationResolver{r}

	u := mkUser(t, mr, ctx, "vkuser", "vk@x.io", model.RoleNameUser)
	issued, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{UserID: u.ID, Models: []string{"smart"}})
	if err != nil {
		t.Fatalf("IssueVirtualKey: %v", err)
	}

	if _, err := mr.SetVirtualKeyEnabled(ctx, issued.VirtualKey.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if len(fg.updated) != 1 || fg.updated[0].Blocked == nil || !*fg.updated[0].Blocked {
		t.Fatalf("disable should send blocked=true to gateway: %+v", fg.updated)
	}
	if _, err := mr.SetVirtualKeyEnabled(ctx, issued.VirtualKey.ID, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if len(fg.updated) != 2 || fg.updated[1].Blocked == nil || *fg.updated[1].Blocked {
		t.Fatalf("enable should send blocked=false to gateway: %+v", fg.updated)
	}
}
