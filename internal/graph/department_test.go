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
