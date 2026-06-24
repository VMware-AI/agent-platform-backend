package graph

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

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

	if _, err := mr.CreateUser(adminCtx(), model.CreateUserInput{Username: "victim", DisplayName: "victim", Email: "v@x.io", RoleID: string(model.RoleNameUser), PasswordMode: model.PasswordModeCustom, CustomPassword: ptr("VictimPass12")}); err != nil {
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
	qr := &queryResolver{r}

	t1 := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	t2 := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	mkTenantUser(t, r, "u1", "u1@x.io", model.RoleNameUser, uuid.MustParse(t1))
	mkTenantUser(t, r, "u2", "u2@x.io", model.RoleNameUser, uuid.MustParse(t2))

	// platform admin sees both
	all, err := qr.Users(adminCtx(), nil, nil, nil)
	if err != nil || all.TotalCount != 2 || len(all.Nodes) != 2 {
		t.Fatalf("admin should see all users: total=%d nodes=%d err=%v", all.TotalCount, len(all.Nodes), err)
	}

	// tenant-admin of t1 sees only u1 (both total and nodes scoped)
	scoped, err := qr.Users(tenantAdminCtx("11111111-1111-1111-1111-111111111111", t1), nil, nil, nil)
	if err != nil {
		t.Fatalf("t1 admin users: %v", err)
	}
	if scoped.TotalCount != 1 || len(scoped.Nodes) != 1 || scoped.Nodes[0].Username != "u1" {
		t.Fatalf("tenant-admin must see only their tenant's users: total=%d nodes=%+v", scoped.TotalCount, scoped.Nodes)
	}
}

func TestCreateUserAndLogin(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	u, err := mr.CreateUser(ctx, model.CreateUserInput{Username: "alice", DisplayName: "alice", Email: "alice@x.io", RoleID: string(model.RoleNameUser), PasswordMode: model.PasswordModeCustom, CustomPassword: ptr("AlicePass123")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// CUSTOM password → no forced change, no generatedPassword.
	if u.User.Username != "alice" || u.GeneratedPassword != nil {
		t.Fatalf("unexpected user: %+v", u)
	}

	if _, err := mr.Login(ctx, model.LoginInput{Email: "alice", Password: "WrongPassword9"}); err == nil {
		t.Fatal("login with wrong password should fail")
	}
	ap, err := mr.Login(ctx, model.LoginInput{Email: "alice", Password: "AlicePass123"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if ap.User.Username != "alice" || ap.Token == "" {
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
	in := model.CreateUserInput{Username: "dup", DisplayName: "dup", Email: "dup@x.io", RoleID: string(model.RoleNameUser), PasswordMode: model.PasswordModeCustom, CustomPassword: ptr("DupPass12345")}
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
	u, err := mr.CreateUser(ctx, model.CreateUserInput{Username: "bob", DisplayName: "bob", Email: "bob@x.io", RoleID: string(model.RoleNameUser), PasswordMode: model.PasswordModeCustom, CustomPassword: ptr("BobPass12345")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	bobCtx := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: u.User.ID, Role: auth.RoleUser})
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
	if res, err := HasRole(adminCtx, nil, next, []model.RoleName{model.RoleNameAdmin}); err != nil || res != "ok" {
		t.Fatalf("admin should pass: res=%v err=%v", res, err)
	}

	userCtx := auth.WithCurrentUser(context.Background(), &auth.CurrentUser{Role: auth.RoleUser})
	if _, err := HasRole(userCtx, nil, next, []model.RoleName{model.RoleNameAdmin}); err == nil {
		t.Fatal("user should be denied admin-only field")
	}

	if _, err := HasRole(context.Background(), nil, next, []model.RoleName{model.RoleNameAdmin}); err == nil {
		t.Fatal("unauthenticated should be denied")
	}

	// tenant_admin (GraphQL) must map to tenant-admin (storage) and pass.
	taCtx := auth.WithCurrentUser(context.Background(), &auth.CurrentUser{Role: auth.RoleTenantAdmin})
	if _, err := HasRole(taCtx, nil, next, []model.RoleName{model.RoleNameTenantAdmin}); err != nil {
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
	u := mkUser(t, mr, admin, "carol", "carol@x.io", model.RoleNameUser)
	payload, err := mr.ResetUserPassword(admin, u.ID)
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if payload.GeneratedPassword == "" {
		t.Fatal("temp password must be returned")
	}
	// the temp password should log the user in (login is unauthenticated)
	if _, err := mr.Login(ctx, model.LoginInput{Email: "carol", Password: payload.GeneratedPassword}); err != nil {
		t.Fatalf("login with temp password: %v", err)
	}
}
