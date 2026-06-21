package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// teamFailGateway fails CreateTeam, to exercise no-orphan rollback.
type teamFailGateway struct{ fakeGateway }

func (teamFailGateway) CreateTeam(context.Context, gateway.TeamRequest) (*gateway.TeamResponse, error) {
	return nil, errors.New("team boom")
}

// teamDeleteFailGateway fails DeleteTeam, to assert deleteDepartment keeps the
// row (retryable, no half-delete) when the gateway is unreachable.
type teamDeleteFailGateway struct{ fakeGateway }

func (teamDeleteFailGateway) DeleteTeam(context.Context, string) error {
	return errors.New("delete team boom")
}

func TestDeleteDepartment_DeletesLitellmTeam(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	dept, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "research"})
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	ok, err := mr.DeleteDepartment(ctx, dept.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteDepartment: ok=%v err=%v", ok, err)
	}
	if len(fg.deletedTeams) != 1 || fg.deletedTeams[0] != dept.ID {
		t.Fatalf("litellm team not deleted (orphan): %+v", fg.deletedTeams)
	}
	if depts, _ := qr.Departments(ctx); len(depts) != 0 {
		t.Fatalf("department row not deleted: %d", len(depts))
	}
}

func TestDeleteDepartment_GatewayFailureKeepsDept(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	dept, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "keepme"})
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	// Gateway team-delete now fails: the DB row must survive so the op is
	// retryable — no silent orphan, no half-delete.
	r.Gateway = &teamDeleteFailGateway{}
	if _, err := mr.DeleteDepartment(ctx, dept.ID); err == nil {
		t.Fatal("expected error when litellm team delete fails")
	}
	if depts, _ := qr.Departments(ctx); len(depts) != 1 {
		t.Fatalf("department should be kept when team delete fails: %d", len(depts))
	}
}

func TestDepartmentAndMembership(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	budget := 500.0
	dept, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "research", MaxBudget: &budget})
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	if dept.LitellmTeamID == nil || *dept.LitellmTeamID != dept.ID {
		t.Fatalf("litellm team id not set to dept id: %+v", dept.LitellmTeamID)
	}

	uid := "11111111-1111-1111-1111-111111111111"
	da := model.MembershipRoleDeptAdmin
	m, err := mr.AddMembership(ctx, uid, dept.ID, &da)
	if err != nil || m.Role != model.MembershipRoleDeptAdmin {
		t.Fatalf("AddMembership: %v role=%v", err, m.Role)
	}
	// adding the same user again updates the role (no duplicate)
	u := model.MembershipRoleUser
	if _, err := mr.AddMembership(ctx, uid, dept.ID, &u); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	members, _ := qr.DepartmentMembers(ctx, dept.ID)
	if len(members) != 1 || members[0].Role != model.MembershipRoleUser {
		t.Fatalf("membership not updated/deduped: %+v", members)
	}

	if ok, err := mr.RemoveMembership(ctx, uid, dept.ID); err != nil || !ok {
		t.Fatalf("RemoveMembership: %v", err)
	}
	if mm, _ := qr.DepartmentMembers(ctx, dept.ID); len(mm) != 0 {
		t.Fatalf("member not removed: %d", len(mm))
	}
}

// Tenant isolation (C1): a tenant-admin's departments query is confined to their
// own tenant; a platform admin sees every tenant's.
func TestDepartments_TenantScoped(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	admin := adminCtx()

	t1 := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	t2 := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	if _, err := mr.CreateDepartment(admin, model.CreateDepartmentInput{Name: "t1-dept", TenantID: &t1}); err != nil {
		t.Fatalf("create t1 dept: %v", err)
	}
	if _, err := mr.CreateDepartment(admin, model.CreateDepartmentInput{Name: "t2-dept", TenantID: &t2}); err != nil {
		t.Fatalf("create t2 dept: %v", err)
	}

	// platform admin sees both tenants' departments
	if all, err := qr.Departments(admin); err != nil || len(all) != 2 {
		t.Fatalf("platform admin should see all departments: n=%d err=%v", len(all), err)
	}

	// tenant-admin of t1 sees only t1's
	t1Admin := tenantAdminCtx("11111111-1111-1111-1111-111111111111", t1)
	scoped, err := qr.Departments(t1Admin)
	if err != nil {
		t.Fatalf("t1 admin departments: %v", err)
	}
	if len(scoped) != 1 || scoped[0].Name != "t1-dept" {
		t.Fatalf("tenant-admin must see only their tenant's departments, got %+v", scoped)
	}
}

