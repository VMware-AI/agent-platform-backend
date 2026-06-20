package graph

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// genFailGateway makes GenerateKey fail with a detail-bearing internal error.
type genFailGateway struct {
	fakeGateway
}

func (genFailGateway) GenerateKey(context.Context, gateway.GenerateKeyRequest) (*gateway.KeyResponse, error) {
	return nil, errors.New("dial litellm:4000: connection refused (master-key=sk-secret)")
}

// Regression guard: an internal gateway failure must surface as a PLAIN error so
// the global ErrorPresenter masks it. If a resolver wraps it in gqlerror.Errorf
// (which the presenter passes through verbatim), the internal detail leaks to the
// client. See internal/graph/errors.go.
func TestIssueVirtualKey_InternalErrorIsMaskable(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &genFailGateway{}
	ctx := context.Background()
	mr := &mutationResolver{r}

	u, err := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "maskuser", Email: "m@x.io", Password: "MaskUserPass1", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	_, err = mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{UserID: u.ID, Models: []string{"smart"}})
	if err == nil {
		t.Fatal("expected gateway failure")
	}
	var g *gqlerror.Error
	if errors.As(err, &g) {
		t.Fatalf("internal error must be a plain (maskable) error, not a pass-through gqlerror: %v", err)
	}
	// The detail must still be present in the (server-side) error for logging — it
	// is the presenter, not the resolver, that strips it before the wire.
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("wrapped error should retain detail for server logs, got %q", err.Error())
	}
}

// fakeGateway is an in-memory gateway.Client for tests.
type fakeGateway struct {
	generated    []gateway.GenerateKeyRequest
	deleted      []string
	deletedTeams []string
	listed       []gateway.KeyInfo
	regenerated  []string
}

func (f *fakeGateway) GenerateKey(_ context.Context, req gateway.GenerateKeyRequest) (*gateway.KeyResponse, error) {
	f.generated = append(f.generated, req)
	return &gateway.KeyResponse{Key: "sk-fake-123", Token: "tok-fake-123", UserID: req.UserID}, nil
}
func (f *fakeGateway) UpdateKey(context.Context, gateway.UpdateKeyRequest) error { return nil }
func (f *fakeGateway) DeleteKey(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}
func (f *fakeGateway) RegenerateKey(_ context.Context, key string) (*gateway.KeyResponse, error) {
	f.regenerated = append(f.regenerated, key)
	return &gateway.KeyResponse{Key: "sk-regenerated", Token: "tok-regenerated"}, nil
}
func (f *fakeGateway) CreateTeam(context.Context, gateway.TeamRequest) (*gateway.TeamResponse, error) {
	return &gateway.TeamResponse{}, nil
}
func (f *fakeGateway) DeleteTeam(_ context.Context, teamID string) error {
	f.deletedTeams = append(f.deletedTeams, teamID)
	return nil
}
func (f *fakeGateway) ListKeys(context.Context) ([]gateway.KeyInfo, error) {
	return f.listed, nil
}
func (f *fakeGateway) ListTeams(context.Context) ([]gateway.TeamInfo, error) {
	return nil, nil
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
	// The gateway's hashed token is persisted for reconciliation (root-cause fix).
	ikid := uuid.MustParse(issued.VirtualKey.ID)
	if tok := r.Ent.VirtualKey.GetX(ctx, ikid).LitellmToken; tok != "tok-fake-123" {
		t.Fatalf("litellm_token not persisted at issue: %q", tok)
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

// RegenerateVirtualKey rotates the secret at the gateway and persists the new key
// onto the SAME row (binding preserved), returning the new secret once.
func TestRegenerateVirtualKey(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	ctx := context.Background()
	mr := &mutationResolver{r}

	u, err := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "rotuser", Email: "rot@x.io", Password: "RotUserPass1", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	issued, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{UserID: u.ID, Models: []string{"smart"}})
	if err != nil {
		t.Fatalf("IssueVirtualKey: %v", err)
	}

	regen, err := mr.RegenerateVirtualKey(ctx, issued.VirtualKey.ID)
	if err != nil {
		t.Fatalf("RegenerateVirtualKey: %v", err)
	}
	if regen.Secret != "sk-regenerated" {
		t.Errorf("new secret = %q, want sk-regenerated", regen.Secret)
	}
	if len(fg.regenerated) != 1 || fg.regenerated[0] != "sk-fake-123" {
		t.Errorf("gateway regenerate called with %v, want [sk-fake-123]", fg.regenerated)
	}
	if regen.VirtualKey.ID != issued.VirtualKey.ID {
		t.Errorf("regenerate must reuse the same row: got %s want %s", regen.VirtualKey.ID, issued.VirtualKey.ID)
	}
	// The stored secret AND its reconciliation token are now the rotated ones.
	kid, _ := uuid.Parse(issued.VirtualKey.ID)
	row := r.Ent.VirtualKey.GetX(ctx, kid)
	if row.LitellmKey != "sk-regenerated" {
		t.Errorf("DB key not rotated: %q", row.LitellmKey)
	}
	if row.LitellmToken != "tok-regenerated" {
		t.Errorf("DB token not rotated: %q", row.LitellmToken)
	}
}

func TestRegenerateVirtualKey_RejectsRevoked(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	ctx := context.Background()
	mr := &mutationResolver{r}

	u, _ := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "revuser", Email: "rev@x.io", Password: "RevUserPass1", Role: model.RoleUser,
	})
	issued, _ := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{UserID: u.ID, Models: []string{"smart"}})
	if _, err := mr.RevokeVirtualKey(ctx, issued.VirtualKey.ID); err != nil {
		t.Fatalf("RevokeVirtualKey: %v", err)
	}
	if _, err := mr.RegenerateVirtualKey(ctx, issued.VirtualKey.ID); err == nil {
		t.Fatal("regenerating a revoked key must fail")
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
