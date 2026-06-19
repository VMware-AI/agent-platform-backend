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