// C1: a tenant-admin may manage departments in their OWN tenant but not another
// tenant's (canManageDepartment must compare dept.TenantID, not blanket-allow).
func TestMembership_TenantAdminConfinedToTenant(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	admin := adminCtx()

	t1 := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	t2 := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	d1, err := mr.CreateDepartment(admin, model.CreateDepartmentInput{Name: "t1-dept", TenantID: &t1})
	if err != nil {
		t.Fatalf("create d1: %v", err)
	}
	d2, err := mr.CreateDepartment(admin, model.CreateDepartmentInput{Name: "t2-dept", TenantID: &t2})
	if err != nil {
		t.Fatalf("create d2: %v", err)
	}

	t1User, _ := mr.CreateUser(admin, model.CreateUserInput{Username: "t1u", Email: "t1u@x.io", Password: "T1UserPass12", Role: model.RoleUser, TenantID: &t1})
	t2User, _ := mr.CreateUser(admin, model.CreateUserInput{Username: "t2u", Email: "t2u@x.io", Password: "T2UserPass12", Role: model.RoleUser, TenantID: &t2})
	t1Admin := tenantAdminCtx("11111111-1111-1111-1111-111111111111", t1)

	// tenant-admin of t1 manages t1's department with a t1 user
	if _, err := mr.AddMembership(t1Admin, t1User.ID, d1.ID, nil); err != nil {
		t.Fatalf("t1 admin should add a t1 user to a t1 dept: %v", err)
	}
	// ...but may NOT pull a t2 user into the t1 dept (cross-tenant member)
	if _, err := mr.AddMembership(t1Admin, t2User.ID, d1.ID, nil); err == nil {
		t.Fatal("tenant-admin must NOT add a foreign-tenant user")
	}
	// ...nor manage t2's department at all
	if _, err := mr.AddMembership(t1Admin, t1User.ID, d2.ID, nil); err == nil {
		t.Fatal("tenant-admin must NOT manage another tenant's department")
	}
	if _, err := qr.DepartmentMembers(t1Admin, d2.ID); err == nil {
		t.Fatal("tenant-admin must NOT read another tenant's department members")
	}

	// a tenant-admin with no tenant set manages nothing (fail closed)
	noTenant := tenantAdminCtx("33333333-3333-3333-3333-333333333333", "")
	if _, err := mr.AddMembership(noTenant, t1User.ID, d1.ID, nil); err == nil {
		t.Fatal("tenant-admin without a tenant must manage nothing")
	}
}

// A dept-admin may manage memberships of their OWN department, but not another's;
// a plain member/non-member may not (LLD-01 §4.1 部门委派).
func TestMembership_DeptAdminDelegation(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	admin := adminCtx()

	d1, err := mr.CreateDepartment(admin, model.CreateDepartmentInput{Name: "d1"})
	if err != nil {
		t.Fatalf("create d1: %v", err)
	}
	d2, err := mr.CreateDepartment(admin, model.CreateDepartmentInput{Name: "d2"})
	if err != nil {
		t.Fatalf("create d2: %v", err)
	}

	alice := "11111111-1111-1111-1111-111111111111"
	da := model.MembershipRoleDeptAdmin
	if _, err := mr.AddMembership(admin, alice, d1.ID, &da); err != nil {
		t.Fatalf("seed dept-admin: %v", err)
	}
	aliceCtx := userCtx(alice, "user") // platform role is plain user; delegation is via membership
	// d1 is untenanted, so an untenanted user is in-tenant for the same-tenant rule.
	bob, _ := mr.CreateUser(admin, model.CreateUserInput{Username: "bob", Email: "bob@x.io", Password: "BobPass123456", Role: model.RoleUser})

	// alice manages her own department
	if _, err := mr.AddMembership(aliceCtx, bob.ID, d1.ID, nil); err != nil {
		t.Fatalf("dept-admin should manage own dept: %v", err)
	}
	if _, err := qr.DepartmentMembers(aliceCtx, d1.ID); err != nil {
		t.Fatalf("dept-admin should read own dept members: %v", err)
	}
	// alice cannot touch a different department
	if _, err := mr.AddMembership(aliceCtx, bob.ID, d2.ID, nil); err == nil {
		t.Fatal("dept-admin must not manage a different department")
	}
	// a non-member plain user cannot manage d1
	carol := userCtx("33333333-3333-3333-3333-333333333333", "user")
	if _, err := mr.AddMembership(carol, bob.ID, d1.ID, nil); err == nil {
		t.Fatal("non-member must not manage department")
	}
	if _, err := mr.RemoveMembership(carol, bob.ID, d1.ID); err == nil {
		t.Fatal("non-member must not remove membership")
	}
}

func TestCreateDepartment_NoOrphanOnTeamFailure(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &teamFailGateway{}
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	if _, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "doomed"}); err == nil {
		t.Fatal("expected error when litellm team creation fails")
	}
	// rolled back: no department row left
	depts, _ := qr.Departments(ctx)
	if len(depts) != 0 {
		t.Fatalf("orphan department left after team failure: %d", len(depts))
	}
}
