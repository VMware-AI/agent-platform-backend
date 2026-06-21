package agentmgr

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agentenrollment"
	"github.com/VMware-AI/agent-platform-backend/ent/auditlog"
	"github.com/VMware-AI/agent-platform-backend/ent/rotationcommand"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

// ErrAuth is the single, opaque authentication failure (fail-closed: callers
// must not distinguish missing/expired/revoked/mismatch to avoid probing).
var ErrAuth = errors.New("agentmgr: authentication failed")

// Defaults for the credential + rotation policy (LLD-08 §4/§5).
const (
	DefaultEnrollTTL       = 15 * time.Minute
	DefaultVMTokenTTL      = 90 * 24 * time.Hour
	DefaultRenewWithin     = 30 * 24 * time.Hour // sliding renewal threshold
	DefaultMaxAge          = 30 * 24 * time.Hour // UI password rotation cadence
	DefaultDispatchTimeout = 5 * time.Minute     // pending re-investment
)

// Service is the agent-manager backend. It owns enrollment, authentication,
// heartbeat processing and rotation-command dispatch.
type Service struct {
	Ent     *ent.Client
	Secrets secrets.Store // nil → rotation completions cannot persist (left acked)

	EnrollTTL       time.Duration
	VMTokenTTL      time.Duration
	RenewWithin     time.Duration
	MaxAge          time.Duration
	DispatchTimeout time.Duration

	// now is injectable for deterministic tests; nil → time.Now.
	now func() time.Time
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) enrollTTL() time.Duration   { return orDur(s.EnrollTTL, DefaultEnrollTTL) }
func (s *Service) vmTokenTTL() time.Duration  { return orDur(s.VMTokenTTL, DefaultVMTokenTTL) }
func (s *Service) renewWithin() time.Duration { return orDur(s.RenewWithin, DefaultRenewWithin) }
func (s *Service) maxAge() time.Duration      { return orDur(s.MaxAge, DefaultMaxAge) }
func (s *Service) dispatchTimeout() time.Duration {
	return orDur(s.DispatchTimeout, DefaultDispatchTimeout)
}

func orDur(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}

