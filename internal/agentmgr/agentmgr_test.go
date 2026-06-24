package agentmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/rotationcommand"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

// failStore is a secrets.Store that always fails (publish-then-commit test).
type failStore struct{}

func (failStore) Put(context.Context, string, secrets.Credential) (string, error) {
	return "", errors.New("vault down")
}

func newTestService(t *testing.T) (*Service, *time.Time, func()) {
	t.Helper()
	client, err := store.Open(context.Background(), "", true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	// Base the fake clock on real now: ent stamps enrollment created_at with the
	// real wall clock (TimeMixin), and applyMaxAge compares it against this clock.
	// A hardcoded past date becomes a time-bomb — once wall-clock passes it, a fresh
	// enrollment's created_at sorts *after* the fake "now+maxAge" and the max-age
	// rotation never fires. Tests only ever advance via relative *clk.Add(...).
	clk := time.Now().UTC()
	svc := &Service{
		Ent:     client,
		Secrets: secrets.NewStaticResolver(nil),
		now:     func() time.Time { return clk },
	}
	return svc, &clk, func() { _ = client.Close() }
}

func enrolled(t *testing.T, svc *Service) (agentID uuid.UUID, vmID, vmToken string) {
	t.Helper()
	ctx := context.Background()
	agentID = uuid.New()
	vmID = "vm-" + agentID.String()[:8]
	enrollTok, err := svc.IssueEnrollment(ctx, agentID, vmID, nil)
	if err != nil {
		t.Fatalf("IssueEnrollment: %v", err)
	}
	vmToken, err = svc.Enroll(ctx, vmID, enrollTok)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return agentID, vmID, vmToken
}

func TestEnroll_ExchangeOnceAndExpiry(t *testing.T) {
	svc, clk, done := newTestService(t)
	defer done()
	ctx := context.Background()

	agentID := uuid.New()
	tok, err := svc.IssueEnrollment(ctx, agentID, "vm-1", nil)
	if err != nil {
		t.Fatalf("IssueEnrollment: %v", err)
	}
	// A2: valid exchange → vm token, enrollment active
	vmTok, err := svc.Enroll(ctx, "vm-1", tok)
	if err != nil || vmTok == "" {
		t.Fatalf("Enroll: tok=%q err=%v", vmTok, err)
	}
	// A3: one-time — second exchange fails
	if _, err := svc.Enroll(ctx, "vm-1", tok); !errors.Is(err, ErrAuth) {
		t.Fatalf("replayed enroll should be ErrAuth, got %v", err)
	}

	// A4: expiry — a fresh pending enrollment past TTL cannot exchange
	tok2, _ := svc.IssueEnrollment(ctx, uuid.New(), "vm-2", nil)
	*clk = clk.Add(DefaultEnrollTTL + time.Minute)
	if _, err := svc.Enroll(ctx, "vm-2", tok2); !errors.Is(err, ErrAuth) {
		t.Fatalf("expired enroll should be ErrAuth, got %v", err)
	}
}

func TestAuthenticate_FailClosed(t *testing.T) {
	svc, _, done := newTestService(t)
	defer done()
	ctx := context.Background()
	agentID, vmID, vmToken := enrolled(t, svc)

	// valid
	if _, err := svc.Authenticate(ctx, vmID, vmToken); err != nil {
		t.Fatalf("valid auth failed: %v", err)
	}
	// A5: wrong token, unknown vm, revoked → all ErrAuth
	if _, err := svc.Authenticate(ctx, vmID, "wrong"); !errors.Is(err, ErrAuth) {
		t.Fatal("wrong token should be ErrAuth")
	}
	if _, err := svc.Authenticate(ctx, "nope", vmToken); !errors.Is(err, ErrAuth) {
		t.Fatal("unknown vm should be ErrAuth")
	}
	// A14: revoke → 401
	if err := svc.Revoke(ctx, agentID, "admin"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := svc.Authenticate(ctx, vmID, vmToken); !errors.Is(err, ErrAuth) {
		t.Fatal("revoked token should be ErrAuth")
	}
}

func TestHeartbeat_RecordsAndDispatches(t *testing.T) {
	svc, _, done := newTestService(t)
	defer done()
	ctx := context.Background()
	agentID, vmID, vmToken := enrolled(t, svc)

	enr, err := svc.Authenticate(ctx, vmID, vmToken)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	// A7: a pending command is dispatched in the response
	cmd, err := svc.RequestRotation(ctx, agentID, rotationcommand.KindRotateUIPassword, "manual", "admin")
	if err != nil || cmd == nil {
		t.Fatalf("RequestRotation: %v", err)
	}
	resp, err := svc.ProcessHeartbeat(ctx, enr, HeartbeatRequest{Status: "ok"})
	if err != nil {
		t.Fatalf("ProcessHeartbeat: %v", err)
	}
	if len(resp.Commands) != 1 || resp.Commands[0].CommandID != cmd.CommandID {
		t.Fatalf("expected dispatched command, got %+v", resp.Commands)
	}
	// A6: heartbeat row written + last_seen set
	if n := svc.Ent.AgentHeartbeat.Query().CountX(ctx); n != 1 {
		t.Fatalf("expected 1 heartbeat row, got %d", n)
	}
	got := svc.Ent.RotationCommand.GetX(ctx, cmd.ID)
	if got.Status != rotationcommand.StatusDispatched {
		t.Fatalf("command status = %s, want dispatched", got.Status)
	}
}

func TestHeartbeat_AckStateMachineAndSecret(t *testing.T) {
	svc, _, done := newTestService(t)
	defer done()
	ctx := context.Background()
	agentID, vmID, vmToken := enrolled(t, svc)
	enr, _ := svc.Authenticate(ctx, vmID, vmToken)

	cmd, _ := svc.RequestRotation(ctx, agentID, rotationcommand.KindRotateUIPassword, "manual", "admin")
	if _, err := svc.ProcessHeartbeat(ctx, enr, HeartbeatRequest{Status: "ok"}); err != nil {
		t.Fatalf("dispatch heartbeat: %v", err)
	}

	// A9: acked
	if _, err := svc.ProcessHeartbeat(ctx, enr, HeartbeatRequest{
		Status: "ok", Acks: []Ack{{CommandID: cmd.CommandID, Phase: "acked"}},
	}); err != nil {
		t.Fatalf("ack heartbeat: %v", err)
	}
	if svc.Ent.RotationCommand.GetX(ctx, cmd.ID).Status != rotationcommand.StatusAcked {
		t.Fatal("command should be acked")
	}

	// A11: completed with new password → secret stored, command completed, ref set
	if _, err := svc.ProcessHeartbeat(ctx, enr, HeartbeatRequest{
		Status: "ok",
		Acks: []Ack{{CommandID: cmd.CommandID, Phase: "completed",
			ResultFingerprint: "sha256:abc", NewUIPassword: "s3cr3t-pw-value"}},
	}); err != nil {
		t.Fatalf("complete heartbeat: %v", err)
	}
	done2 := svc.Ent.RotationCommand.GetX(ctx, cmd.ID)
	if done2.Status != rotationcommand.StatusCompleted {
		t.Fatalf("command status = %s, want completed", done2.Status)
	}
	if done2.SecretRef == "" {
		t.Fatal("secret_ref not set on completion")
	}
	// the plaintext password must NOT appear in any DB column we persisted
	if done2.SecretRef == "s3cr3t-pw-value" || done2.ResultFingerprint == "s3cr3t-pw-value" {
		t.Fatal("plaintext password leaked into DB")
	}
	// A10: idempotent — repeating the completed ack does not error or regress
	if _, err := svc.ProcessHeartbeat(ctx, enr, HeartbeatRequest{
		Status: "ok", Acks: []Ack{{CommandID: cmd.CommandID, Phase: "completed"}},
	}); err != nil {
		t.Fatalf("repeat completed ack: %v", err)
	}
	if svc.Ent.RotationCommand.GetX(ctx, cmd.ID).Status != rotationcommand.StatusCompleted {
		t.Fatal("idempotency: status regressed")
	}
}

func TestHeartbeat_PublishThenCommit_StoreFailure(t *testing.T) {
	svc, _, done := newTestService(t)
	defer done()
	svc.Secrets = failStore{}
	ctx := context.Background()
	agentID, vmID, vmToken := enrolled(t, svc)
	enr, _ := svc.Authenticate(ctx, vmID, vmToken)

	cmd, _ := svc.RequestRotation(ctx, agentID, rotationcommand.KindRotateUIPassword, "manual", "admin")
	_, _ = svc.ProcessHeartbeat(ctx, enr, HeartbeatRequest{Status: "ok"})
	_, _ = svc.ProcessHeartbeat(ctx, enr, HeartbeatRequest{Status: "ok", Acks: []Ack{{CommandID: cmd.CommandID, Phase: "acked"}}})

	// A12: store write fails → command stays acked (not completed), so the daemon retries
	_, _ = svc.ProcessHeartbeat(ctx, enr, HeartbeatRequest{
		Status: "ok", Acks: []Ack{{CommandID: cmd.CommandID, Phase: "completed", NewUIPassword: "pw"}},
	})
	if svc.Ent.RotationCommand.GetX(ctx, cmd.ID).Status != rotationcommand.StatusAcked {
		t.Fatal("on store failure the command must remain acked (publish-then-commit)")
	}
}

func TestHeartbeat_MaxAgeAutoCreatesRotation(t *testing.T) {
	svc, clk, done := newTestService(t)
	defer done()
	svc.MaxAge = 24 * time.Hour
	ctx := context.Background()
	_, vmID, vmToken := enrolled(t, svc)
	enr, _ := svc.Authenticate(ctx, vmID, vmToken)

	// advance past max-age → next heartbeat auto-enqueues a rotate_ui_password
	*clk = clk.Add(48 * time.Hour)
	resp, err := svc.ProcessHeartbeat(ctx, enr, HeartbeatRequest{Status: "ok"})
	if err != nil {
		t.Fatalf("ProcessHeartbeat: %v", err)
	}
	if len(resp.Commands) != 1 || resp.Commands[0].Reason != "max_age" {
		t.Fatalf("A8: expected auto max_age command, got %+v", resp.Commands)
	}
}

func TestHeartbeat_SlidingTokenRenewal(t *testing.T) {
	svc, clk, done := newTestService(t)
	defer done()
	ctx := context.Background()
	_, vmID, vmToken := enrolled(t, svc)
	enr, _ := svc.Authenticate(ctx, vmID, vmToken)

	// jump to within the renewal window before expiry
	*clk = clk.Add(DefaultVMTokenTTL - DefaultRenewWithin + time.Hour)
	resp, err := svc.ProcessHeartbeat(ctx, enr, HeartbeatRequest{Status: "ok"})
	if err != nil {
		t.Fatalf("ProcessHeartbeat: %v", err)
	}
	if resp.VMToken == "" {
		t.Fatal("A13: expected a renewed vm_token")
	}
	// old token no longer authenticates; new one does
	if _, err := svc.Authenticate(ctx, vmID, vmToken); !errors.Is(err, ErrAuth) {
		t.Fatal("old token should stop working after renewal")
	}
	if _, err := svc.Authenticate(ctx, vmID, resp.VMToken); err != nil {
		t.Fatalf("renewed token should authenticate: %v", err)
	}
}

// TestHTTP_EnrollAndHeartbeat_NoCSRF proves the daemon endpoints work without any
// Origin header or cookie (A1): they are not behind the CSRF/session middleware.
func TestHTTP_EnrollAndHeartbeat_NoCSRF(t *testing.T) {
	svc, _, done := newTestService(t)
	defer done()
	ctx := context.Background()
	agentID := uuid.New()
	enrollTok, _ := svc.IssueEnrollment(ctx, agentID, "vm-http", nil)
	srv := httptest.NewServer(Handler(svc))
	defer srv.Close()

	// enroll (no Origin, no cookie) → 200 + vm_token
	vmToken := postEnroll(t, srv.URL, "vm-http", enrollTok)

	// heartbeat with the vm token → 200
	body, _ := json.Marshal(HeartbeatRequest{Status: "ok"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/agents/vm-http/heartbeat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+vmToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("heartbeat req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status = %d, want 200 (no CSRF/session gate)", resp.StatusCode)
	}

	// bad token → 401 (fail-closed)
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/agents/vm-http/heartbeat", bytes.NewReader(body))
	req2.Header.Set("Authorization", "Bearer nope")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("bad-token heartbeat req: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad-token heartbeat = %d, want 401", resp2.StatusCode)
	}
}

func postEnroll(t *testing.T, base, vmID, enrollTok string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/agents/"+vmID+"/enroll", nil)
	req.Header.Set("Authorization", "Bearer "+enrollTok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("enroll req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enroll status = %d, want 200", resp.StatusCode)
	}
	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode enroll: %v", err)
	}
	if out["vm_token"] == "" {
		t.Fatal("enroll returned empty vm_token")
	}
	return out["vm_token"]
}
