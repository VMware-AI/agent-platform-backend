package graph

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"entgo.io/ent/dialect/sql"

	"strings"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/agenttemplate"
	"github.com/VMware-AI/agent-platform-backend/ent/auditlog"
	"github.com/VMware-AI/agent-platform-backend/ent/department"
	"github.com/VMware-AI/agent-platform-backend/ent/gatewayconnection"
	"github.com/VMware-AI/agent-platform-backend/ent/membership"
	"github.com/VMware-AI/agent-platform-backend/ent/resourcepool"
	"github.com/VMware-AI/agent-platform-backend/ent/tokenusage"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/catalog"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
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
func (r *Resolver) resolveUserWriteTenant(ctx context.Context, inputTenantID *string, role model.RoleName) (*uuid.UUID, error) {
	cu := auth.FromContext(ctx)
	if cu != nil && cu.Role == auth.RoleTenantAdmin {
		tid, err := uuid.Parse(cu.TenantID)
		if err != nil {
			return nil, gqlerror.Errorf("forbidden")
		}
		if inputTenantID != nil && *inputTenantID != tid.String() {
			return nil, gqlerror.Errorf("forbidden: cannot create users in another tenant")
		}
		if role == model.RoleNameAdmin || role == model.RoleNameTenantAdmin {
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
		ID:                 p.ID.String(),
		Name:               p.Name,
		Endpoint:           p.Endpoint,
		ContentLibraryName: p.ContentLibraryName,
		Insecure:           p.Insecure,
		ConnectionStatus:   poolConnStatus(p.Status),
		DatacenterCount:    p.DatacenterCount,
		ClusterCount:       p.ClusterCount,
		EsxiHostCount:      p.HostCount,
		VMInstanceCount:    p.VMCount,
		SyncStatus:         poolSyncState(p),
		LastSyncedAt:       p.LastSyncedAt,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}

// poolConnStatus collapses the ent tri-state (connected/disconnected/error) into
// the console's binary status: only "connected" reads as CONNECTED; everything
// else (including error) reads as DISCONNECTED.
func poolConnStatus(s resourcepool.Status) model.PoolConnectionStatus {
	if s == resourcepool.StatusConnected {
		return model.PoolConnectionStatusConnected
	}
	return model.PoolConnectionStatusDisconnected
}

// poolSyncState derives the console's inventory-sync state: never synced → NEVER;
// last sync errored (status=error) → FAILED; otherwise (a successful sync is
// recorded) → SYNCED. The backend's sync is synchronous, so SYNCING/PARTIAL are
// never produced.
func poolSyncState(p *ent.ResourcePool) model.ResourcePoolSyncState {
	switch {
	case p.Status == resourcepool.StatusError:
		return model.ResourcePoolSyncStateFailed
	case p.LastSyncedAt == nil:
		return model.ResourcePoolSyncStateNever
	default:
		return model.ResourcePoolSyncStateSynced
	}
}

// applyResourcePoolSort orders a pool query by the console's sort field, with a
// stable id tiebreak. CONNECTION_STATUS sorts on the ent status column; the
// default (CREATED_AT) and any unmapped field fall back to created_at.
func applyResourcePoolSort(q *ent.ResourcePoolQuery, sort *model.ResourcePoolSort) *ent.ResourcePoolQuery {
	if sort == nil {
		return q.Order(ent.Desc(resourcepool.FieldCreatedAt))
	}
	desc := sort.Direction == model.SortDirectionDesc
	col := ""
	switch sort.Field {
	case model.ResourcePoolSortFieldName:
		col = resourcepool.FieldName
	case model.ResourcePoolSortFieldEndpoint:
		col = resourcepool.FieldEndpoint
	case model.ResourcePoolSortFieldConnectionStatus:
		col = resourcepool.FieldStatus
	case model.ResourcePoolSortFieldDatacenterCount:
		col = resourcepool.FieldDatacenterCount
	case model.ResourcePoolSortFieldClusterCount:
		col = resourcepool.FieldClusterCount
	case model.ResourcePoolSortFieldEsxiHostCount:
		col = resourcepool.FieldHostCount
	case model.ResourcePoolSortFieldVMInstanceCount:
		col = resourcepool.FieldVMCount
	case model.ResourcePoolSortFieldUpdatedAt:
		col = resourcepool.FieldUpdatedAt
	default: // CREATED_AT
		col = resourcepool.FieldCreatedAt
	}
	if desc {
		return q.Order(ent.Desc(col), ent.Desc(resourcepool.FieldID))
	}
	return q.Order(ent.Asc(col), ent.Asc(resourcepool.FieldID))
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
// (前后端整合契约). The FK ids (OwnerUserID/VirtualKeyID) are carried on the model
// as Go-only fields so those lazy resolvers never re-fetch the agent row (N+1
// kill); they batch only the related User/VirtualKey via the request loaders.
func toModelAgent(a *ent.Agent) *model.Agent {
	m := &model.Agent{
		ID:           a.ID.String(),
		Name:         a.Name,
		Type:         a.AgentType,
		Status:       model.AgentStatus(string(a.Status)),
		CreatedAt:    a.CreatedAt,
		UpdatedAt:    a.UpdatedAt,
		OwnerUserID:  a.OwnerUserID,
		VirtualKeyID: a.VirtualKeyID,
	}
	if a.VMRef != "" {
		v := a.VMRef
		m.Endpoint = &v
	}
	if a.TemplateFamilyID != nil {
		s := a.TemplateFamilyID.String()
		m.TemplateFamilyID = &s
	}
	if a.TemplateVersionID != nil {
		s := a.TemplateVersionID.String()
		m.TemplateVersionID = &s
	}
	if a.ResourcePoolID != nil {
		s := a.ResourcePoolID.String()
		m.ResourcePoolID = &s
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

// clampLimit normalizes an optional list-limit into [1, max], defaulting to def.
func clampLimit(p *int, def, max int) int {
	n := def
	if p != nil {
		n = *p
	}
	if n < 1 {
		n = def
	}
	if n > max {
		n = max
	}
	return n
}

// dashboardAgentStatus projects the ent agent status onto the console's 3-state
// overview badge. provisioning is not yet running, so it surfaces as stopped.
func dashboardAgentStatus(s agent.Status) model.DashboardAgentStatus {
	switch s {
	case agent.StatusRunning:
		return model.DashboardAgentStatusRunning
	case agent.StatusException:
		return model.DashboardAgentStatusException
	default: // stopped + provisioning
		return model.DashboardAgentStatusStopped
	}
}

// noticeText renders a human-readable system-notice line from an audit log's
// action + resource type, marked succeeded/failed. e.g. ("resource_pool.test",
// "resource_pool", success) → "resource_pool.test on resource_pool succeeded".
func noticeText(action, resourceType string, status model.DashboardNoticeStatus) string {
	verb := "succeeded"
	if status == model.DashboardNoticeStatusDanger {
		verb = "failed"
	}
	if resourceType != "" {
		return fmt.Sprintf("%s on %s %s", action, resourceType, verb)
	}
	return fmt.Sprintf("%s %s", action, verb)
}

// monthlyUsageTotals sums input/output tokens and cost over the given TokenUsage
// query, returning zeros when there are no rows (the aggregate scan yields no row
// on an empty table for some drivers).
func (r *Resolver) monthlyUsageTotals(ctx context.Context, q *ent.TokenUsageQuery) (in, out int, cost float64, err error) {
	var agg []struct {
		In   int     `json:"in"`
		Out  int     `json:"out"`
		Cost float64 `json:"cost"`
	}
	if err = q.Clone().Aggregate(
		ent.As(ent.Sum(tokenusage.FieldInputTokens), "in"),
		ent.As(ent.Sum(tokenusage.FieldOutputTokens), "out"),
		ent.As(ent.Sum(tokenusage.FieldCost), "cost"),
	).Scan(ctx, &agg); err != nil {
		return 0, 0, 0, err
	}
	if len(agg) == 1 {
		return agg[0].In, agg[0].Out, agg[0].Cost, nil
	}
	return 0, 0, 0, nil
}

// scopedTokenUsageQuery builds a TokenUsage query confined to the caller's tenant
// (tenant-admin → own tenant; admin → all) and environment (when env_scope is on),
// optionally narrowed to one user. Shared by the metering aggregations so they all
// agree on visibility (LLD-10).
func (r *Resolver) scopedTokenUsageQuery(ctx context.Context, userID *string) (*ent.TokenUsageQuery, error) {
	q := r.Ent.TokenUsage.Query()
	if userID != nil {
		uid, err := uuid.Parse(*userID)
		if err != nil {
			return nil, gqlerror.Errorf("invalid userId")
		}
		q = q.Where(tokenusage.UserID(uid))
	}
	if d := tenantScopeFor(ctx); d.apply {
		if d.denyAll {
			q = q.Where(tokenusage.IDEQ(uuid.Nil))
		} else {
			q = q.Where(tokenusage.TenantID(d.tenant))
		}
	}
	if env, ok := r.envScopeFor(ctx); ok {
		q = q.Where(tokenusage.Or(tokenusage.EnvironmentID(env), tokenusage.EnvironmentIDIsNil()))
	}
	return q, nil
}

// meteringRangeStart maps a MeteringTimeRange to the inclusive lower bound of the
// window, relative to now. LAST_7_DAYS (default) and LAST_30_DAYS are rolling
// day windows; THIS_MONTH starts at the first of the current calendar month.
func meteringRangeStart(rng model.MeteringTimeRange, now time.Time) time.Time {
	switch rng {
	case model.MeteringTimeRangeLast30Days:
		return now.AddDate(0, 0, -30)
	case model.MeteringTimeRangeThisMonth:
		return startOfMonth(now)
	default: // LAST_7_DAYS
		return now.AddDate(0, 0, -7)
	}
}

// startOfMonth returns midnight on the first day of now's calendar month (UTC),
// matching the DB-side UTC day buckets.
func startOfMonth(now time.Time) time.Time {
	y, m, _ := now.UTC().Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}

// agentNamesFor resolves agent display names for a per-agent usage breakdown in one
// batched query (id → name). Rows whose agent no longer exists fall back to the id
// string so the metering table never shows a blank name. The argument is the
// anonymous per-agent aggregation slice; only its AgentID is read.
func (r *Resolver) agentNamesFor(ctx context.Context, rows []struct {
	AgentID string  `json:"agent_id"`
	In      int     `json:"in"`
	Out     int     `json:"out"`
	Cost    float64 `json:"cost"`
	Reqs    int     `json:"reqs"`
}) (map[string]string, error) {
	names := make(map[string]string, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if id, err := uuid.Parse(row.AgentID); err == nil {
			ids = append(ids, id)
		}
		names[row.AgentID] = row.AgentID // fallback to id
	}
	if len(ids) == 0 {
		return names, nil
	}
	ags, err := r.Ent.Agent.Query().Where(agent.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range ags {
		names[a.ID.String()] = a.Name
	}
	return names, nil
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

// vmNameInvalidChars matches characters that are not safe in a vSphere VM name;
// they are collapsed to a single dash. vCenter disallows the special chars
// %/\?*:|"<> among others, so we keep only word chars, dot and dash.
var vmNameInvalidChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// uniqueVMName derives a collision-free vCenter VM name from the agent's display
// name + the first 8 chars of its (unique) id. The display name alone can repeat
// across agents, which would collide on the VM clone; the id suffix disambiguates.
// The result is sanitized to a valid vSphere VM name.
func uniqueVMName(displayName string, id uuid.UUID) string {
	base := strings.Trim(vmNameInvalidChars.ReplaceAllString(displayName, "-"), "-")
	if base == "" {
		base = "agent"
	}
	return base + "-" + id.String()[:8]
}

// parseOptionalUUID parses an optional id input into a *uuid.UUID. nil input →
// nil result (no error); a malformed id → a user-facing error naming the field.
func parseOptionalUUID(s *string, field string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, gqlerror.Errorf("invalid %s", field)
	}
	return &id, nil
}

// orEmptyStrings returns the slice unchanged, or an empty (non-nil) slice when nil,
// so a stored string list is never NULL.
func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toModelGatewayConnection(g *ent.GatewayConnection) *model.GatewayConnection {
	var publicURL *string
	if g.PublicURL != "" {
		publicURL = &g.PublicURL
	}
	return &model.GatewayConnection{
		ID:                  g.ID.String(),
		Name:                g.Name,
		Endpoint:            g.Endpoint,
		PublicURL:           publicURL,
		IsDefault:           g.IsDefault,
		Status:              model.GatewayStatus(string(g.Status)),
		LoadBalanceStrategy: model.LoadBalanceStrategy(string(g.LoadBalanceStrategy)),
		CreatedAt:           g.CreatedAt,
	}
}

// ---- 模型网关页 (P4): GatewayConnection façade helpers ----

// modelGatewayStatus maps the ent connection status to the console status enum.
func modelGatewayStatus(s gatewayconnection.Status) model.ModelGatewayStatus {
	switch s {
	case gatewayconnection.StatusConnected:
		return model.ModelGatewayStatusConnected
	case gatewayconnection.StatusError:
		return model.ModelGatewayStatusError
	default:
		return model.ModelGatewayStatusDisconnected
	}
}

// entGatewayStatus maps the console status enum back to the ent column (filter).
func entGatewayStatus(s model.ModelGatewayStatus) gatewayconnection.Status {
	switch s {
	case model.ModelGatewayStatusConnected:
		return gatewayconnection.StatusConnected
	case model.ModelGatewayStatusError:
		return gatewayconnection.StatusError
	default:
		return gatewayconnection.StatusDisconnected
	}
}

// modelGatewaySyncState derives the sync state from the connection status — there
// is no dedicated sync-tracking store yet: connected→SYNCED, error→FAILED,
// disconnected→NEVER (never synced).
func modelGatewaySyncState(s gatewayconnection.Status) model.ModelGatewaySyncState {
	switch s {
	case gatewayconnection.StatusConnected:
		return model.ModelGatewaySyncStateSynced
	case gatewayconnection.StatusError:
		return model.ModelGatewaySyncStateFailed
	default:
		return model.ModelGatewaySyncStateNever
	}
}

// toModelGateway projects a GatewayConnection (+ the live backend-model count) into
// the console's ModelGateway aggregate. provider/strategy are the console's single
// supported values; adminUrl is the operator-set admin_url, falling back to the
// derived <endpoint>/ui; latencyMs is transient (only a live test sets it);
// lastSyncAt is the real last_synced_at column (nil until the gateway has ever
// successfully connected), and never moves on an unrelated edit.
func toModelGateway(g *ent.GatewayConnection, backendModelCount int) *model.ModelGateway {
	adminURL := g.AdminURL
	if adminURL == "" {
		adminURL = strings.TrimRight(g.Endpoint, "/") + "/ui"
	}
	return &model.ModelGateway{
		ID:                    g.ID.String(),
		Name:                  g.Name,
		Provider:              model.ModelGatewayProviderLitellm,
		Endpoint:              g.Endpoint,
		Status:                modelGatewayStatus(g.Status),
		BackendModelCount:     backendModelCount,
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin,
		AdminURL:              &adminURL,
		LastSyncAt:            g.LastSyncedAt,
		LastSyncStatus:        modelGatewaySyncState(g.Status),
		CreatedAt:             g.CreatedAt,
		UpdatedAt:             g.UpdatedAt,
	}
}

// applyModelGatewaySort orders a gateway query by the console's sort field with a
// stable id tiebreak; default (and any unmapped field) is newest-first.
func applyModelGatewaySort(q *ent.GatewayConnectionQuery, sort *model.ModelGatewaySort) *ent.GatewayConnectionQuery {
	if sort == nil {
		return q.Order(ent.Desc(gatewayconnection.FieldCreatedAt))
	}
	desc := sort.Direction == model.SortDirectionDesc
	col := gatewayconnection.FieldCreatedAt
	switch sort.Field {
	case model.ModelGatewaySortFieldName:
		col = gatewayconnection.FieldName
	case model.ModelGatewaySortFieldEndpoint:
		col = gatewayconnection.FieldEndpoint
	case model.ModelGatewaySortFieldStatus:
		col = gatewayconnection.FieldStatus
	case model.ModelGatewaySortFieldUpdatedAt:
		col = gatewayconnection.FieldUpdatedAt
	default: // CREATED_AT
		col = gatewayconnection.FieldCreatedAt
	}
	if desc {
		return q.Order(ent.Desc(col), ent.Desc(gatewayconnection.FieldID))
	}
	return q.Order(ent.Asc(col), ent.Asc(gatewayconnection.FieldID))
}

// backendModelCount is the number of registered upstreams the litellm gateway
// fronts (real DB count, surfaced as ModelGateway.backendModelCount).
func (r *Resolver) backendModelCount(ctx context.Context) (int, error) {
	return r.Ent.Upstream.Query().Count(ctx)
}

// buildGatewayModels builds a litellm model-manager bound to a SPECIFIC gateway
// row — its own endpoint and master key (resolved from the secret store) — so a
// model/connection op hits the right gateway, not a process-wide default. The
// builder is injectable (GatewayClientFor) so tests can supply a fake.
func (r *Resolver) buildGatewayModels(ctx context.Context, g *ent.GatewayConnection) gateway.ModelManager {
	masterKey := r.gatewayMasterKey(ctx, g)
	if r.GatewayClientFor != nil {
		return r.GatewayClientFor(ctx, g.Endpoint, masterKey)
	}
	return gateway.NewHTTPClient(g.Endpoint, masterKey)
}

// applyGatewayTestResult persists a connection-test outcome on a gateway row: it
// sets the status and, on a successful connect, stamps last_synced_at = now.
// Both gateway façades (ModelGateway + the legacy routing GatewayConnection) go
// through here so they agree on "last synced" — a successful test from either the
// 模型网关接入 page or the 模型路由 page advances the same timestamp.
func (r *Resolver) applyGatewayTestResult(ctx context.Context, g *ent.GatewayConnection, status gatewayconnection.Status) (*ent.GatewayConnection, error) {
	upd := r.Ent.GatewayConnection.UpdateOne(g).SetStatus(status)
	if status == gatewayconnection.StatusConnected {
		upd.SetLastSyncedAt(time.Now())
	}
	return upd.Save(ctx)
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
		ID:          r.ID.String(),
		Name:        r.Name,
		ModelAlias:  r.ModelAlias,
		GatewayName: r.GatewayName,
		Upstreams:   ups,
		// Console alias for upstreams — same backing slice (the route's model group).
		SupportedModels: ups,
		Strategy:        model.LoadBalanceStrategy(string(r.Strategy)),
		UIStrategy:      model.ModelRouteStrategy(string(r.UIStrategy)),
		Enabled:         r.Enabled,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
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
	if d.GatewayConnectionID != nil {
		g := d.GatewayConnectionID.String()
		m.GatewayConnectionID = &g
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

// poolProbeTimeout bounds the pre-save reachability dial (testResourcePoolConnection).
const poolProbeTimeout = 5 * time.Second

// defaultVCenterPort is the HTTPS port a vCenter endpoint listens on; used when the
// operator's endpoint omits an explicit port.
const defaultVCenterPort = "443"

// parseEndpointHostPort validates a resource-pool endpoint (a host, host:port, or
// https URL) and returns a "host:port" suitable for net.Dial. It rejects empty or
// malformed input — the boundary validation for the credential-less probe — and
// defaults the port to vCenter HTTPS (443) when none is given.
func parseEndpointHostPort(endpoint string) (string, error) {
	e := strings.TrimSpace(endpoint)
	if e == "" {
		return "", fmt.Errorf("endpoint is required")
	}
	// Accept a full URL (https://host[:port][/path]) or a bare host[:port].
	host := e
	if strings.Contains(e, "://") {
		u, err := url.Parse(e)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("endpoint %q is not a valid URL", endpoint)
		}
		host = u.Host
	}
	// Split host:port; default the port when absent.
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		// No port present (or another error) — treat the whole thing as the host.
		h, p = host, defaultVCenterPort
	}
	if strings.TrimSpace(h) == "" {
		return "", fmt.Errorf("endpoint %q has no host", endpoint)
	}
	return net.JoinHostPort(h, p), nil
}

// dialReachable performs a bounded TCP dial to confirm an endpoint is reachable.
// LIMITATION: this is a transport-level reachability check only — it does NOT
// complete a TLS handshake or authenticate, so it cannot verify the endpoint is
// actually a vCenter or that the supplied credentials work. That full check
// happens later, with credentials, in syncResourcePool.
func dialReachable(ctx context.Context, hostPort string) error {
	d := net.Dialer{Timeout: poolProbeTimeout}
	dctx, cancel := context.WithTimeout(ctx, poolProbeTimeout)
	defer cancel()
	conn, err := d.DialContext(dctx, "tcp", hostPort)
	if err != nil {
		return err
	}
	return conn.Close()
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
	return r.VCenterConnect(ctx, pool.Endpoint, cred.Username, cred.Password, pool.Insecure)
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
func (r *Resolver) rollbackDeploy(ctx context.Context, conn VCenterClient, gw gateway.Client, ag *ent.Agent, vmName, key string) {
	cctx := context.WithoutCancel(ctx)
	if err := conn.Destroy(cctx, vmName); err != nil {
		log.Printf("deploy rollback: orphan VM %q, destroy failed: %v", vmName, err)
	}
	revokeDeployKey(cctx, gw, key, ag.ID.String())
	if _, err := r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusException).Save(cctx); err != nil {
		log.Printf("deploy rollback: mark agent %s exception failed: %v", ag.ID, err)
	}
}

// deleteAgentRow drops a freshly-created agent row when its deploy aborts before
// any VM/key exists (create-from-OVA flow). The row was created by DeployAgent
// itself, so removing it leaves no orphan. Detached ctx so cleanup runs even if
// the request ctx was canceled. Failures are logged (never swallowed).
func (r *Resolver) deleteAgentRow(ctx context.Context, ag *ent.Agent) {
	if err := r.Ent.Agent.DeleteOne(ag).Exec(context.WithoutCancel(ctx)); err != nil {
		log.Printf("deploy rollback: delete agent row %s failed: %v", ag.ID, err)
	}
}

// rollbackDeployCreate compensates a failed create-from-OVA deploy AFTER the VM
// and gateway key already exist: destroy the VM, revoke the key, and delete the
// agent row (it was created by this same deploy and never went live, so unlike
// rollbackDeploy we drop it rather than mark it exception).
func (r *Resolver) rollbackDeployCreate(ctx context.Context, conn VCenterClient, gw gateway.Client, ag *ent.Agent, vmName, key string) {
	cctx := context.WithoutCancel(ctx)
	if err := conn.Destroy(cctx, vmName); err != nil {
		log.Printf("deploy rollback: orphan VM %q, destroy failed: %v", vmName, err)
	}
	revokeDeployKey(cctx, gw, key, ag.ID.String())
	r.deleteAgentRow(cctx, ag)
}

// revokeDeployKey best-effort revokes a deploy's gateway key during rollback. The
// key was minted on the agent's department/default gateway (gw, LLD-13 §3.3); the
// rollback MUST revoke through that SAME client — not a process-wide default that
// no longer exists — or a failed deploy leaks a live, billable key. Never silent.
func revokeDeployKey(ctx context.Context, gw gateway.Client, key, agentID string) {
	if gw == nil {
		log.Printf("deploy rollback: no gateway to revoke key for agent %s (orphan key)", agentID)
		return
	}
	if err := gw.DeleteKey(ctx, key); err != nil {
		log.Printf("deploy rollback: orphan gateway key for agent %s, revoke failed: %v", agentID, err)
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

// assertGatewayDeletable refuses to delete a GatewayConnection that is still
// referenced by a department (gateway_connection_id is a soft FK — deleting
// would silently break those departments' key/team ops, LLD-13 §3.3) or that is
// the platform default (the platform must always keep one). Shared by both
// delete façades (DeleteGatewayConnection + DeleteModelGateway) so they enforce
// the same guards.
func (r *Resolver) assertGatewayDeletable(ctx context.Context, g *ent.GatewayConnection) error {
	if n, err := r.Ent.Department.Query().Where(department.GatewayConnectionID(g.ID)).Count(ctx); err != nil {
		return err
	} else if n > 0 {
		return gqlerror.Errorf("gateway is used by %d department(s); reassign them before deleting", n)
	}
	if g.IsDefault {
		return gqlerror.Errorf("cannot delete the default gateway; set another default first")
	}
	return nil
}

// deleteSecretRef best-effort removes a secret-store entry (e.g. a gateway master
// key) when its owning row is deleted or its key rotated, so the store doesn't
// accumulate orphans. Never fatal — a resolver that can't delete (store missing,
// or not a Store) is logged, not surfaced: the DB row is already gone, so failing
// the mutation would be worse than a lingering secret.
func (r *Resolver) deleteSecretRef(ctx context.Context, ref string) {
	if ref == "" {
		return
	}
	store, ok := r.Secrets.(secrets.Store)
	if !ok {
		return
	}
	if err := store.Delete(ctx, ref); err != nil {
		log.Printf("secret cleanup: delete ref failed (orphan possible): %v", err)
	}
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
	case model.UserSortFieldUpdatedAt:
		col = user.FieldUpdatedAt
	case model.UserSortFieldLastLogin:
		col = user.FieldLastLoginAt
	default:
		// CONNECTION (online status) is derived, not a column — fall back to newest.
		return q.Order(orderNewest)
	}
	if desc {
		return q.Order(ent.Desc(col), ent.Desc(user.FieldID))
	}
	return q.Order(ent.Asc(col), ent.Asc(user.FieldID))
}

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

// toAccountUser maps an ent.User to the frontend AccountUser shape. online drives
// the connection-status badge. displayName has no column yet → mirrors username.
func toAccountUser(u *ent.User, online bool) *model.AccountUser {
	roleID := string(entRoleToGQL(string(u.Role)))
	name, _, _ := builtinRole(roleID)
	cs := model.ConnectionStatusOffline
	if online {
		cs = model.ConnectionStatusOnline
	}
	m := &model.AccountUser{
		ID:               u.ID.String(),
		Username:         u.Username,
		DisplayName:      u.Username,
		Email:            u.Email,
		Role:             &model.AccountRoleRef{ID: roleID, Name: name},
		ConnectionStatus: cs,
		Enabled:          u.IsActive,
		CreatedAt:        u.CreatedAt,
		UpdatedAt:        u.UpdatedAt,
	}
	if u.LastLoginAt != nil {
		m.LastLoginAt = u.LastLoginAt
	}
	return m
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
