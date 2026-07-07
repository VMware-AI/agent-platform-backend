package graph

import (
	"context"
	"log"

	"github.com/VMware-AI/agent-platform-backend/ent/auditlog"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/google/uuid"
)

// Secret-access purposes — short labels that record WHY a credential was
// unsealed, so an audit reader can tell a vCenter-connect Resolve apart from
// a provider-model probe Resolve without tracing call stacks. New purposes
// are added here as new callers wire up; the existing set is the union of
// every resolveSecret call site today.
const (
	secretPurposeVCenterConnect     = "vcenter.connect"      // sync + content-library + agent VM access
	secretPurposeProviderModelProbe = "provider_model.probe" // provider-health periodic + auto-probe after upsert
	secretPurposeGatewayMaster      = "gateway.master_key"   // per-request master-key fetch
	secretPurposeAgentUIRotate      = "agent.ui_rotate"      // daemon-driven UI password rotation
)

// resolveSecret is the audited wrapper around Secrets.Resolve. It transparently
// calls the resolver, and on success + when SecretsAuditEnabled is set, records
// an audit_log row tagged with `purpose` so the unseal is traceable per
// request. Decryption failures are NOT audited here — a failed Resolve is
// usually a programming/config error (bad ref), not a security event, and
// auditing them on a hot path floods the audit table.
//
// `purpose` is REQUIRED and should be one of the secretPurpose* constants —
// the audit reader filters by it.
func (r *Resolver) resolveSecret(ctx context.Context, ref, purpose string) (secrets.Credential, error) {
	if r.Secrets == nil {
		return secrets.Credential{}, nil
	}
	cred, err := r.Secrets.Resolve(ctx, ref)
	if err != nil {
		return cred, err
	}
	if r.SecretsAuditEnabled {
		r.auditSecretRead(ctx, ref, purpose)
	}
	return cred, nil
}

// auditSecretRead writes one audit_log row per successful Resolve. Uses the
// existing audit() shape (action, resource_type, resource_id, actor, ip) and
// stuffs the purpose into detail so the JSON-shaped audit_filter reader
// (LLD-15) can pull it back. Failures are logged, never propagated — a
// broken audit write must not break the underlying Resolve that already
// succeeded.
func (r *Resolver) auditSecretRead(ctx context.Context, ref, purpose string) {
	c := r.Ent.AuditLog.Create().
		SetAction("secret.read").
		SetResourceType("secret").
		SetResourceID(secretRefDisplay(ref)). // masked; full ref never lands in audit
		SetResult(auditlog.ResultSuccess).
		SetDetail(map[string]any{"purpose": purpose})
	if cu := auth.FromContext(ctx); cu != nil {
		if id, err := uuid.Parse(cu.ID); err == nil {
			c.SetActorUserID(id)
		}
	}
	if ip := clientIP(ctx); ip != "" {
		c.SetIP(ip)
	}
	if _, err := c.Save(ctx); err != nil {
		log.Printf("audit write failed: action=secret.read purpose=%s err=%v", purpose, err)
	}
}
