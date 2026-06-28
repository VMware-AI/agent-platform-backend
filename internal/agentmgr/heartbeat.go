package agentmgr

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agentheartbeat"
	"github.com/VMware-AI/agent-platform-backend/ent/rotationcommand"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

// Ack is a daemon's report on a previously dispatched command.
type Ack struct {
	CommandID         string `json:"command_id"`
	Phase             string `json:"phase"` // acked | completed | failed
	ResultFingerprint string `json:"result_fingerprint,omitempty"`
	NewUIPassword     string `json:"new_ui_password,omitempty"` // rotate_ui_password completion only
	Error             string `json:"error,omitempty"`
}

// HeartbeatRequest is the daemon → backend payload (LLD-08 §5.2).
type HeartbeatRequest struct {
	ReportedAt    time.Time      `json:"reported_at"`
	Status        string         `json:"status"` // ok | degraded | error
	AgentVersion  string         `json:"agent_version,omitempty"`
	RotationState string         `json:"rotation_state,omitempty"` // idle | rotating | failed
	Acks          []Ack          `json:"acks,omitempty"`
	Detail        map[string]any `json:"detail,omitempty"`
}

// Command is a backend → daemon instruction carried in a heartbeat response.
type Command struct {
	CommandID string         `json:"command_id"`
	Kind      string         `json:"kind"`
	Reason    string         `json:"reason,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
}

// HeartbeatResponse is the backend → daemon payload (LLD-08 §5.2).
type HeartbeatResponse struct {
	NextHeartbeatSecs int       `json:"next_heartbeat_secs"`
	VMToken           string    `json:"vm_token,omitempty"` // set only on sliding renewal
	Commands          []Command `json:"commands"`
}

// defaultHeartbeatSecs is the fixed cadence (M1: no dynamic backpressure).
const defaultHeartbeatSecs = 60

// uiPasswordMinLength is passed to the daemon for rotate_ui_password.
const uiPasswordMinLength = 24

// ProcessHeartbeat records the heartbeat, advances command state machines from
// the daemon's acks (idempotent, single-direction), applies the max-age rotation
// policy, re-dispatches timed-out commands, and returns the commands to run plus
// an optionally renewed VM token (LLD-08 §5.3). The heartbeat itself is not
// audited (high-frequency noise); command/token/secret changes are.
func (s *Service) ProcessHeartbeat(ctx context.Context, enr *ent.AgentEnrollment, req HeartbeatRequest) (HeartbeatResponse, error) {
	now := s.clock()

	// 1) Append the health row + refresh last_seen_at.
	hb := s.Ent.AgentHeartbeat.Create().
		SetAgentID(enr.AgentID).
		SetReportedAt(nonZeroTime(req.ReportedAt, now)).
		SetReceivedAt(now).
		SetStatus(heartbeatStatus(req.Status))
	if req.AgentVersion != "" {
		hb.SetAgentVersion(req.AgentVersion)
	}
	if rs := rotationState(req.RotationState); rs != "" {
		hb.SetRotationState(rs)
	}
	if len(req.Detail) > 0 {
		hb.SetDetail(req.Detail)
	}
	if _, err := hb.Save(ctx); err != nil {
		return HeartbeatResponse{}, err
	}
	if _, err := enr.Update().SetLastSeenAt(now).Save(ctx); err != nil {
		return HeartbeatResponse{}, err
	}

	// 2) Apply the daemon's acks (idempotent state-machine advance).
	for _, ack := range req.Acks {
		s.applyAck(ctx, enr.AgentID, ack, now)
	}

	// 3) Re-dispatch commands stuck in 'dispatched' past the timeout → pending.
	if _, err := s.Ent.RotationCommand.Update().
		Where(
			rotationcommand.AgentID(enr.AgentID),
			rotationcommand.StatusEQ(rotationcommand.StatusDispatched),
			rotationcommand.DispatchedAtLT(now.Add(-s.dispatchTimeout())),
		).
		SetStatus(rotationcommand.StatusPending).
		Save(ctx); err != nil {
		log.Printf("agentmgr: redispatch sweep failed for %s: %v", enr.AgentID, err)
	}

	// 4) max-age policy: enqueue a UI-password rotation if overdue and none in flight.
	s.applyMaxAge(ctx, enr)

	// 5+6) Dispatch pending commands and assemble the response (incl. sliding
	// VM-token renewal).
	return s.dispatchAndAssemble(ctx, enr, now)
}

// dispatchAndAssemble selects pending commands → marks them dispatched → returns
// them, then layers on a sliding VM-token renewal when the current token nears
// expiry (LLD-08 §5.3 steps 5+6). Extracted from ProcessHeartbeat verbatim.
func (s *Service) dispatchAndAssemble(ctx context.Context, enr *ent.AgentEnrollment, now time.Time) (HeartbeatResponse, error) {
	// 5) Select pending commands → mark dispatched → return them.
	pending, err := s.Ent.RotationCommand.Query().
		Where(rotationcommand.AgentID(enr.AgentID), rotationcommand.StatusEQ(rotationcommand.StatusPending)).
		All(ctx)
	if err != nil {
		return HeartbeatResponse{}, err
	}
	resp := HeartbeatResponse{NextHeartbeatSecs: defaultHeartbeatSecs, Commands: []Command{}}
	for _, cmd := range pending {
		n, err := s.Ent.RotationCommand.Update().
			Where(rotationcommand.ID(cmd.ID), rotationcommand.StatusEQ(rotationcommand.StatusPending)).
			SetStatus(rotationcommand.StatusDispatched).SetDispatchedAt(now).
			Save(ctx)
		if err != nil || n == 0 {
			continue // another concurrent heartbeat grabbed it
		}
		resp.Commands = append(resp.Commands, Command{
			CommandID: cmd.CommandID,
			Kind:      string(cmd.Kind),
			Reason:    cmd.Reason,
			Params:    map[string]any{"min_length": uiPasswordMinLength},
		})
	}

	// 6) Sliding VM-token renewal when nearing expiry (LLD-08 §4.4).
	if tok := s.maybeRenew(ctx, enr, now); tok != "" {
		resp.VMToken = tok
	}
	return resp, nil
}

// applyAck advances one command's state machine. Single-direction; terminal
// states ignore further acks (idempotent). Completion of a UI-password rotation
// is publish-then-commit: the new secret must land in the store before the
// command is marked completed.
func (s *Service) applyAck(ctx context.Context, agentID uuid.UUID, ack Ack, now time.Time) {
	if ack.CommandID == "" {
		return
	}
	cmd, err := s.Ent.RotationCommand.Query().
		Where(rotationcommand.CommandID(ack.CommandID), rotationcommand.AgentID(agentID)).Only(ctx)
	if err != nil {
		return // unknown command — ignore
	}
	switch ack.Phase {
	case "acked":
		_, _ = s.Ent.RotationCommand.Update().
			Where(rotationcommand.ID(cmd.ID), rotationcommand.StatusEQ(rotationcommand.StatusDispatched)).
			SetStatus(rotationcommand.StatusAcked).SetAckedAt(now).Save(ctx)
	case "failed":
		n, _ := s.Ent.RotationCommand.Update().
			Where(rotationcommand.ID(cmd.ID), rotationcommand.StatusIn(
				rotationcommand.StatusDispatched, rotationcommand.StatusAcked)).
			SetStatus(rotationcommand.StatusFailed).SetError(ack.Error).Save(ctx)
		if n > 0 {
			s.audit(ctx, "rotation.failed", "rotation_command", cmd.ID.String(), false, "vm")
		}
	case "completed":
		s.completeCommand(ctx, cmd, ack, now)
	}
}

// completeCommand finalizes a command. For a UI-password rotation carrying a new
// plaintext password, it writes the secret to the store FIRST (publish-then-
// commit): only on success is the command marked completed with the secret_ref.
// If the store write fails the command stays acked and the daemon retries.
func (s *Service) completeCommand(ctx context.Context, cmd *ent.RotationCommand, ack Ack, now time.Time) {
	if cmd.Status == rotationcommand.StatusCompleted || cmd.Status == rotationcommand.StatusFailed {
		return // terminal — idempotent ignore
	}
	upd := s.Ent.RotationCommand.Update().
		Where(rotationcommand.ID(cmd.ID), rotationcommand.StatusIn(
			rotationcommand.StatusDispatched, rotationcommand.StatusAcked)).
		SetStatus(rotationcommand.StatusCompleted).SetCompletedAt(now)
	if ack.ResultFingerprint != "" {
		upd.SetResultFingerprint(ack.ResultFingerprint)
	}
	if cmd.Kind == rotationcommand.KindRotateUIPassword && ack.NewUIPassword != "" {
		if s.Secrets == nil {
			return // cannot persist — leave acked, daemon retries
		}
		ref, err := s.Secrets.Put(ctx, "agent-ui/"+cmd.AgentID.String(),
			secrets.Credential{Username: "agent", Password: ack.NewUIPassword})
		if err != nil {
			log.Printf("agentmgr: vault put failed for %s, leaving acked: %v", cmd.ID, err)
			return // publish-then-commit: do NOT complete
		}
		upd.SetSecretRef(ref)
	}
	if n, _ := upd.Save(ctx); n > 0 {
		s.audit(ctx, "rotation.completed", "rotation_command", cmd.ID.String(), true, "vm")
	}
}

// applyMaxAge enqueues a UI-password rotation when the last one is older than the
// policy and none is in flight (LLD-08 §5.1).
func (s *Service) applyMaxAge(ctx context.Context, enr *ent.AgentEnrollment) {
	has, err := s.hasInFlight(ctx, enr.AgentID, rotationcommand.KindRotateUIPassword)
	if err != nil || has {
		return
	}
	last := s.lastRotation(ctx, enr)
	if s.clock().Sub(last) <= s.maxAge() {
		return
	}
	if _, err := s.createCommand(ctx, enr.AgentID, rotationcommand.KindRotateUIPassword, "max_age"); err != nil {
		log.Printf("agentmgr: max-age command create failed for %s: %v", enr.AgentID, err)
	}
}

// lastRotation returns when the agent's UI password was last rotated — the most
// recent completed rotate_ui_password, or the enrollment's creation time.
func (s *Service) lastRotation(ctx context.Context, enr *ent.AgentEnrollment) time.Time {
	c, err := s.Ent.RotationCommand.Query().
		Where(
			rotationcommand.AgentID(enr.AgentID),
			rotationcommand.KindEQ(rotationcommand.KindRotateUIPassword),
			rotationcommand.StatusEQ(rotationcommand.StatusCompleted),
		).
		Order(ent.Desc(rotationcommand.FieldCompletedAt)).First(ctx)
	if err == nil && c.CompletedAt != nil {
		return *c.CompletedAt
	}
	return enr.CreatedAt
}

func (s *Service) hasInFlight(ctx context.Context, agentID uuid.UUID, kind rotationcommand.Kind) (bool, error) {
	return s.Ent.RotationCommand.Query().
		Where(
			rotationcommand.AgentID(agentID),
			rotationcommand.KindEQ(kind),
			rotationcommand.StatusIn(
				rotationcommand.StatusPending,
				rotationcommand.StatusDispatched,
				rotationcommand.StatusAcked,
			),
		).Exist(ctx)
}

func (s *Service) createCommand(ctx context.Context, agentID uuid.UUID, kind rotationcommand.Kind, reason string) (*ent.RotationCommand, error) {
	cmd, err := s.Ent.RotationCommand.Create().
		SetCommandID(uuid.New().String()).
		SetAgentID(agentID).SetKind(kind).SetReason(reason).
		SetStatus(rotationcommand.StatusPending).Save(ctx)
	if err != nil {
		// Lost the race: the partial unique index (agent_id, kind WHERE status in
		// flight) rejected a duplicate. That's the desired "already in flight"
		// outcome, not an error — return (nil, nil) so callers no-op, closing the
		// EXISTS-then-INSERT TOCTOU.
		if ent.IsConstraintError(err) {
			return nil, nil
		}
		return nil, err
	}
	return cmd, nil
}

// maybeRenew issues a fresh VM token when the current one nears expiry, returning
// the new plaintext (empty if no renewal). Old token stops working immediately.
func (s *Service) maybeRenew(ctx context.Context, enr *ent.AgentEnrollment, now time.Time) string {
	if enr.VMTokenExpiresAt == nil || now.Before(enr.VMTokenExpiresAt.Add(-s.renewWithin())) {
		return ""
	}
	tok, err := newToken()
	if err != nil {
		return ""
	}
	hash, err := hashToken(tok)
	if err != nil {
		return ""
	}
	if _, err := enr.Update().
		SetVMTokenHash(hash).SetVMTokenIssuedAt(now).SetVMTokenExpiresAt(now.Add(s.vmTokenTTL())).
		Save(ctx); err != nil {
		return ""
	}
	s.audit(ctx, "agent.token.renew", "agent_enrollment", enr.ID.String(), true, "vm")
	return tok
}

func nonZeroTime(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}

func heartbeatStatus(s string) agentheartbeat.Status {
	switch s {
	case "degraded":
		return agentheartbeat.StatusDegraded
	case "error":
		return agentheartbeat.StatusError
	default:
		return agentheartbeat.StatusOk
	}
}

func rotationState(s string) agentheartbeat.RotationState {
	switch s {
	case "idle":
		return agentheartbeat.RotationStateIdle
	case "rotating":
		return agentheartbeat.RotationStateRotating
	case "failed":
		return agentheartbeat.RotationStateFailed
	default:
		return ""
	}
}
