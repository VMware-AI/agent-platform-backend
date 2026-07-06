package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/resourcepool"
	"github.com/VMware-AI/agent-platform-backend/internal/catalog"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// toModelUser maps an ent.User to the GraphQL model (omits password_hash).
func toModelUser(u *ent.User) *model.User {
	m := &model.User{
		ID:                 u.ID.String(),
		Username:           u.Username,
		Email:              u.Email,
		Role:               entRoleToGQL(string(u.Role)),
		MustChangePassword: u.MustChangePassword,
		Enabled:            u.IsActive,
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
		Datacenters:        toModelDataCenters(p.Inventory),
		SyncStatus:         poolSyncState(p),
		LastSyncedAt:       p.LastSyncedAt,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}

// toModelDataCenters converts ent inventory (vcenter.DataCenter) into the
// GraphQL model. StoragePolicies is a plain non-null list — a failed PBM
// pull and "no profiles" both map to [] (schema contract; see #98 for the
// nullable variant if the console ever needs the distinction).
func toModelDataCenters(jsonVal []vcenter.DataCenter) []model.DataCenter {
	out := make([]model.DataCenter, 0, len(jsonVal))
	for _, dc := range jsonVal {
		out = append(out, model.DataCenter{
			Name:            dc.Name,
			Path:            dc.Path,
			Clusters:        toModelClusters(dc.Clusters),
			Datastores:      toModelPlacementRefs(dc.Datastores),
			Networks:        toModelPlacementRefs(dc.Networks),
			Folders:         toModelPlacementRefs(dc.Folders),
			StoragePolicies: toModelPlacementRefs(dc.StoragePolicies),
		})
	}
	return out
}

func toModelClusters(jsonVal []vcenter.Cluster) []model.Cluster {
	out := make([]model.Cluster, 0, len(jsonVal))
	for _, c := range jsonVal {
		out = append(out, model.Cluster{
			Name:          c.Name,
			Path:          c.Path,
			EsxiHosts:     toModelPlacementRefs(c.EsxiHosts),
			ResourcePools: toModelPlacementRefs(c.ResourcePools),
		})
	}
	return out
}

func toModelPlacementRefs(jsonVal []vcenter.PlacementRef) []model.PlacementRef {
	out := make([]model.PlacementRef, 0, len(jsonVal))
	for _, r := range jsonVal {
		ref := model.PlacementRef{Name: r.Name}
		if r.Path != "" {
			p := r.Path
			ref.Path = &p
		}
		out = append(out, ref)
	}
	return out
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

// formatRemainingDuration returns a human-friendly "remaining lifetime" string
// for display on the console (e.g. "30d", "12h", ""). Pure projection —
// not persisted. Empty when expiresAt is nil.
func formatRemainingDuration(expiresAt *time.Time) string {
	if expiresAt == nil {
		return ""
	}
	d := time.Until(*expiresAt)
	if d <= 0 {
		return "expired"
	}
	days := int(d.Hours() / 24)
	if days >= 1 {
		return fmt.Sprintf("%dd", days)
	}
	hours := int(d.Hours())
	if hours >= 1 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// modelsOrEmpty / tagsOrEmpty / routesOrEmpty: nil → []string{} so the
// GraphQL non-null array contract holds even when the ent column is empty.
func modelsOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func tagsOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func routesOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// toModelVirtualKey maps an ent.VirtualKey to the GraphQL model.
//
// Signature is (ctx, r, k) — NOT (k) — because the VirtualKey.modelGateway
// nested object requires a database lookup. The lookup + nested model
// construction happens in this mapper; `r` is the resolver (for Ent
// access) and `ctx` is the request context.
//
// The ModelGateway nested object is set via `r.toModelGateway` from
// gateway_facade.go (intra-package, since both live in `package graph`).
// This is the single source of truth for the ent→GraphQL ModelGateway
// field mapping. See Task 11 for the rename to buildModelGatewayFromConn.
func toModelVirtualKey(ctx context.Context, r *Resolver, k *ent.VirtualKey) (*model.VirtualKey, error) {
	mg, err := lookupModelGateway(ctx, r, k.ModelGatewayID)
	if err != nil {
		return nil, err
	}
	return &model.VirtualKey{
		ID:             k.ID.String(),
		Name:           k.Name,
		MaskedKey:      k.MaskedKey,
		OrganizationID: k.OrganizationID,
		ModelGateway:   r.toModelGateway(mg),
		AgentID:        uuidOrNil(k.AgentID),
		Models:         modelsOrEmpty(k.Models),
		Tags:           tagsOrEmpty(k.Tags),
		AllowedRoutes:  routesOrEmpty(k.AllowedRoutes),
		Blocked:        k.Blocked,
		KeyType:        k.KeyType,
		AutoRotate:     k.AutoRotate,
		Spend:          float64(k.Spend),
		Status:         model.VirtualKeyStatus(string(k.Status)),
		CreatedAt:      k.CreatedAt,
		UpdatedAt:      k.UpdatedAt,
		Duration:       stringPtr(formatRemainingDuration(k.ExpiresAt)),
		// Optional/nullable pointers via existing ent helpers:
		MaxParallelRequests: intPtr(k.MaxParallelRequests),
		TpmLimit:            intPtr(k.TpmLimit),
		RpmLimit:            intPtr(k.RpmLimit),
		RpmLimitType:        strPtr(k.RpmLimitType),
		TpmLimitType:        strPtr(k.TpmLimitType),
		BudgetDuration:      strPtr(k.BudgetDuration),
		ExpiresAt:           k.ExpiresAt,
		RotationInterval:    strPtr(k.RotationInterval),
		LastActiveAt:        k.LastActiveAt,
		MaxBudget:           float64Ptr(k.MaxBudget),
	}, nil
}

// lookupModelGateway fetches the GatewayConnection that issued this key.
// NotFound is converted to a gqlerror so callers can surface a clean 404.
func lookupModelGateway(ctx context.Context, r *Resolver, id uuid.UUID) (*ent.GatewayConnection, error) {
	c, err := r.Ent.GatewayConnection.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, gqlerror.Errorf("model gateway not found")
		}
		return nil, err
	}
	return c, nil
}

// uuidOrNil returns the UUID as a string, or nil if the pointer is nil.
// Used to map ent nullable FK pointers to GraphQL optional ID fields.
func uuidOrNil(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

func intPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func strPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func float64Ptr(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

func stringPtrForMapper(v string) *string {
	if v == "" {
		return nil
	}
	return &v
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
		Strategy:        model.LoadBalancingStrategy(string(r.Strategy)),
		UIStrategy:      model.ModelRouteStrategy(string(r.UIStrategy)),
		Enabled:         r.Enabled,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
	if r.GatewayConnectionID != uuid.Nil {
		m.BackendGatewayID = r.GatewayConnectionID.String()
	}
	return m
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

// toAccountUser maps an ent.User to the frontend AccountUser shape. online drives
// the connection-status badge. displayName has no column yet → mirrors username.
func toAccountUser(u *ent.User, online bool) *model.AccountUser {
	roleKey := string(u.Role)
	name, _, _ := builtinRole(roleKey)
	cs := model.ConnectionStatusOffline
	if online {
		cs = model.ConnectionStatusOnline
	}
	m := &model.AccountUser{
		ID:          u.ID.String(),
		Username:    u.Username,
		DisplayName: u.Username,
		Email:       u.Email,
		Role: &model.AccountRoleRef{
			ID:   builtinRoleUUID(roleKey),
			Name: name,
		},
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

// toModelAgentSnapshot maps a vcenter snapshot to its GraphQL model.
func toModelAgentSnapshot(s vcenter.SnapshotInfo) *model.AgentSnapshot {
	m := &model.AgentSnapshot{Name: s.Name, State: s.State, CreatedAt: s.CreatedAt}
	if s.Description != "" {
		d := s.Description
		m.Description = &d
	}
	return m
}
