package graph

import (
	"context"
	"testing"
	"time"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/ratelimit"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

func newTestResolver(t *testing.T) (*Resolver, func()) {
	t.Helper()
	client, err := store.Open(context.Background(), "", true) // in-memory sqlite
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	r := &Resolver{Ent: client, Sessions: session.NewMemoryStore(), SessionTTL: time.Hour}
	return r, func() { _ = client.Close() }
}

func TestLogin_RateLimited(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.LoginLimiter = ratelimit.NewMemory(3, time.Hour)
	ctx := context.Background()
	mr := &mutationResolver{r}

	if _, err := mr.CreateUser(adminCtx(), model.CreateUserInput{
		Username: "victim", Email: "v@x.io", Password: "VictimPass12", Role: model.RoleUser,
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := mr.Login(ctx, model.LoginInput{Email: "victim", Password: "wrongpass"}); err == nil {
			t.Fatalf("attempt %d: expected invalid credentials", i)
		}
	}
	// 4th attempt is locked out even with the CORRECT password.
	if _, err := mr.Login(ctx, model.LoginInput{Email: "victim", Password: "VictimPass12"}); err == nil {
		t.Fatal("expected lockout after 3 failed attempts")
	}
}

// Tenant isolation (C1): the paginated users query confines a tenant-admin to
// their own tenant (count AND page), while a platform admin sees everyone.
func TestUsers_TenantScoped(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	admin := adminCtx()

	t1 := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	t2 := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	if _, err := mr.CreateUser(admin, model.CreateUserInput{
		Username: "u1", Email: "u1@x.io", Password: "U1Password123", Role: model.RoleUser, TenantID: &t1,
	}); err != nil {
		t.Fatalf("create u1: %v", err)
	}
	if _, err := mr.CreateUser(admin, model.CreateUserInput{
		Username: "u2", Email: "u2@x.io", Password: "U2Password123", Role: model.RoleUser, TenantID: &t2,
	}); err != nil {
		t.Fatalf("create u2: %v", err)
	}

	// platform admin sees both
	all, err := qr.Users(admin, nil, nil, nil)
	if err != nil || all.Total != 2 || len(all.Items) != 2 {
		t.Fatalf("admin should see all users: total=%d items=%d err=%v", all.Total, len(all.Items), err)
	}

	// tenant-admin of t1 sees only u1 (both total and items scoped)
	scoped, err := qr.Users(tenantAdminCtx("11111111-1111-1111-1111-111111111111", t1), nil, nil, nil)
	if err != nil {
		t.Fatalf("t1 admin users: %v", err)
	}
	if scoped.Total != 1 || len(scoped.Items) != 1 || scoped.Items[0].Username != "u1" {
		t.Fatalf("tenant-admin must see only their tenant's users: total=%d items=%+v", scoped.Total, scoped.Items)
	}
}

func TestCreateUserAndLogin(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	u, err := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "alice", Email: "alice@x.io", Password: "AlicePass123", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.Username != "alice" || !u.MustChangePassword {
		t.Fatalf("unexpected user: %+v", u)
	}

	if _, err := mr.Login(ctx, model.LoginInput{Email: "alice", Password: "WrongPassword9"}); err == nil {
		t.Fatal("login with wrong password should fail")
	}
	ap, err := mr.Login(ctx, model.LoginInput{Email: "alice", Password: "AlicePass123"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if ap.User.Username != "alice" || !ap.MustChangePassword {
		t.Fatalf("unexpected auth payload: %+v", ap)
	}

	// login + create should have produced audit rows
	conn, err := (&queryResolver{r}).AuditLogs(ctx, nil, nil)
	if err != nil {
		t.Fatalf("AuditLogs: %v", err)
	}
	if conn.Total < 2 {
		t.Fatalf("expected >=2 audit entries, got %d", conn.Total)
	}
}

func TestDuplicateUsernameRejected(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	in := model.CreateUserInput{Username: "dup", Email: "dup@x.io", Password: "DupPass12345", Role: model.RoleUser}
	if _, err := mr.CreateUser(ctx, in); err != nil {
		t.Fatalf("first create: %v", err)
	}
	in.Email = "dup2@x.io"
	if _, err := mr.CreateUser(ctx, in); err == nil {
		t.Fatal("duplicate username should be rejected")
	}
}

func TestChangePasswordFlow(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	u, err := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "bob", Email: "bob@x.io", Password: "BobPass12345", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	bobCtx := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: u.ID, Role: auth.RoleUser})
	ok, err := mr.ChangePassword(bobCtx, "BobPass12345", "NewBobPass678")
	if err != nil || !ok {
		t.Fatalf("ChangePassword: ok=%v err=%v", ok, err)
	}
	if _, err := mr.Login(ctx, model.LoginInput{Email: "bob", Password: "BobPass12345"}); err == nil {
		t.Fatal("old password should no longer work")
	}
	ap, err := mr.Login(ctx, model.LoginInput{Email: "bob", Password: "NewBobPass678"})
	if err != nil {
		t.Fatalf("Login with new password: %v", err)
	}
	if ap.MustChangePassword {
		t.Fatal("must_change_password should be false after change")
	}
}

func TestHasRoleDirective(t *testing.T) {
	next := func(context.Context) (any, error) { return "ok", nil }

	adminCtx := auth.WithCurrentUser(context.Background(), &auth.CurrentUser{Role: auth.RoleAdmin})
	if res, err := HasRole(adminCtx, nil, next, []model.Role{model.RoleAdmin}); err != nil || res != "ok" {
		t.Fatalf("admin should pass: res=%v err=%v", res, err)
	}

	userCtx := auth.WithCurrentUser(context.Background(), &auth.CurrentUser{Role: auth.RoleUser})
	if _, err := HasRole(userCtx, nil, next, []model.Role{model.RoleAdmin}); err == nil {
		t.Fatal("user should be denied admin-only field")
	}

	if _, err := HasRole(context.Background(), nil, next, []model.Role{model.RoleAdmin}); err == nil {
		t.Fatal("unauthenticated should be denied")
	}

	// tenant_admin (GraphQL) must map to tenant-admin (storage) and pass.
	taCtx := auth.WithCurrentUser(context.Background(), &auth.CurrentUser{Role: auth.RoleTenantAdmin})
	if _, err := HasRole(taCtx, nil, next, []model.Role{model.RoleTenantAdmin}); err != nil {
		t.Fatalf("tenant-admin should pass: %v", err)
	}
}

func TestHasPermissionDirective(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	next := func(context.Context) (any, error) { return "ok", nil }
	obsCtx := auth.WithCurrentUser(context.Background(), &auth.CurrentUser{Role: auth.RoleObservability})
	// Static fast path grants audit:view to observability (no custom role needed).
	if _, err := r.HasPermission(obsCtx, nil, next, auth.PermAuditView); err != nil {
		t.Fatalf("observability should have audit:view: %v", err)
	}
	// user:manage is neither in the static matrix nor any custom role → denied.
	if _, err := r.HasPermission(obsCtx, nil, next, auth.PermUserManage); err == nil {
		t.Fatal("observability must not have user:manage")
	}
}

func TestResetPassword(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	// CreateUser/ResetPassword are admin-gated; call them with an admin context
	// (the directive guarantees a caller in prod — resolvers now enforce tenant
	// scope on the caller).
	admin := adminCtx()
	u, _ := mr.CreateUser(admin, model.CreateUserInput{
		Username: "carol", Email: "carol@x.io", Password: "CarolPass123", Role: model.RoleUser,
	})
	payload, err := mr.ResetPassword(admin, u.ID)
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if payload.TempPassword == "" {
		t.Fatal("temp password must be returned")
	}
	// the temp password should log the user in (login is unauthenticated)
	if _, err := mr.Login(ctx, model.LoginInput{Email: "carol", Password: payload.TempPassword}); err != nil {
		t.Fatalf("login with temp password: %v", err)
	}
}