// IssueEnrollment mints a one-time enroll token for an agent's VM and records a
// pending AgentEnrollment (LLD-08 §4.2). Called by deploy; the plaintext token is
// returned for guestinfo injection and never stored. Re-issuing (redeploy) resets
// the row to pending with a fresh token.
func (s *Service) IssueEnrollment(ctx context.Context, agentID uuid.UUID, vmID string, tenantID *uuid.UUID) (string, error) {
	if vmID == "" {
		return "", fmt.Errorf("agentmgr: vmID required")
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	hash, err := hashToken(token)
	if err != nil {
		return "", err
	}
	exp := s.clock().Add(s.enrollTTL())

	existing, err := s.Ent.AgentEnrollment.Query().Where(agentenrollment.AgentID(agentID)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		c := s.Ent.AgentEnrollment.Create().
			SetAgentID(agentID).SetVMID(vmID).SetStatus(agentenrollment.StatusPending).
			SetEnrollTokenHash(hash).SetEnrollExpiresAt(exp)
		if tenantID != nil {
			c.SetTenantID(*tenantID)
		}
		if _, err := c.Save(ctx); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	default:
		// Redeploy: reset to pending, clear any old VM token.
		_, err := existing.Update().
			SetVMID(vmID).SetStatus(agentenrollment.StatusPending).
			SetEnrollTokenHash(hash).SetEnrollExpiresAt(exp).
			ClearVMTokenHash().ClearVMTokenIssuedAt().ClearVMTokenExpiresAt().
			Save(ctx)
		if err != nil {
			return "", err
		}
	}
	return token, nil
}

// Enroll exchanges a valid one-time enroll token for a long-lived VM bearer
// token (LLD-08 §4.2). The enroll token is invalidated on success (one-time).
// All failures return ErrAuth (fail-closed, opaque).
func (s *Service) Enroll(ctx context.Context, vmID, enrollToken string) (string, error) {
	enr, err := s.Ent.AgentEnrollment.Query().Where(agentenrollment.VMID(vmID)).Only(ctx)
	if err != nil {
		return "", ErrAuth
	}
	now := s.clock()
	if enr.Status != agentenrollment.StatusPending || now.After(enr.EnrollExpiresAt) ||
		!verifyToken(enr.EnrollTokenHash, enrollToken) {
		return "", ErrAuth
	}
	vmToken, err := newToken()
	if err != nil {
		return "", err
	}
	vmHash, err := hashToken(vmToken)
	if err != nil {
		return "", err
	}
	exp := now.Add(s.vmTokenTTL())
	// One-time: clear the enroll hash so a replayed exchange fails.
	if _, err := enr.Update().
		SetStatus(agentenrollment.StatusActive).
		ClearEnrollTokenHash().
		SetVMTokenHash(vmHash).SetVMTokenIssuedAt(now).SetVMTokenExpiresAt(exp).
		Save(ctx); err != nil {
		return "", err
	}
	s.audit(ctx, "agent.enroll", "agent_enrollment", enr.ID.String(), true, "vm:"+vmID)
	return vmToken, nil
}

// Authenticate verifies a VM bearer token for a heartbeat (LLD-08 §4.3). Returns
// the active enrollment or ErrAuth (opaque, fail-closed).
func (s *Service) Authenticate(ctx context.Context, vmID, vmToken string) (*ent.AgentEnrollment, error) {
	enr, err := s.Ent.AgentEnrollment.Query().Where(agentenrollment.VMID(vmID)).Only(ctx)
	if err != nil {
		return nil, ErrAuth
	}
	now := s.clock()
	expired := enr.VMTokenExpiresAt != nil && now.After(*enr.VMTokenExpiresAt)
	if enr.Status != agentenrollment.StatusActive || expired || !verifyToken(enr.VMTokenHash, vmToken) {
		return nil, ErrAuth
	}
	return enr, nil
}

// Revoke disables a VM's credential (recycle / suspected leak, LLD-08 §4.4):
// next heartbeat 401s. Idempotent.
func (s *Service) Revoke(ctx context.Context, agentID uuid.UUID, actorID string) error {
	n, err := s.Ent.AgentEnrollment.Update().
		Where(agentenrollment.AgentID(agentID)).
		SetStatus(agentenrollment.StatusRevoked).ClearVMTokenHash().ClearEnrollTokenHash().
		Save(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		s.audit(ctx, "agent.enrollment.revoke", "agent", agentID.String(), true, actorID)
	}
	return nil
}

// audit writes an AuditLog row for a security-relevant agent-manager event
// (command state change, token rotation, secret write, revoke). Heartbeats
// themselves are not audited (high-frequency noise). Best-effort: a failed audit
// is logged, never fatal (mirrors the graph audit helper). actorID "vm"/"vm:<id>"
// marks daemon-originated events that have no platform user.
func (s *Service) audit(ctx context.Context, action, resType, resID string, ok bool, actorID string) {
	res := auditlog.ResultSuccess
	if !ok {
		res = auditlog.ResultFail
	}
	c := s.Ent.AuditLog.Create().
		SetAction(action).SetResourceType(resType).SetResourceID(resID).SetResult(res)
	if id, err := uuid.Parse(actorID); err == nil {
		c.SetActorUserID(id)
	}
	if _, err := c.Save(ctx); err != nil {
		log.Printf("agentmgr: audit write failed: action=%s err=%v", action, err)
	}
}

// RequestRotation enqueues a manual rotation command (admin-triggered, LLD-08
// §5.1). Skips if an in-flight command of the same kind already exists.
func (s *Service) RequestRotation(ctx context.Context, agentID uuid.UUID, kind rotationcommand.Kind, reason, actorID string) (*ent.RotationCommand, error) {
	if has, err := s.hasInFlight(ctx, agentID, kind); err != nil || has {
		return nil, err
	}
	cmd, err := s.createCommand(ctx, agentID, kind, reason)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, "rotation.request", "rotation_command", cmd.ID.String(), true, actorID)
	return cmd, nil
}
