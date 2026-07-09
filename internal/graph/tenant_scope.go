package graph

import (
	"context"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/membership"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// tenantScopeDecision describes how to confine a list query to the caller's
// tenant (C1). apply=false means no scoping (platform admin or any non
// tenant-admin). When apply=true: scope to tenant, or — for a tenant-admin with
// no valid tenant set (misconfigured) — denyAll: return nothing (fail closed).
// denyAll must NOT be implemented as "untenanted rows", which would leak every
// un-tenanted row (e.g. the platform admin user) to a tenant-less admin.
type tenantScopeDecision struct {
	apply   bool
	denyAll bool
	tenant  uuid.UUID
}

// tenantScopeFor computes the tenant-isolation decision for the caller. Each
// resolver applies it with its own ent predicate package (ent predicates are
// per-type, so the decision is shared but its application is not). Apply denyAll
// with a never-matching predicate (e.g. <entity>.IDEQ(uuid.Nil)).
//
// After the 3-role refactor (tenant-admin removed) every caller is
// admin/user/read_only — none of which scope by tenant in the new model —
// so this always returns the zero value (no scoping). Kept as a no-op so call
// sites compile and so a future tenant-scoped decision can plug in here
// without re-touching every resolver.
func tenantScopeFor(_ context.Context) tenantScopeDecision {
	return tenantScopeDecision{}
}

// writeAllowed reports whether the caller may mutate (update/delete) a row owned
// by rowTenant (LLD-10 §1.5 404 oracle). admin: any row. A tenant-scoped caller:
// only rows in their own tenant — NOT platform (NULL-tenant) rows, and never
// another tenant's. Callers translate a false result into notFoundErr so a
// cross-tenant row is indistinguishable from a missing one (no existence oracle).
func writeAllowed(ctx context.Context, rowTenant *uuid.UUID) bool {
	cu := auth.FromContext(ctx)
	if cu == nil {
		return false
	}
	if cu.Role == auth.RoleAdmin {
		return true
	}
	if cu.TenantID == "" {
		return false
	}
	tid, err := uuid.Parse(cu.TenantID)
	if err != nil {
		return false
	}
	return rowTenant != nil && *rowTenant == tid
}

// assertAgentConfigWritable enforces the tenant 404 oracle for AgentConfig
// update/delete: a cross-tenant or platform row reads as missing to a tenant
// caller (no existence oracle).
func (r *Resolver) assertAgentConfigWritable(ctx context.Context, id uuid.UUID) error {
	cfg, err := r.Ent.AgentConfig.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return notFoundErr("agent config")
		}
		return err
	}
	if !writeAllowed(ctx, cfg.TenantID) {
		return notFoundErr("agent config")
	}
	return nil
}

// resolveUserWriteTenant decides which tenant a created user lands in.
//
// After the 3-role refactor (tenant-admin removed) only admin can create
// users, so the tenant-admin escalation guard is gone — every input is
// honored as-is. Kept as a function so the call site reads naturally.
func (r *Resolver) resolveUserWriteTenant(_ context.Context, inputTenantID *string, _ model.RoleName) (*uuid.UUID, error) {
	if inputTenantID != nil {
		tid, err := uuid.Parse(*inputTenantID)
		if err != nil {
			return nil, gqlerror.Errorf("invalid tenantId")
		}
		return &tid, nil
	}
	return nil, nil
}

// assertRoleWritable enforces the tenant 404 oracle for Role mutations: a
// tenant-admin may modify only their own tenant's roles; system/other-tenant
// roles read as missing.
func (r *Resolver) assertRoleWritable(ctx context.Context, id uuid.UUID) error {
	role0, err := r.Ent.Role.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return notFoundErr("role")
		}
		return err
	}
	if !writeAllowed(ctx, role0.TenantID) {
		return notFoundErr("role")
	}
	return nil
}

// assertUserInCallerTenant enforces that a target user is in the caller's tenant
// (admin: any). Keeps a tenant-admin from acting on users outside their tenant
// (cross-tenant role assignment, etc.) — the user reads as missing otherwise.
func (r *Resolver) assertUserInCallerTenant(ctx context.Context, id uuid.UUID) error {
	u, err := r.Ent.User.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return notFoundErr("user")
		}
		return err
	}
	if !writeAllowed(ctx, u.TenantID) {
		return notFoundErr("user")
	}
	return nil
}

