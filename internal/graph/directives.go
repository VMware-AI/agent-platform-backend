package graph

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"
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

// HasPermission implements the @hasPermission directive. It first checks the
// static role→permission matrix (fast path), then falls back to the union of
// the caller's custom-role permissions (user_roles → role_permissions), so
// admin-configured roles actually grant access (LLD-01 §4.2/§4.3).
func (r *Resolver) HasPermission(ctx context.Context, _ any, next graphql.Resolver, perm string) (any, error) {
	u := auth.FromContext(ctx)
	if u == nil {
		return nil, gqlerror.Errorf("unauthenticated")
	}
	if u.Role.HasPermission(perm) {
		return next(ctx)
	}
	set, err := r.effectivePerms(ctx, u.ID)
	if err != nil {
		return nil, gqlerror.Errorf("permission check failed")
	}
	if set[perm] {
		return next(ctx)
	}
	return nil, gqlerror.Errorf("forbidden: requires permission %q", perm)
}

// effectivePerms returns the union of permission keys granted to a user by their
// assigned custom roles. Memoized via permCache (short TTL + eager invalidation).
func (r *Resolver) effectivePerms(ctx context.Context, userID string) (map[string]bool, error) {
	if set, ok := r.permCache.get(userID); ok {
		return set, nil
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, err
	}
	u, err := r.Ent.User.Get(ctx, uid)
	if err != nil {
		return nil, err
	}
	perms, err := u.QueryRoles().QueryPermissions().All(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(perms))
	for _, p := range perms {
		set[p.Key] = true
	}
	r.permCache.put(userID, set)
	return set, nil
}
