package graph

import (
	"context"
	"log"

	"github.com/VMware-AI/agent-platform-backend/ent/auditlog"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
	"github.com/google/uuid"
)

// actorID returns the current user's id, or "" if unauthenticated.
func actorID(cu *auth.CurrentUser) string {
	if cu != nil {
		return cu.ID
	}
	return ""
}

// clientIP extracts the remote address from the request in context. It uses
// RemoteAddr ONLY — never X-Forwarded-For / X-Real-IP — because those headers are
// client-controlled, and trusting them unconditionally would let an attacker
// spoof both the audit IP and the login-throttle key. This is correct when the
// backend is the network edge. Behind a trusted reverse proxy, parse XFF ONLY
// from a configured trusted-proxy allowlist (not implemented here — until then
// the backend MUST be the edge, or audit IPs/throttle keys read as the proxy IP).
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
