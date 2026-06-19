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
