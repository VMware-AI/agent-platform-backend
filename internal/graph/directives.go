package graph

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// gqlRoleToEnt maps the GraphQL enum (tenant_admin) to the storage/auth string
// (tenant-admin). GraphQL enums cannot contain hyphens.
func gqlRoleToEnt(r model.Role) string {
	if r == model.RoleTenantAdmin {
		return "tenant-admin"
	}
	return string(r)
}

// entRoleToGQL is the inverse of gqlRoleToEnt.
func entRoleToGQL(s string) model.Role {
	if s == "tenant-admin" {
		return model.RoleTenantAdmin
	}
	return model.Role(s)
}

// HasRole implements the @hasRole directive: the caller's role must be one of
// the allowed roles (platform/tenant level, 判权三轨不交叉 — LLD-01 §4.1).
func HasRole(ctx context.Context, _ any, next graphql.Resolver, allowed []model.Role) (any, error) {
	u := auth.FromContext(ctx)
	if u == nil {
		return nil, gqlerror.Errorf("unauthenticated")
	}
	for _, r := range allowed {
		if string(u.Role) == gqlRoleToEnt(r) {
			return next(ctx)
		}
	}
	return nil, gqlerror.Errorf("forbidden: requires one of the allowed roles")
}

// HasPermission implements the @hasPermission directive via the role→permission
// matrix (LLD-01 §4.3).
func HasPermission(ctx context.Context, _ any, next graphql.Resolver, perm string) (any, error) {
	u := auth.FromContext(ctx)
	if u == nil {
		return nil, gqlerror.Errorf("unauthenticated")
	}
	if !u.Role.HasPermission(perm) {
		return nil, gqlerror.Errorf("forbidden: requires permission %q", perm)
	}
	return next(ctx)
}
