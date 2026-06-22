package graph

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"entgo.io/ent/dialect/sql"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/agenttemplate"
	"github.com/VMware-AI/agent-platform-backend/ent/auditlog"
	"github.com/VMware-AI/agent-platform-backend/ent/membership"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/catalog"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// orderNewest / orderByKey give list queries a stable TOTAL sort so Limit/Offset
// pagination is deterministic (no duplicate/dropped rows across pages) and logs
// read newest-first. The id tiebreaker matters: created_at alone is not unique
// (rapid inserts share a timestamp), so without it pages could overlap. orderByKey
// is for entities without created_at (Permission); key is unique.
var (
	orderNewest = ent.Desc("created_at", "id")
	orderByKey  = ent.Asc("key")
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
func tenantScopeFor(ctx context.Context) tenantScopeDecision {
	cu := auth.FromContext(ctx)
	if cu == nil || cu.Role != auth.RoleTenantAdmin {
		return tenantScopeDecision{}
	}
	if id, err := uuid.Parse(cu.TenantID); err == nil {
		return tenantScopeDecision{apply: true, tenant: id}
	}
	return tenantScopeDecision{apply: true, denyAll: true}
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

// resolveUserWriteTenant decides which tenant a created user lands in and guards
// against escalation (LLD-10 §1.5): a tenant-admin is confined to their own
// tenant and may not mint admin/tenant-admin users; a platform admin may target
// any tenant via explicit input (or none = untenanted platform user).
func (r *Resolver) resolveUserWriteTenant(ctx context.Context, inputTenantID *string, role model.Role) (*uuid.UUID, error) {
	cu := auth.FromContext(ctx)
	if cu != nil && cu.Role == auth.RoleTenantAdmin {
		tid, err := uuid.Parse(cu.TenantID)
		if err != nil {
			return nil, gqlerror.Errorf("forbidden")
		}
		if inputTenantID != nil && *inputTenantID != tid.String() {
			return nil, gqlerror.Errorf("forbidden: cannot create users in another tenant")
		}
		if role == model.RoleAdmin || role == model.RoleTenantAdmin {
			return nil, gqlerror.Errorf("forbidden: tenant-admin cannot grant admin roles")
		}
		return &tid, nil
	}
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

// actorID returns the current user's id, or "" if unauthenticated.
func actorID(cu *auth.CurrentUser) string {
	if cu != nil {
		return cu.ID
	}
	return ""
}

// pageBounds normalizes a PageInput into a safe limit/offset.
func pageBounds(page *model.PageInput) (limit, offset int) {
	limit, offset = 50, 0
	if page != nil {
		if page.Limit != nil {
			limit = *page.Limit
		}
		if page.Offset != nil {
			offset = *page.Offset
		}
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// toModelUser maps an ent.User to the GraphQL model (omits password_hash).
func toModelUser(u *ent.User) *model.User {
	m := &model.User{
		ID:                 u.ID.String(),
		Username:           u.Username,
		Email:              u.Email,
		Role:               entRoleToGQL(string(u.Role)),
		MustChangePassword: u.MustChangePassword,
		IsActive:           u.IsActive,
		CreatedAt:          u.CreatedAt,
	}
	if u.TenantID != nil {
		s := u.TenantID.String()
		m.TenantID = &s
	}
	if u.LastLoginAt != nil {
		t := *u.LastLoginAt
		m.LastLoginAt = &t
	}
	return m
}

// toModelResourcePool maps an ent.ResourcePool to the GraphQL model
// (secret_ref is intentionally not exposed).
func toModelResourcePool(p *ent.ResourcePool) *model.ResourcePool {
	return &model.ResourcePool{
		ID:              p.ID.String(),
		Name:            p.Name,
		Kind:            string(p.Kind),
		Endpoint:        p.Endpoint,
		Status:          model.ResourcePoolStatus(string(p.Status)),
		DatacenterCount: p.DatacenterCount,
		ClusterCount:    p.ClusterCount,
		HostCount:       p.HostCount,
		VMCount:         p.VMCount,
		CreatedAt:       p.CreatedAt,
	}
}

// toModelVirtualKey maps an ent.VirtualKey to the GraphQL model (omits the secret).
func toModelVirtualKey(k *ent.VirtualKey) *model.VirtualKey {
	m := &model.VirtualKey{
		ID:        k.ID.String(),
		UserID:    k.UserID.String(),
		Models:    k.Models,
		Status:    model.VirtualKeyStatus(string(k.Status)),
		CreatedAt: k.CreatedAt,
	}
	if k.Models == nil {
		m.Models = []string{}
	}
	if k.Alias != "" {
		a := k.Alias
		m.Alias = &a
	}
	if k.AgentID != nil {
		a := k.AgentID.String()
		m.AgentID = &a
	}
	if k.RateLimitPolicyID != nil {
		p := k.RateLimitPolicyID.String()
		m.RateLimitPolicyID = &p
	}
	if k.TeamID != "" {
		tid := k.TeamID
		m.TeamID = &tid
	}
	if k.MaxBudget != 0 {
		b := k.MaxBudget
		m.MaxBudget = &b
	}
	if k.ExpiresAt != nil {
		t := *k.ExpiresAt
		m.ExpiresAt = &t
	}
	return m
}

// toModelAgentTemplate maps a catalog entry to its GraphQL model, resolving the
// install_command's {{PLACEHOLDER}} tokens against installVars (LLD-05 §1).
func toModelAgentTemplate(t *ent.AgentTemplate, installVars map[string]string) *model.AgentTemplate {
	m := &model.AgentTemplate{
		ID:            t.ID.String(),
		Kind:          t.Kind,
		Display:       t.Display,
		InstallMethod: model.InstallMethod(string(t.InstallMethod)),
		Status:        model.AgentTemplateStatus(string(t.Status)),
		CreatedAt:     t.CreatedAt,
	}
	if t.Description != "" {
		d := t.Description
		m.Description = &d
	}
	if t.InstallCommand != "" {
		c := catalog.ResolvePlaceholders(t.InstallCommand, installVars)
		m.InstallCommand = &c
	}
	if t.Version != "" {
		v := t.Version
		m.Version = &v
	}
	if t.KnowledgeRoot != "" {
		kr := t.KnowledgeRoot
		m.KnowledgeRoot = &kr
	}
	if t.KnowledgePrompt != "" {
		kp := t.KnowledgePrompt
		m.KnowledgePrompt = &kp
	}
	return m
}

func toModelAgentConfig(c *ent.AgentConfig) *model.AgentConfig {
	m := &model.AgentConfig{
		ID:        c.ID.String(),
		Name:      c.Name,
		AgentType: c.AgentType,
		IsDefault: c.IsDefault,
		CreatedAt: c.CreatedAt,
	}
	if c.ArtifactID != nil {
		a := c.ArtifactID.String()
		m.ArtifactID = &a
	}
	return m
}

// toModelAgent maps the scalar Agent fields. type→agent_type, endpoint→vm_ref.
// typeLabel/owner/apiKey are populated lazily by agentResolver field resolvers
// (前后端整合契约).
func toModelAgent(a *ent.Agent) *model.Agent {
	m := &model.Agent{
		ID:        a.ID.String(),
		Name:      a.Name,
		Type:      a.AgentType,
		Status:    model.AgentStatus(string(a.Status)),
		CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt,
	}
	if a.VMRef != "" {
		v := a.VMRef
		m.Endpoint = &v
	}
	return m
}

// applyTemplateOptionals sets nullable string fields on a template mutation.
func applyTemplateOptionals(m *ent.AgentTemplateMutation, input model.UpsertAgentTemplateInput) {
	if input.Description != nil {
		m.SetDescription(*input.Description)
	}
	if input.InstallCommand != nil {
		m.SetInstallCommand(*input.InstallCommand)
	}
	if input.Version != nil {
		m.SetVersion(*input.Version)
	}
	if input.KnowledgeRoot != nil {
		m.SetKnowledgeRoot(*input.KnowledgeRoot)
	}
	if input.KnowledgePrompt != nil {
		m.SetKnowledgePrompt(*input.KnowledgePrompt)
	}
}

func toModelTokenUsage(t *ent.TokenUsage) *model.TokenUsage {
	m := &model.TokenUsage{
		ID:           t.ID.String(),
		UserID:       t.UserID.String(),
		Model:        t.Model,
		InputTokens:  t.InputTokens,
		OutputTokens: t.OutputTokens,
		Cost:         t.Cost,
		CreatedAt:    t.CreatedAt,
	}
	if t.AgentID != nil {
		a := t.AgentID.String()
		m.AgentID = &a
	}
	return m
}

func toModelRequestLog(l *ent.RequestLog) *model.RequestLog {
	m := &model.RequestLog{
		ID:           l.ID.String(),
		RequestID:    l.RequestID,
		InputTokens:  l.InputTokens,
		OutputTokens: l.OutputTokens,
		LatencyMs:    l.LatencyMs,
		StatusCode:   l.StatusCode,
		CreatedAt:    l.CreatedAt,
	}
	if l.UserID != nil {
		s := l.UserID.String()
		m.UserID = &s
	}
	if l.AgentID != nil {
		s := l.AgentID.String()
		m.AgentID = &s
	}
	if l.Model != "" {
		mod := l.Model
		m.Model = &mod
	}
	if l.Detail != "" {
		d := l.Detail
		m.Detail = &d
	}
	return m
}

func toModelRateLimitPolicy(p *ent.RateLimitPolicy) *model.RateLimitPolicy {
	return &model.RateLimitPolicy{
		ID:        p.ID.String(),
		Name:      p.Name,
		Rpm:       p.Rpm,
		Tpm:       p.Tpm,
		Enabled:   p.Enabled,
		CreatedAt: p.CreatedAt,
	}
}

// intOrZero dereferences an optional int, defaulting to 0.
func intOrZero(p *int) int {
	if p != nil {
		return *p
	}
	return 0
}

// derefString dereferences an optional string, defaulting to "".
func derefString(p *string) string {
	if p != nil {
		return *p
	}
	return ""
}

func toModelGatewayConnection(g *ent.GatewayConnection) *model.GatewayConnection {
	return &model.GatewayConnection{
		ID:                  g.ID.String(),
		Name:                g.Name,
		Endpoint:            g.Endpoint,
		Status:              model.GatewayStatus(string(g.Status)),
		LoadBalanceStrategy: model.LoadBalanceStrategy(string(g.LoadBalanceStrategy)),
		CreatedAt:           g.CreatedAt,
	}
}

func toModelUpstream(u *ent.Upstream) *model.Upstream {
	m := &model.Upstream{
		ID:        u.ID.String(),
		Name:      u.Name,
		Provider:  model.UpstreamProvider(string(u.Provider)),
		Model:     u.Model,
		Enabled:   u.Enabled,
		CreatedAt: u.CreatedAt,
	}
	if u.APIBase != "" {
		b := u.APIBase
		m.APIBase = &b
	}
	return m
}

func toModelModelRoute(r *ent.ModelRoute) *model.ModelRoute {
	ups := r.Upstreams
	if ups == nil {
		ups = []string{}
	}
	m := &model.ModelRoute{
		ID:         r.ID.String(),
		Name:       r.Name,
		ModelAlias: r.ModelAlias,
		Upstreams:  ups,
		Strategy:   model.LoadBalanceStrategy(string(r.Strategy)),
		Enabled:    r.Enabled,
		CreatedAt:  r.CreatedAt,
	}
	if r.GatewayConnectionID != nil {
		g := r.GatewayConnectionID.String()
		m.BackendGatewayID = &g
	}
	return m
}

func toModelRouterTier(t *ent.RouterTier) *model.RouterTier {
	return &model.RouterTier{
		ID:         t.ID.String(),
		Tier:       model.RouterTierLevel(string(t.Tier)),
		ModelAlias: t.ModelAlias,
	}
}

func toModelArtifact(a *ent.Artifact) *model.Artifact {
	m := &model.Artifact{
		ID: a.ID.String(), Name: a.Name, Kind: model.ArtifactKind(string(a.Kind)),
		Version: a.Version, URI: a.URI, CreatedAt: a.CreatedAt,
	}
	if a.Content != "" {
		c := a.Content
		m.Content = &c
	}
	if a.Sha256 != "" {
		s := a.Sha256
		m.Sha256 = &s
	}
	if len(a.Metadata) > 0 {
		// Copy, don't alias the ent map — the model and the stored entity must not
		// share backing storage (immutability rule).
		md := make(map[string]any, len(a.Metadata))
		for k, v := range a.Metadata {
			md[k] = v
		}
		m.Metadata = md
	}
	return m
}

func toModelSkill(s *ent.Skill) *model.Skill {
	m := &model.Skill{ID: s.ID.String(), Name: s.Name, Version: s.Version, URI: s.URI, CreatedAt: s.CreatedAt}
	if s.Description != "" {
		d := s.Description
		m.Description = &d
	}
	return m
}

func toModelImage(i *ent.Image) *model.Image {
	m := &model.Image{ID: i.ID.String(), Repository: i.Repository, Tag: i.Tag, Signed: i.Signed, CreatedAt: i.CreatedAt}
	if i.Digest != "" {
		d := i.Digest
		m.Digest = &d
	}
	return m
}

func toModelDepartment(d *ent.Department) *model.Department {
	m := &model.Department{ID: d.ID.String(), Name: d.Name, CreatedAt: d.CreatedAt}
	if d.TenantID != nil {
		t := d.TenantID.String()
		m.TenantID = &t
	}
	if d.LitellmTeamID != "" {
		l := d.LitellmTeamID
		m.LitellmTeamID = &l
	}
	return m
}

func toModelMembership(m *ent.Membership) *model.Membership {
	return &model.Membership{
		ID:           m.ID.String(),
		UserID:       m.UserID.String(),
		DepartmentID: m.DepartmentID.String(),
		Role:         entMembershipRoleToGQL(string(m.Role)),
	}
}

func gqlMembershipRoleToEnt(r model.MembershipRole) string {
	if r == model.MembershipRoleDeptAdmin {
		return "dept-admin"
	}
	return string(r)
}

func entMembershipRoleToGQL(s string) model.MembershipRole {
	if s == "dept-admin" {
		return model.MembershipRoleDeptAdmin
	}
	return model.MembershipRole(s)
}

func toModelPermission(p *ent.Permission) *model.Permission {
	m := &model.Permission{ID: p.ID.String(), Key: p.Key}
	if p.Description != "" {
		d := p.Description
		m.Description = &d
	}
	return m
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

// connectPool resolves a resource pool's credentials and dials its vCenter.
func (r *Resolver) connectPool(ctx context.Context, pool *ent.ResourcePool) (VCenterClient, error) {
	if r.Secrets == nil || r.VCenterConnect == nil {
		return nil, fmt.Errorf("resource-pool connect not configured")
	}
	if pool.SecretRef == "" {
		return nil, fmt.Errorf("resource pool has no secret_ref")
	}
	cred, err := r.Secrets.Resolve(ctx, pool.SecretRef)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	return r.VCenterConnect(ctx, pool.Endpoint, cred.Username, cred.Password, r.VCenterInsecure)
}

// clientIP extracts the remote address from the request in context.
func clientIP(ctx context.Context) string {
	if r := httpx.Request(ctx); r != nil {
		return r.RemoteAddr
	}
	return ""
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
	if cu.Role == auth.RoleTenantAdmin {
		dept, err := r.Ent.Department.Get(ctx, did)
		if err != nil {
			if ent.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return tenantMatches(cu.TenantID, dept.TenantID), nil
	}
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

// rollbackDeploy tears down a half-deployed agent after Provision succeeded but
// DB persistence failed: destroy the running VM, revoke the live gateway key, and
// mark the agent exception. Uses a detached context so a canceled request still
// cleans up. Best-effort — each step is logged on failure, never fatal.
func (r *Resolver) rollbackDeploy(ctx context.Context, conn VCenterClient, ag *ent.Agent, vmName, key string) {
	cctx := context.WithoutCancel(ctx)
	if err := conn.Destroy(cctx, vmName); err != nil {
		log.Printf("deploy rollback: orphan VM %q, destroy failed: %v", vmName, err)
	}
	if r.Gateway != nil {
		if err := r.Gateway.DeleteKey(cctx, key); err != nil {
			log.Printf("deploy rollback: orphan gateway key, revoke failed: %v", err)
		}
	}
	if _, err := r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusException).Save(cctx); err != nil {
		log.Printf("deploy rollback: mark agent %s exception failed: %v", ag.ID, err)
	}
}

// connectAgentVM resolves an agent the caller owns, dials its resource pool's
// vCenter, and returns the live connection plus the agent's VM ref. The caller
// MUST Logout the returned connection. Errors (404-style via getOwnedAgent) if
// the agent is not the caller's, has no pool, or has no deployed VM.
func (r *Resolver) connectAgentVM(ctx context.Context, cu *auth.CurrentUser, agentID uuid.UUID) (VCenterClient, string, error) {
	ag, err := r.getOwnedAgent(ctx, agentID, cu)
	if err != nil {
		return nil, "", err
	}
	if ag.VMRef == "" {
		return nil, "", gqlerror.Errorf("agent has no VM (not deployed)")
	}
	if ag.ResourcePoolID == nil {
		return nil, "", gqlerror.Errorf("agent has no resource pool; cannot locate its VM")
	}
	pool, err := r.Ent.ResourcePool.Get(ctx, *ag.ResourcePoolID)
	if err != nil {
		return nil, "", err
	}
	conn, err := r.connectPool(ctx, pool)
	if err != nil {
		return nil, "", fmt.Errorf("connect vcenter: %w", err)
	}
	return conn, ag.VMRef, nil
}

// maxArtifactContent caps inline artifact content (matches the ent MaxLen).
const maxArtifactContent = 65536

// defaultAgentConfigPath is where an agent's inline default_config lands in the
// VM when the artifact doesn't override it via metadata["config_path"].
const defaultAgentConfigPath = "/etc/agent/config"

// resolveAgentConfig loads the agent's inline default_config via
// agent.config_id → AgentConfig.artifact_id → Artifact.content, returning the
// content and the VM path to write it to (LLD-09). Empty content means "no
// config" — deploy then degrades to gateway-env only. Best-effort: a missing or
// unreadable config never fails deploy.
func (r *Resolver) resolveAgentConfig(ctx context.Context, ag *ent.Agent) (content, path string) {
	if ag.ConfigID == nil {
		return "", ""
	}
	cfg, err := r.Ent.AgentConfig.Get(ctx, *ag.ConfigID)
	if err != nil || cfg.ArtifactID == nil {
		return "", ""
	}
	art, err := r.Ent.Artifact.Get(ctx, *cfg.ArtifactID)
	if err != nil || art.Content == "" {
		return "", ""
	}
	path = defaultAgentConfigPath
	if p, ok := art.Metadata["config_path"].(string); ok && p != "" {
		path = p
	}
	return art.Content, path
}

// resolveAgentKnowledge returns the ids of the OKF knowledge packs mounted on an
// agent's config (LLD-11 K2), for 下发 via guestinfo at deploy — the daemon pulls
// each over the control-plane channel (§6). Best-effort: a config-less agent or a
// load error yields no packs (knowledge never blocks deploy).
func (r *Resolver) resolveAgentKnowledge(ctx context.Context, ag *ent.Agent) []string {
	if ag.ConfigID == nil {
		return nil
	}
	cfg, err := r.Ent.AgentConfig.Get(ctx, *ag.ConfigID)
	if err != nil {
		return nil
	}
	arts, err := cfg.QueryKnowledge().Order(orderNewest).All(ctx)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(arts))
	for _, a := range arts {
		ids = append(ids, a.ID.String())
	}
	return ids
}

// agentForField reloads the ent.Agent backing a resolved Agent field (owner/apiKey
// need its FK columns, which are not GraphQL fields). Returns nil (no error) if the
// row vanished between the list query and the field resolution.
func (r *agentResolver) agentForField(ctx context.Context, id string) (*ent.Agent, error) {
	aid, err := uuid.Parse(id)
	if err != nil {
		return nil, nil
	}
	ag, err := r.Ent.Agent.Get(ctx, aid)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ag, nil
}

// applyAgentSort orders the agent query per the requested field (前后端整合契约),
// with id as a stable tiebreaker for deterministic pagination. Native columns sort
// in SQL; OWNER / API_KEY_NAME sort via a LEFT JOIN on the related display column.
func applyAgentSort(q *ent.AgentQuery, sort *model.AgentSort) *ent.AgentQuery {
	if sort == nil {
		return q.Order(orderNewest)
	}
	desc := sort.Direction == model.SortDirectionDesc
	col := ""
	switch sort.Field {
	case model.AgentSortFieldName:
		col = agent.FieldName
	case model.AgentSortFieldType:
		col = agent.FieldAgentType
	case model.AgentSortFieldStatus:
		col = agent.FieldStatus
	case model.AgentSortFieldCreatedAt:
		col = agent.FieldCreatedAt
	case model.AgentSortFieldUpdatedAt:
		col = agent.FieldUpdatedAt
	case model.AgentSortFieldOwner:
		return q.Order(joinOrder(user.Table, agent.FieldOwnerUserID, user.FieldUsername, desc))
	case model.AgentSortFieldAPIKeyName:
		return q.Order(joinOrder(virtualkey.Table, agent.FieldVirtualKeyID, virtualkey.FieldAlias, desc))
	default:
		return q.Order(orderNewest)
	}
	if desc {
		return q.Order(ent.Desc(col), ent.Desc(agent.FieldID))
	}
	return q.Order(ent.Asc(col), ent.Asc(agent.FieldID))
}

// resolvePoolSecretRef turns a resource-pool credential submission into a stored
// secret reference (模块② 接入). The 接入表单 sends a vCenter username/password;
// the backend writes them to the secret store (Vaultwarden) and persists ONLY the
// returned ref — plaintext never lands in the DB. An explicit secretRef (pre-existing
// item) is accepted as an alternative. Returns set=false when no credential was given
// (leave secret_ref untouched). label seeds the secret-store item name.
func (r *Resolver) resolvePoolSecretRef(ctx context.Context, label string, username, password, secretRef *string) (ref string, set bool, err error) {
	u, p := derefString(username), derefString(password)
	if u != "" || p != "" {
		store, ok := r.Secrets.(secrets.Store)
		if !ok {
			return "", false, gqlerror.Errorf("secret store not configured; cannot accept credentials")
		}
		ref, err := store.Put(ctx, "resourcepool/"+label, secrets.Credential{Username: u, Password: p})
		if err != nil {
			return "", false, fmt.Errorf("store pool credentials: %w", err)
		}
		return ref, true, nil
	}
	if secretRef != nil && *secretRef != "" {
		return *secretRef, true, nil
	}
	return "", false, nil
}

// resolveKeySecretRef turns a single raw API/master key submission into a stored
// secret reference (模块③ 路由 / 网关接入). Mirrors resolvePoolSecretRef but for a
// one-field key: the form sends a raw key, the backend writes it to the secret
// store and persists ONLY the ref — plaintext never lands in the DB. An explicit
// existing ref is the alternative; set=false when neither is given. label = the
// secret-store item name (caller-qualified, e.g. "upstream/<name>").
func (r *Resolver) resolveKeySecretRef(ctx context.Context, label string, rawKey, existingRef *string) (ref string, set bool, err error) {
	if k := derefString(rawKey); k != "" {
		store, ok := r.Secrets.(secrets.Store)
		if !ok {
			return "", false, gqlerror.Errorf("secret store not configured; cannot accept credentials")
		}
		ref, err := store.Put(ctx, label, secrets.Credential{APIKey: k})
		if err != nil {
			return "", false, fmt.Errorf("store credential: %w", err)
		}
		return ref, true, nil
	}
	if existingRef != nil && *existingRef != "" {
		return *existingRef, true, nil
	}
	return "", false, nil
}

// applyUserSort orders the user query per the requested field (模块① 用户与权限),
// id as a stable tiebreaker. All columns are native to users — no joins needed.
func applyUserSort(q *ent.UserQuery, sort *model.UserSort) *ent.UserQuery {
	if sort == nil {
		return q.Order(orderNewest)
	}
	desc := sort.Direction == model.SortDirectionDesc
	col := ""
	switch sort.Field {
	case model.UserSortFieldUsername:
		col = user.FieldUsername
	case model.UserSortFieldEmail:
		col = user.FieldEmail
	case model.UserSortFieldRole:
		col = user.FieldRole
	case model.UserSortFieldCreatedAt:
		col = user.FieldCreatedAt
	case model.UserSortFieldLastLogin:
		col = user.FieldLastLoginAt
	default:
		return q.Order(orderNewest)
	}
	if desc {
		return q.Order(ent.Desc(col), ent.Desc(user.FieldID))
	}
	return q.Order(ent.Asc(col), ent.Asc(user.FieldID))
}

// joinOrder orders agents by a column on a related table (owner username / key
// alias). LEFT JOIN keeps agents with no owner/key; id is the stable tiebreaker.
func joinOrder(table, fk, col string, desc bool) func(*sql.Selector) {
	return func(s *sql.Selector) {
		t := sql.Table(table)
		s.LeftJoin(t).On(s.C(fk), t.C("id"))
		if desc {
			s.OrderBy(sql.Desc(t.C(col)), sql.Desc(s.C(agent.FieldID)))
		} else {
			s.OrderBy(sql.Asc(t.C(col)), sql.Asc(s.C(agent.FieldID)))
		}
	}
}

// resolveKnowledgeRoot returns the VM path the daemon should unpack the agent's
// knowledge packs to (LLD-11 K4). It is the agent kind's AgentTemplate
// knowledge_root, or the platform default when unset/unknown.
func (r *Resolver) resolveKnowledgeRoot(ctx context.Context, ag *ent.Agent) string {
	t, err := r.Ent.AgentTemplate.Query().Where(agenttemplate.Kind(ag.AgentType)).Only(ctx)
	if err == nil && t.KnowledgeRoot != "" {
		return t.KnowledgeRoot
	}
	// A genuine DB fault (not just a missing template) silently downgrades a custom
	// root to the default — packs would unpack where the prompt doesn't look. Surface
	// it so the degradation isn't invisible; deploy still proceeds with the default.
	if err != nil && !ent.IsNotFound(err) {
		log.Printf("resolveKnowledgeRoot: kind %q template lookup failed, using default: %v", ag.AgentType, err)
	}
	return catalog.DefaultKnowledgeRoot
}

// toModelAgentSnapshot maps a vcenter snapshot to its GraphQL model.
func toModelAgentSnapshot(s vcenter.SnapshotInfo) *model.AgentSnapshot {
	m := &model.AgentSnapshot{Name: s.Name, State: s.State, CreatedAt: s.CreatedAt}
	if s.Description != "" {
		d := s.Description
		m.Description = &d
	}
	return m
}

// audit writes an AuditLog row for a write operation. Failures are logged, not
// swallowed silently, but never block the underlying operation.
func (r *Resolver) audit(ctx context.Context, action, resType, resID string, ok bool, actorID string) {
	res := auditlog.ResultSuccess
	if !ok {
		res = auditlog.ResultFail
	}
	c := r.Ent.AuditLog.Create().
		SetAction(action).
		SetResourceType(resType).
		SetResourceID(resID).
		SetResult(res)
	if actorID != "" {
		if id, err := uuid.Parse(actorID); err == nil {
			c.SetActorUserID(id)
		}
	}
	if ip := clientIP(ctx); ip != "" {
		c.SetIP(ip)
	}
	if _, err := c.Save(ctx); err != nil {
		log.Printf("audit write failed: action=%s result=%v err=%v", action, ok, err)
	}
}
