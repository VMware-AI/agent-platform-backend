package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
)

// 重蒙皮 P2 — roles surfaces the built-in roles as entities, each with its user
// count (for the 用户与权限 page's role tab).
func TestRoles_Entities(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	mkUser(t, mr, ctx, "u1", "u1@x.io", model.RoleNameUser)
	mkUser(t, mr, ctx, "u2", "u2@x.io", model.RoleNameUser)

	rc, err := qr.Roles(ctx, nil)
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}
	if rc.TotalCount != 3 || len(rc.Nodes) != 3 {
		t.Fatalf("want 3 built-in roles, got %d", rc.TotalCount)
	}
	var userRole *model.Role
	for i := range rc.Nodes {
		ri := &rc.Nodes[i]
		if ri.Name == "" || ri.Description == "" || !ri.BuiltIn {
			t.Errorf("role %s malformed: %+v", ri.ID, ri)
		}
		if ri.RoleKey == "user" {
			userRole = ri
		}
	}
	if userRole == nil || userRole.UserCount != 2 {
		t.Fatalf("user role userCount = %v, want 2", userRole)
	}
	// single-role query by UUID
	if one, err := qr.Role(ctx, builtinRoleUUID("admin")); err != nil || one == nil || one.RoleKey != "admin" {
		t.Fatalf("Role(admin UUID): %+v / %v", one, err)
	}
	if unknown, _ := qr.Role(ctx, "nope"); unknown != nil {
		t.Fatal("unknown role id should return nil")
	}
}

// users is a filtered/sorted/paged connection of AccountUser.
func TestUsers_Connection(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	mkUser(t, mr, ctx, "alice", "alice@corp.com", model.RoleNameUser)
	mkUser(t, mr, ctx, "bob", "bob@corp.com", model.RoleNameUser)
	mkUser(t, mr, ctx, "carol", "carol@other.com", model.RoleNameAdmin)

	// username keyword
	a := mustConn(t, qr, ctx, &model.UserFilter{UsernameKeyword: ptr("ali")}, nil)
	if a.TotalCount != 1 || a.Nodes[0].Username != "alice" {
		t.Fatalf("usernameKeyword: %+v", a.Nodes)
	}
	// email keyword
	c := mustConn(t, qr, ctx, &model.UserFilter{EmailKeyword: ptr("corp.com")}, nil)
	if c.TotalCount != 2 {
		t.Fatalf("emailKeyword corp.com: %d, want 2", c.TotalCount)
	}
	// roleId filter (UUID, looked up from roles query)
	admins := mustConn(t, qr, ctx, &model.UserFilter{RoleID: ptr(builtinRoleUUID("admin"))}, nil)
	if admins.TotalCount != 1 || admins.Nodes[0].Username != "carol" {
		t.Fatalf("roleId filter: %+v", admins.Nodes)
	}
	// role on a node is the entity ref (id = UUID, matches AccountRoleRef)
	if admins.Nodes[0].Role == nil || admins.Nodes[0].Role.ID != builtinRoleUUID("admin") {
		t.Fatalf("AccountUser.role ref wrong: %+v", admins.Nodes[0].Role)
	}
	// sort by USERNAME asc + connection shape
	asc := mustConn(t, qr, ctx, nil, &model.UserSort{Field: model.UserSortFieldUsername, Direction: model.SortDirectionAsc})
	if asc.TotalCount != 3 || asc.Nodes[0].Username != "alice" || asc.Nodes[2].Username != "carol" {
		t.Fatalf("sort/shape: total=%d %s..%s", asc.TotalCount, asc.Nodes[0].Username, asc.Nodes[2].Username)
	}
	if asc.PageInfo == nil || asc.PageInfo.TotalPages != 1 {
		t.Fatalf("pageInfo wrong: %+v", asc.PageInfo)
	}
}