// assertAgentReferenceVisible enforces the agent-visibility rule (LLD-10 §1.3,
// the same three tracks as agentVisibilityPredicates) for a by-id agent
// REFERENCE supplied to a mutation (e.g. IssueVirtualKey's agentId): admin → any;
// tenant-admin → only an agent in their own tenant; regular user → only an agent
// they own. A cross-tenant / non-owned agent reads as missing (notFoundErr), so a
// caller cannot bind a key to — or probe the existence of — another tenant's
// agent (cross-tenant 1:1-slot DoS). Only constrains an authed caller; a no-auth
// ctx (resolver-level tests; @hasRole enforces auth in prod) and a not-yet-created
// soft reference (agent_id has no FK) fall through, mirroring the UserID guard.
func (r *Resolver) assertAgentReferenceVisible(ctx context.Context, id uuid.UUID) error {
	cu := auth.FromContext(ctx)
	if cu == nil {
		return nil
	}
	ag, err := r.Ent.Agent.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			// Binding to a non-existent agent id can't reach another tenant's slot;
			// leave the soft reference (no FK) intact, as the UserID guard does for a
			// missing user. The 1:1-slot check still runs on the real binding.
			return nil
		}
		return err
	}
	switch {
	case cu.Role == auth.RoleAdmin:
		return nil
	// (tenant-admin case removed in the 3-role refactor; read_only and user
	// fall through to the owner-only default branch.)
	default: // regular user: only their own agent (owner track)
		if ag.OwnerUserID.String() != cu.ID {
			return notFoundErr("agent")
		}
	}
	return nil
}

// assertDepartmentReferenceManageable enforces the department/tenant rule for a
// by-id team REFERENCE supplied to a mutation (IssueVirtualKey's teamId, which is
// interpreted as a department id — LLD-13 §3.3): the caller must be able to manage
// that department (platform/tenant admin of its tenant, or its dept-admin). A
// department in another tenant reads as missing, so a tenant-admin cannot mint a
// key under another tenant's litellm team on its gateway (budget misattribution).
// Only constrains an authed caller; a no-auth ctx (resolver-level tests) and a
// teamId that is not an existing department (free-form team / routes to default,
// no cross-tenant reach) fall through, mirroring the agent guard.
func (r *Resolver) assertDepartmentReferenceManageable(ctx context.Context, did uuid.UUID) error {
	if auth.FromContext(ctx) == nil {
		return nil
	}
	if _, err := r.Ent.Department.Get(ctx, did); err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return err
	}
	ok, err := r.canManageDepartment(ctx, did)
	if err != nil {
		return err
	}
	if !ok {
		return notFoundErr("department")
	}
	return nil
}

// writeTenant decides which tenant a newly created tenant-scoped resource should
// be stamped with (LLD-10 §1.6 STAMP). A caller with a tenant (tenant-admin or a
// regular user) always stamps their own tenant — they cannot create cross-tenant
// rows. A platform admin (no tenant) leaves it nil = an untenanted platform
// resource. This makes the data layer forward-correct ahead of the full
// multi-tenant read sweep: rows created now are already correctly tenanted
// rather than orphaned (NULL) forever.
func writeTenant(ctx context.Context) (*uuid.UUID, error) {
	cu := auth.FromContext(ctx)
	if cu == nil || cu.TenantID == "" {
		return nil, nil
	}
	id, err := uuid.Parse(cu.TenantID)
	if err != nil {
		return nil, gqlerror.Errorf("invalid caller tenant")
	}
	return &id, nil
}

// tenantMemberIDs returns the user ids that belong to a tenant. Used to scope
// B-class entities (VirtualKey/RequestLog/AuditLog) that have no tenant_id of
// their own and must be confined via their owning user (LLD-10 §1.1 B-class).
// An empty result naturally yields a fail-closed `... IN ()` (matches nothing).
func (r *Resolver) tenantMemberIDs(ctx context.Context, tenant uuid.UUID) ([]uuid.UUID, error) {
	return r.Ent.User.Query().Where(user.TenantID(tenant)).IDs(ctx)
}

