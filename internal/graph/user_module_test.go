package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// 模块① 用户与权限: roles lists the built-in assignable roles for the picker.
func TestRoles_BuiltinList(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	roles, err := qr.Roles(adminCtx())
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}
	if len(roles) != 4 {
		t.Fatalf("want 4 built-in roles, got %d", len(roles))
	}
	seen := map[model.Role]string{}
	for _, ri := range roles {
		if ri.Label == "" || ri.Description == "" {
			t.Errorf("role %s missing label/description", ri.Value)
		}
		seen[ri.Value] = ri.Label
	}
	for _, want := range []model.Role{model.RoleAdmin, model.RoleTenantAdmin, model.RoleUser, model.RoleObservability} {
		if _, ok := seen[want]; !ok {
			t.Errorf("missing role %s", want)
		}
	}
}

// users gains search/role/active filters + sort, keeping the {items,total} shape.
func TestUsers_FilterAndSort(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	bg := context.Background()
	qr := &queryResolver{r}

	mk := func(name, email string, role user.Role, active bool) {
		r.Ent.User.Create().SetUsername(name).SetEmail(email).SetPasswordHash("x").
			SetRole(role).SetIsActive(active).SaveX(bg)
	}
	mk("alice", "alice@corp.com", user.RoleUser, true)
	mk("bob", "bob@corp.com", user.RoleUser, false)
	mk("carol", "carol@other.com", user.RoleAdmin, true)

	// --- search (username/email substring) ---
	s := mustUsers(t, qr, ctx, &model.UserFilter{Search: ptr("corp.com")}, nil)
	if s.Total != 2 {
		t.Fatalf("search corp.com: total=%d, want 2", s.Total)
	}
	// --- role filter ---
	admins := mustUsers(t, qr, ctx, &model.UserFilter{Role: ptr(model.RoleAdmin)}, nil)
	if admins.Total != 1 || admins.Items[0].Username != "carol" {
		t.Fatalf("role filter: %+v", admins.Items)
	}
	// --- active filter ---
	inactive := mustUsers(t, qr, ctx, &model.UserFilter{Active: ptr(false)}, nil)
	if inactive.Total != 1 || inactive.Items[0].Username != "bob" {
		t.Fatalf("active=false: %+v", inactive.Items)
	}
	// --- sort by USERNAME asc / desc ---
	asc := mustUsers(t, qr, ctx, nil, &model.UserSort{Field: model.UserSortFieldUsername, Direction: model.SortDirectionAsc})
	if asc.Items[0].Username != "alice" || asc.Items[2].Username != "carol" {
		t.Fatalf("USERNAME asc: %s..%s", asc.Items[0].Username, asc.Items[2].Username)
	}
	desc := mustUsers(t, qr, ctx, nil, &model.UserSort{Field: model.UserSortFieldUsername, Direction: model.SortDirectionDesc})
	if desc.Items[0].Username != "carol" {
		t.Fatalf("USERNAME desc: first=%s, want carol", desc.Items[0].Username)
	}

	// --- shape unchanged: {items,total} with limit/offset paging ---
	page := mustUsers(t, qr, ctx, nil, nil)
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("default page: total=%d items=%d", page.Total, len(page.Items))
	}
}

func mustUsers(t *testing.T, qr *queryResolver, ctx context.Context, f *model.UserFilter, s *model.UserSort) *model.UserConnection {
	t.Helper()
	c, err := qr.Users(ctx, nil, f, s)
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	return c
}
