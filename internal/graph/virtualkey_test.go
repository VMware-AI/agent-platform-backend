package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// fakeGateway is an in-memory gateway.Client for tests.
type fakeGateway struct {
	generated    []gateway.GenerateKeyRequest
	deleted      []string
	deletedTeams []string
}

func (f *fakeGateway) GenerateKey(_ context.Context, req gateway.GenerateKeyRequest) (*gateway.KeyResponse, error) {
	f.generated = append(f.generated, req)
	return &gateway.KeyResponse{Key: "sk-fake-123", UserID: req.UserID}, nil
}
func (f *fakeGateway) UpdateKey(context.Context, gateway.UpdateKeyRequest) error { return nil }
func (f *fakeGateway) DeleteKey(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}
func (f *fakeGateway) CreateTeam(context.Context, gateway.TeamRequest) (*gateway.TeamResponse, error) {
	return &gateway.TeamResponse{}, nil
}
func (f *fakeGateway) DeleteTeam(_ context.Context, teamID string) error {
	f.deletedTeams = append(f.deletedTeams, teamID)
	return nil
}

func TestIssueAndRevokeVirtualKey(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	// need a user to attach the key to
	u, err := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "keyuser", Email: "k@x.io", Password: "KeyUserPass1", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	budget := 50.0
	alias := "keyuser / coding"
	issued, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{
		UserID: u.ID, Models: []string{"smart"}, MaxBudget: &budget, Alias: &alias,
	})
	if err != nil {
		t.Fatalf("IssueVirtualKey: %v", err)
	}
	if issued.Secret != "sk-fake-123" {
		t.Fatalf("secret = %q", issued.Secret)
	}
	if len(fg.generated) != 1 || fg.generated[0].UserID != u.ID {
		t.Fatalf("gateway not called correctly: %+v", fg.generated)
	}

	// list does not expose the secret (model has no secret field by construction)
	keys, err := qr.VirtualKeys(ctx, &u.ID)
	if err != nil {
		t.Fatalf("VirtualKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].Status != model.VirtualKeyStatusActive {
		t.Fatalf("unexpected keys: %+v", keys)
	}

	// revoke calls the gateway and flips status
	ok, err := mr.RevokeVirtualKey(ctx, issued.VirtualKey.ID)
	if err != nil || !ok {
		t.Fatalf("RevokeVirtualKey: ok=%v err=%v", ok, err)
	}
	if len(fg.deleted) != 1 || fg.deleted[0] != "sk-fake-123" {
		t.Fatalf("gateway delete not called: %+v", fg.deleted)
	}
	keys, _ = qr.VirtualKeys(ctx, &u.ID)
	if keys[0].Status != model.VirtualKeyStatusRevoked {
		t.Fatalf("status should be revoked, got %v", keys[0].Status)
	}
}

// TestIssueVirtualKey_CompensatesOnDBFailure proves no orphan key is left at the
// gateway when the governance row fails to persist (C3). A canceled context lets
// the fake mint succeed but forces the ent Save to fail.
func TestIssueVirtualKey_CompensatesOnDBFailure(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	mr := &mutationResolver{r}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{
		UserID: "11111111-1111-1111-1111-111111111111", Models: []string{"smart"},
	})
	if err == nil {
		t.Fatal("expected error when the key row fails to persist")
	}
	if len(fg.deleted) != 1 || fg.deleted[0] != "sk-fake-123" {
		t.Fatalf("minted key not revoked at gateway (orphan): %+v", fg.deleted)
	}
}

func TestIssueVirtualKey_NoGateway(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r} // r.Gateway is nil
	_, err := mr.IssueVirtualKey(context.Background(), model.IssueVirtualKeyInput{UserID: "00000000-0000-0000-0000-000000000000"})
	if err == nil {
		t.Fatal("expected error when gateway not configured")
	}
}
