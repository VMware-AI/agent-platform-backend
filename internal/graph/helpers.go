package graph

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/auditlog"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
)

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

func toModelAgentTemplate(t *ent.AgentTemplate) *model.AgentTemplate {
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
		c := t.InstallCommand
		m.InstallCommand = &c
	}
	if t.Version != "" {
		v := t.Version
		m.Version = &v
	}
	return m
}

func toModelAgentConfig(c *ent.AgentConfig) *model.AgentConfig {
	return &model.AgentConfig{
		ID:        c.ID.String(),
		Name:      c.Name,
		AgentType: c.AgentType,
		IsDefault: c.IsDefault,
		CreatedAt: c.CreatedAt,
	}
}

func toModelAgent(a *ent.Agent) *model.Agent {
	m := &model.Agent{
		ID:          a.ID.String(),
		Name:        a.Name,
		AgentType:   a.AgentType,
		Status:      model.AgentStatus(string(a.Status)),
		OwnerUserID: a.OwnerUserID.String(),
		CreatedAt:   a.CreatedAt,
	}
	if a.VMRef != "" {
		v := a.VMRef
		m.VMRef = &v
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
	if a.Sha256 != "" {
		s := a.Sha256
		m.Sha256 = &s
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
	perms, err := role.QueryPermissions().All(ctx)
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
	return r.VCenterConnect(ctx, pool.Endpoint, cred.Username, cred.Password, true)
}

// clientIP extracts the remote address from the request in context.
func clientIP(ctx context.Context) string {
	if r := httpx.Request(ctx); r != nil {
		return r.RemoteAddr
	}
	return ""
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
