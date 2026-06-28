package auth

import "context"

// Role is the platform-level role (LLD-01 §4). Fine-grained Role/Permission
// tables exist in the data model for later; M1.0 enforces via this enum.
type Role string

const (
	RoleAdmin         Role = "admin"         // 超级管理员 — platform-wide
	RoleUser          Role = "user"          // 普通用户 — own resources
	RoleObservability Role = "observability" // 可观测性专员 — read-only observability
	RoleTenantAdmin   Role = "tenant-admin"  // tenant-wide (progressive)
)

// Permission keys (LLD-01 §4.3 权限矩阵).
const (
	PermAgentManage  = "agent:manage"
	PermKeyManage    = "key:manage"
	PermRouteManage  = "route:manage"
	PermAuditView    = "audit:view"
	PermMeteringView = "metering:view"
	PermUserManage   = "user:manage"
)

// rolePermissions encodes the M1.0 权限矩阵. "own" scoping (resources the user
// owns) is enforced at the resolver via owner_id, not by this table — this table
// is platform/tenant-level grants only (判权三轨不交叉, LLD-01 §4.1).
var rolePermissions = map[Role]map[string]bool{
	RoleAdmin: {
		PermAgentManage: true, PermKeyManage: true, PermRouteManage: true,
		PermAuditView: true, PermMeteringView: true, PermUserManage: true,
	},
	RoleObservability: {
		PermAuditView: true, PermMeteringView: true,
	},
	RoleUser: {
		// own-scoped perms are resolved per-resource, not granted platform-wide
	},
	RoleTenantAdmin: {
		PermAgentManage: true, PermKeyManage: true, PermRouteManage: true,
		PermAuditView: true, PermMeteringView: true, PermUserManage: true,
	},
}

// HasPermission reports whether the role holds a platform/tenant-level permission.
func (r Role) HasPermission(perm string) bool {
	return rolePermissions[r][perm]
}

// CurrentUser is the authenticated principal carried in the request context.
type CurrentUser struct {
	ID                 string
	Username           string
	Role               Role
	TenantID           string
	MustChangePassword bool
}

type ctxKey struct{}

// WithCurrentUser returns a context carrying the authenticated user.
func WithCurrentUser(ctx context.Context, u *CurrentUser) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// FromContext extracts the authenticated user, or nil if unauthenticated.
func FromContext(ctx context.Context) *CurrentUser {
	u, _ := ctx.Value(ctxKey{}).(*CurrentUser)
	return u
}
