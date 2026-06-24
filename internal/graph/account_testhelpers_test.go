package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// mkUser seeds a user via the CreateUser resolver and returns the AccountUser
// (its ID is a string, like the old *model.User return, so existing call sites
// that read `.ID` keep working). For test setup that expects success.
func mkUser(t *testing.T, mr *mutationResolver, ctx context.Context, username, email string, role model.RoleName) *model.AccountUser {
	t.Helper()
	pw := "SeedPass1234!"
	pl, err := mr.CreateUser(ctx, model.CreateUserInput{
		Username:       username,
		DisplayName:    username,
		Email:          email,
		RoleID:         string(role),
		PasswordMode:   model.PasswordModeCustom,
		CustomPassword: &pw,
	})
	if err != nil {
		t.Fatalf("mkUser %s: %v", username, err)
	}
	return pl.User
}

// mkTenantUser seeds a user directly via ent in a specific tenant (the CreateUser
// resolver derives tenant from the caller, so tenant-isolation tests need this).
func mkTenantUser(t *testing.T, r *Resolver, username, email string, role model.RoleName, tenantID uuid.UUID) *ent.User {
	t.Helper()
	return r.Ent.User.Create().SetUsername(username).SetEmail(email).SetPasswordHash("x").
		SetRole(user.Role(gqlRoleToEnt(role))).SetIsActive(true).SetTenantID(tenantID).
		SaveX(context.Background())
}
