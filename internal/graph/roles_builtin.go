package graph

import (
	"context"
	"strings"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// ---- 用户与权限页 (P2) helpers: built-in roles surfaced as entities ----

// builtinRoles are the assignable platform roles shown in the UI as entities.
// id = the RoleName GraphQL enum value (underscored, e.g. tenant_admin). 本期不
// 支持自定义角色 (LLD-01 §4.2 enforcement 渐进).
var builtinRoles = []struct{ id, name, desc string }{
	{"admin", "管理员", "平台全局管理权限"},
	{"tenant_admin", "租户管理员", "管理本租户的用户与资源"},
	{"user", "普通用户", "自助创建与管理自己的智能体"},
	{"observability", "可观测", "查看计量、日志与审计(只读)"},
}

func builtinRole(id string) (name, desc string, ok bool) {
	for _, r := range builtinRoles {
		if r.id == id {
			return r.name, r.desc, true
		}
	}
	return "", "", false
}

// roleEntity builds the Role entity for a built-in role id.
func roleEntity(id string, userCount int) *model.Role {
	name, desc, _ := builtinRole(id)
	return &model.Role{ID: id, Name: name, Description: desc, UserCount: userCount, BuiltIn: true}
}

// roleUserCount counts users holding a built-in role, within the caller's tenant
// scope (tenant-admin sees only their tenant; admin sees all).
func (r *Resolver) roleUserCount(ctx context.Context, roleID string) (int, error) {
	q := r.Ent.User.Query().Where(user.RoleEQ(user.Role(gqlRoleToEnt(model.RoleName(roleID)))))
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
			out = append(out, user.Role(gqlRoleToEnt(model.RoleName(br.id))))
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
