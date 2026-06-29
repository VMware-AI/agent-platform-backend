package graph

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// ---- 用户与权限页 (P2) helpers: built-in roles surfaced as entities ----

// builtinRoleNamespace is the UUID v5 namespace used to derive deterministic
// UUIDs for built-in roles. UUIDs are stable across processes and restarts so
// the same role key always maps to the same id (frontend caches survive deploys).
var builtinRoleNamespace = uuid.MustParse("6e3b1c4a-7d8f-49b2-9a5e-1c2d3e4f5a6b")

// builtinRoleUUID returns the deterministic UUID for a built-in role key.
// Same roleKey → same id across all deployments.
func builtinRoleUUID(roleKey string) string {
	return uuid.NewSHA1(builtinRoleNamespace, []byte("builtin-role:"+roleKey)).String()
}

// builtinRoleByID resolves a built-in role id (UUID) back to (name, desc). Returns
// ("", "", false) if the id doesn't match any built-in role.
func builtinRoleByID(id string) (name, desc, roleKey string, ok bool) {
	for _, r := range builtinRoles {
		if builtinRoleUUID(r.id) == id {
			return r.name, r.desc, r.id, true
		}
	}
	return "", "", "", false
}

// builtinRoles are the assignable platform roles shown in the UI as entities.
// id here is the ROLE KEY (the stable string identifier used by @hasRole
// directives) — NOT the UUID surfaced in GraphQL responses. The UUID is derived
// on the fly via builtinRoleUUID(id) so the same role key always maps to the
// same UUID across deployments.
//
// Order: super-admin → user → read_only (admin first by privilege, then
// normal user, then read-only). The order is what the UI / GraphQL `roles`
// query returns, so it directly drives the "用户与权限" page's role tab.
//
// Note: the role set is now 3 (admin / user / read_only) — tenant-admin was
// removed in the 3-role refactor; observability was renamed to read_only.
var builtinRoles = []struct{ id, name, desc string }{
	{"admin", "超级管理员", "拥有平台所有功能的完整权限"},
	{"user", "普通用户", "自助创建与管理自己的智能体"},
	{"read_only", "只读用户", "大部分对象的只读权限"},
}

func builtinRole(id string) (name, desc string, ok bool) {
	for _, r := range builtinRoles {
		if r.id == id {
			return r.name, r.desc, true
		}
	}
	return "", "", false
}

// roleEntity builds the Role entity for a built-in role id (UUID). The id
// passed in must be a UUID — if the caller passes a legacy role key string,
// callers should look up the UUID via builtinRoleUUID first. The roles query
// itself calls builtinRoleUUID to produce the id, so this round-trip is lossless.
func roleEntity(id string, userCount int) *model.Role {
	name, desc, roleKey, ok := builtinRoleByID(id)
	if !ok {
		// Caller passed an unknown id (likely a stale client). Fall back to a
		// generic entity with just the UUID so the response shape is preserved;
		// the resolver logs this so we can spot a buggy caller.
		return &model.Role{ID: id, Name: "", Description: "", UserCount: userCount, BuiltIn: true}
	}
	return &model.Role{
		ID:          id,
		RoleKey:     roleKey,
		Name:        name,
		Description: desc,
		UserCount:   userCount,
		BuiltIn:     true,
	}
}

// roleEntities lists all built-in roles (with their UUIDs) as model entities.
func roleEntities() []*model.Role {
	out := make([]*model.Role, 0, len(builtinRoles))
	for _, r := range builtinRoles {
		out = append(out, &model.Role{
			ID:          builtinRoleUUID(r.id),
			RoleKey:     r.id,
			Name:        r.name,
			Description: r.desc,
			UserCount:   0, // overwritten by caller via roleUserCount
			BuiltIn:     true,
		})
	}
	return out
}

// roleUserCount counts users holding a built-in role. roleID is the UUID.
// After the 3-role refactor every caller is admin/user/read_only — none scope
// by tenant in the new model — so this just counts platform-wide.
func (r *Resolver) roleUserCount(ctx context.Context, roleID string) (int, error) {
	// Resolve UUID → role key for the SQL filter.
	_, _, roleKey, ok := builtinRoleByID(roleID)
	if !ok {
		return 0, nil
	}
	q := r.Ent.User.Query().Where(user.RoleEQ(user.Role(roleKey)))
	if d := tenantScopeFor(ctx); d.apply {
		if d.denyAll {
			return 0, nil
		}
		q = q.Where(user.TenantID(d.tenant))
	}
	return q.Count(ctx)
}

// matchRoleKeyword returns the ent role values whose id or 中文 label contains the
// keyword (for the user-list roleKeyword filter). Empty result → matches nothing.
func matchRoleKeyword(kw string) []user.Role {
	low := strings.ToLower(kw)
	var out []user.Role
	for _, br := range builtinRoles {
		if strings.Contains(strings.ToLower(br.id), low) || strings.Contains(br.name, kw) {
			out = append(out, user.Role(br.id))
		}
	}
	return out
}

// modelCustomRole maps an ent.Role to the GraphQL model, loading its permission keys.
func (r *Resolver) modelCustomRole(ctx context.Context, role *ent.Role) (*model.CustomRole, error) {
	perms, err := role.QueryPermissions().Order(orderByKey).All(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(perms))
	for _, p := range perms {
		keys = append(keys, p.Key)
	}
	return &model.CustomRole{
		ID: role.ID.String(), Name: role.Name, IsSystem: role.IsSystem,
		Permissions: keys, CreatedAt: role.CreatedAt,
	}, nil
}