// CreateUser: AUTO generates+returns a temp password; CUSTOM uses the supplied
// password (and requires it).
func TestCreateUser_Modes(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	auto, err := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "auto", DisplayName: "Auto", Email: "auto@x.io",
		RoleID: builtinRoleUUID(string(model.RoleNameUser)), PasswordMode: model.PasswordModeAuto,
	})
	if err != nil {
		t.Fatalf("AUTO create: %v", err)
	}
	if auto.GeneratedPassword == nil || *auto.GeneratedPassword == "" {
		t.Fatal("AUTO must return a generated password")
	}
	// CUSTOM with no password → rejected
	if _, err := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "c", DisplayName: "c", Email: "c@x.io", RoleID: builtinRoleUUID(string(model.RoleNameUser)), PasswordMode: model.PasswordModeCustom,
	}); err == nil {
		t.Fatal("CUSTOM without customPassword should be rejected")
	}
}

// A role change revokes the user's sessions (role is cached in the session — a
// demotion must take effect immediately). An email-only change does not.
func TestUpdateUser_RoleChangeRevokesSessions(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	u := mkUser(t, mr, ctx, "victim", "v@x.io", model.RoleNameAdmin)
	sid, _ := r.Sessions.Create(session.Data{UserID: u.ID, Role: "admin", ExpiresAt: time.Now().Add(time.Hour)})

	demote := builtinRoleUUID(string(model.RoleNameUser))
	if _, err := mr.UpdateUser(ctx, u.ID, model.UpdateUserInput{RoleID: &demote}); err != nil {
		t.Fatalf("UpdateUser role: %v", err)
	}
	if _, err := r.Sessions.Get(sid); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("role change must revoke the session, got %v", err)
	}

	sid2, _ := r.Sessions.Create(session.Data{UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	email := "new@x.io"
	if _, err := mr.UpdateUser(ctx, u.ID, model.UpdateUserInput{Email: &email}); err != nil {
		t.Fatalf("UpdateUser email: %v", err)
	}
	if _, err := r.Sessions.Get(sid2); err != nil {
		t.Fatalf("email-only change should NOT revoke, got %v", err)
	}
}

// toggleUserEnabled flips enabled and (when disabling) revokes sessions;
// assignUsersToRole sets the role for many users; userExists dedupes.
func TestToggleAssignExists(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	a := mkUser(t, mr, ctx, "ann", "ann@x.io", model.RoleNameUser)
	b := mkUser(t, mr, ctx, "ben", "ben@x.io", model.RoleNameUser)

	// disable → enabled=false + sessions revoked
	sid, _ := r.Sessions.Create(session.Data{UserID: a.ID, ExpiresAt: time.Now().Add(time.Hour)})
	tg, err := mr.ToggleUserEnabled(ctx, a.ID)
	if err != nil || tg.User.Enabled {
		t.Fatalf("toggle disable: enabled=%v err=%v", tg.User.Enabled, err)
	}
	if _, err := r.Sessions.Get(sid); !errors.Is(err, session.ErrNotFound) {
		t.Fatal("disable must revoke sessions")
	}

	// assign both to read_only
	res, err := mr.AssignUsersToRole(ctx, model.AssignUsersToRoleInput{RoleID: builtinRoleUUID(string(model.RoleNameReadOnly)), UserIds: []string{a.ID, b.ID}})
	if err != nil || res.AssignedCount != 2 || res.Role.RoleKey != string(model.RoleNameReadOnly) {
		t.Fatalf("assign: %+v / %v", res, err)
	}
	if res.Role.UserCount != 2 {
		t.Fatalf("role userCount after assign = %d, want 2", res.Role.UserCount)
	}

	// userExists
	if ok, _ := qr.UserExists(ctx, ptr("ann"), nil); !ok {
		t.Fatal("ann should exist")
	}
	if ok, _ := qr.UserExists(ctx, ptr("nobody"), ptr("nobody@x.io")); ok {
		t.Fatal("nobody should not exist")
	}
}

func mustConn(t *testing.T, qr *queryResolver, ctx context.Context, f *model.UserFilter, s *model.UserSort) *model.UserConnection {
	t.Helper()
	c, err := qr.Users(ctx, f, nil, s)
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	return c
}