// envScopeFor returns the environment to soft-filter a query by, and ok=true
// only when env_scope is enabled AND the request carried a valid X-Environment
// (LLD-10 §2.3). It is applied AFTER tenant_scope (hard boundary) as
// `env_id == env OR env_id IS NULL` (NULL = tenant-level, visible in any env).
// Disabled by default → ok=false → callers add no env predicate (no-op).
func (r *Resolver) envScopeFor(ctx context.Context) (uuid.UUID, bool) {
	if !r.EnvScopeEnabled {
		return uuid.Nil, false
	}
	return httpx.EnvironmentFromContext(ctx)
}

// contentScopeFor confines a browsable resource to the caller's tenant PLUS
// platform-global rows (tenant_id NULL) — the hybrid model for content like
// AgentConfig/Artifact (LLD-10 §9: built-in platform items are NULL/global
// read-only, customer items are tenant-stamped). Unlike tenantScopeFor (which
// only scopes tenant-admins, strictly), this applies to ANY caller that belongs
// to a tenant, so a regular user browses their tenant's + platform content.
// Platform admin (no tenant) → apply=false (sees all). A caller with a malformed
// tenant → denyAll (fail closed). Application differs from the strict block: the
// caller adds `Or(TenantID(t), TenantIDIsNil())`, not a bare `TenantID(t)`.
func contentScopeFor(ctx context.Context) tenantScopeDecision {
	cu := auth.FromContext(ctx)
	if cu == nil || cu.TenantID == "" {
		return tenantScopeDecision{}
	}
	if id, err := uuid.Parse(cu.TenantID); err == nil {
		return tenantScopeDecision{apply: true, tenant: id}
	}
	return tenantScopeDecision{apply: true, denyAll: true}
}

// getOwnedAgent loads an agent the caller is allowed to act on. To avoid an
// existence oracle, a missing agent and an agent owned by ANOTHER user return the
// SAME error (notFoundErr) — the caller cannot tell "does not exist" from "not
// yours". Admins bypass the owner check. Non-NotFound DB errors pass through (and
// are masked as INTERNAL by the presenter).
func (r *Resolver) getOwnedAgent(ctx context.Context, id uuid.UUID, cu *auth.CurrentUser) (*ent.Agent, error) {
	if cu == nil {
		return nil, notFoundErr("agent")
	}
	ag, err := r.Ent.Agent.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, notFoundErr("agent")
		}
		return nil, err
	}
	if cu.Role != auth.RoleAdmin && ag.OwnerUserID.String() != cu.ID {
		return nil, notFoundErr("agent")
	}
	return ag, nil
}

// canManageDepartment reports whether the caller may manage the given department's
// memberships: platform/tenant admins always, or a dept-admin of THAT department
// (delegation — LLD-01 §4.1, the @hasRole directive only covers platform/tenant).
//
// Lives here, not in *.resolvers.go: gqlgen regen relocates non-resolver funcs out
// of resolver files (and can mangle them), so all shared helpers stay in helpers.go.
func (r *Resolver) canManageDepartment(ctx context.Context, did uuid.UUID) (bool, error) {
	cu := auth.FromContext(ctx)
	if cu == nil {
		return false, nil
	}
	// Platform admin: every tenant.
	if cu.Role == auth.RoleAdmin {
		return true, nil
	}
	// Tenant admin: ONLY departments in their own tenant (C1 — without this a
	// tenant-admin could manage/read any tenant's departments).
	// (tenant-admin case removed in the 3-role refactor; falls through to dept-admin delegation)
	// Dept-admin delegation: a dept-admin membership in THIS department.
	uid, err := uuid.Parse(cu.ID)
	if err != nil {
		return false, nil
	}
	return r.Ent.Membership.Query().
		Where(
			membership.UserID(uid),
			membership.DepartmentID(did),
			membership.RoleEQ(membership.RoleDeptAdmin),
		).Exist(ctx)
}

// sameTenant reports whether two nullable tenant references denote the same tenant
// (both untenanted, or the same id). Keeps a membership from crossing tenants.
func sameTenant(a, b *uuid.UUID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// tenantMatches reports whether the caller's tenant equals the row's tenant. Both
// must be present — a tenant-less caller or untenanted row never matches (fail
// closed), so a misconfigured tenant-admin manages nothing across the boundary.
func tenantMatches(callerTenant string, rowTenant *uuid.UUID) bool {
	if callerTenant == "" || rowTenant == nil {
		return false
	}
	tid, err := uuid.Parse(callerTenant)
	if err != nil {
		return false
	}
	return tid == *rowTenant
}
