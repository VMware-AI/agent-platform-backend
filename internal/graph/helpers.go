package graph

import (
	"context"
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
		ID:        p.ID.String(),
		Name:      p.Name,
		Kind:      string(p.Kind),
		Endpoint:  p.Endpoint,
		Status:    model.ResourcePoolStatus(string(p.Status)),
		CreatedAt: p.CreatedAt,
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
