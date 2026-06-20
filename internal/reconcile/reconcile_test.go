package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

// fakeKeyGateway is an in-memory Gateway for reconciler tests.
type fakeKeyGateway struct {
	keys         []gateway.KeyInfo
	listErr      error
	deleted      []string
	delErr       error
	teams        []gateway.TeamInfo
	listTeamErr  error
	deletedTeams []string
}

func (f *fakeKeyGateway) ListKeys(context.Context) ([]gateway.KeyInfo, error) {
	return f.keys, f.listErr
}
func (f *fakeKeyGateway) DeleteKey(_ context.Context, key string) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = append(f.deleted, key)
	return nil
}
func (f *fakeKeyGateway) ListTeams(context.Context) ([]gateway.TeamInfo, error) {
	return f.teams, f.listTeamErr
}
func (f *fakeKeyGateway) DeleteTeam(_ context.Context, teamID string) error {
	f.deletedTeams = append(f.deletedTeams, teamID)
	return nil
}

func newDB(t *testing.T) (*ent.Client, func()) {
	t.Helper()
	c, err := store.Open(context.Background(), "", true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return c, func() { _ = c.Close() }
}

func mkKey(t *testing.T, c *ent.Client, litellmKey string, status virtualkey.Status) *ent.VirtualKey {
	t.Helper()
	vk, err := c.VirtualKey.Create().
		SetLitellmKey(litellmKey).
		SetUserID(uuid.New()).
		SetStatus(status).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create key %s: %v", litellmKey, err)
	}
	return vk
}

// Default mode is report-only: discrepancies are found but nothing is mutated.
func TestReconcileKeys_ReportOnly(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	mkKey(t, db, "sk-A", virtualkey.StatusActive)          // governed, present at gateway
	stale := mkKey(t, db, "sk-C", virtualkey.StatusActive) // active but vanished at gateway

	gw := &fakeKeyGateway{keys: []gateway.KeyInfo{
		{Key: "sk-A"}, // matches DB
		{Key: "sk-B"}, // orphan: no DB row
	}}

	r := &Reconciler{Ent: db, Gateway: gw, Prune: false}
	rep, err := r.ReconcileKeys(ctx)
	if err != nil {
		t.Fatalf("ReconcileKeys: %v", err)
	}
	if len(rep.GatewayOrphans) != 1 || rep.GatewayOrphans[0] != "sk-B" {
		t.Errorf("GatewayOrphans = %v, want [sk-B]", rep.GatewayOrphans)
	}
	if len(rep.StaleRows) != 1 || rep.StaleRows[0] != stale.ID.String() {
		t.Errorf("StaleRows = %v, want [%s]", rep.StaleRows, stale.ID)
	}
	if rep.Pruned != 0 || rep.Revoked != 0 {
		t.Errorf("report-only must not mutate: pruned=%d revoked=%d", rep.Pruned, rep.Revoked)
	}
	if len(gw.deleted) != 0 {
		t.Errorf("report-only must not call DeleteKey: %v", gw.deleted)
	}
	got := db.VirtualKey.GetX(ctx, stale.ID)
	if got.Status != virtualkey.StatusActive {
		t.Errorf("report-only must not flip stale row: status=%s", got.Status)
	}
}

// Prune mode revokes stale DB rows and deletes gateway orphans.
func TestReconcileKeys_Prune(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	mkKey(t, db, "sk-A", virtualkey.StatusActive)
	stale := mkKey(t, db, "sk-C", virtualkey.StatusActive)

	gw := &fakeKeyGateway{keys: []gateway.KeyInfo{{Key: "sk-A"}, {Key: "sk-B"}}}
	r := &Reconciler{Ent: db, Gateway: gw, Prune: true}

	rep, err := r.ReconcileKeys(ctx)
	if err != nil {
		t.Fatalf("ReconcileKeys: %v", err)
	}
	if rep.Pruned != 1 || len(gw.deleted) != 1 || gw.deleted[0] != "sk-B" {
		t.Errorf("want orphan sk-B pruned, got pruned=%d deleted=%v", rep.Pruned, gw.deleted)
	}
	if rep.Revoked != 1 {
		t.Errorf("want 1 stale row revoked, got %d", rep.Revoked)
	}
	got := db.VirtualKey.GetX(ctx, stale.ID)
	if got.Status != virtualkey.StatusRevoked {
		t.Errorf("stale row should be revoked, got %s", got.Status)
	}
}

// Already-revoked DB rows are not reported as stale (they are expected to be gone).
func TestReconcileKeys_IgnoresRevokedRows(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	mkKey(t, db, "sk-R", virtualkey.StatusRevoked) // absent at gateway, but already revoked

	gw := &fakeKeyGateway{keys: []gateway.KeyInfo{}}
	r := &Reconciler{Ent: db, Gateway: gw}
	rep, err := r.ReconcileKeys(context.Background())
	if err != nil {
		t.Fatalf("ReconcileKeys: %v", err)
	}
	if len(rep.StaleRows) != 0 {
		t.Errorf("revoked rows must not count as stale: %v", rep.StaleRows)
	}
}

func TestReconcileKeys_ListError(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	gw := &fakeKeyGateway{listErr: errors.New("gateway down")}
	r := &Reconciler{Ent: db, Gateway: gw}
	if _, err := r.ReconcileKeys(context.Background()); err == nil {
		t.Fatal("expected error when ListKeys fails")
	}
}

// Guard: when EVERY gateway key is unmatched against a non-empty DB (the
// identifier-mismatch signature), Prune must refuse — deleting all keys + revoking
// all rows would be catastrophic.
func TestReconcileKeys_Prune_RefusesOnTotalMismatch(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	active := mkKey(t, db, "sk-A", virtualkey.StatusActive)
	// gateway lists hashed tokens that match NO DB row (raw-key vs hash mismatch)
	gw := &fakeKeyGateway{keys: []gateway.KeyInfo{{Key: "hash1"}, {Key: "hash2"}}}
	r := &Reconciler{Ent: db, Gateway: gw, Prune: true}

	rep, err := r.ReconcileKeys(ctx)
	if err != nil {
		t.Fatalf("ReconcileKeys: %v", err)
	}
	if rep.Pruned != 0 || len(gw.deleted) != 0 {
		t.Errorf("must refuse to prune on total mismatch: pruned=%d deleted=%v", rep.Pruned, gw.deleted)
	}
	if rep.Revoked != 0 {
		t.Errorf("must refuse to revoke on total mismatch: revoked=%d", rep.Revoked)
	}
	if got := db.VirtualKey.GetX(ctx, active.ID); got.Status != virtualkey.StatusActive {
		t.Errorf("active key wrongly revoked: %s", got.Status)
	}
}

// Guard: an empty gateway listing (possibly a failed/partial call) must not
// mass-revoke every governance row.
func TestReconcileKeys_Prune_RefusesRevokeOnEmptyGateway(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	active := mkKey(t, db, "sk-A", virtualkey.StatusActive)
	gw := &fakeKeyGateway{keys: []gateway.KeyInfo{}} // empty / failed-looking
	r := &Reconciler{Ent: db, Gateway: gw, Prune: true}

	rep, err := r.ReconcileKeys(ctx)
	if err != nil {
		t.Fatalf("ReconcileKeys: %v", err)
	}
	if rep.Revoked != 0 {
		t.Errorf("must not revoke on empty gateway list: revoked=%d", rep.Revoked)
	}
	if got := db.VirtualKey.GetX(ctx, active.ID); got.Status != virtualkey.StatusActive {
		t.Errorf("active key wrongly revoked on empty list: %s", got.Status)
	}
}

// Guard: when every gateway team is unmatched (mismatch / foreign teams on a
// shared gateway), team Prune must refuse.
func TestReconcileTeams_Prune_RefusesOnTotalMismatch(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	mkDept(t, db, "research", "t-A")
	mkDept(t, db, "sales", "t-B")
	// gateway has only foreign teams, none backed by a department
	gw := &fakeKeyGateway{teams: []gateway.TeamInfo{{TeamID: "foreign-1"}, {TeamID: "foreign-2"}}}
	r := &Reconciler{Ent: db, Gateway: gw, Prune: true}

	rep, err := r.ReconcileTeams(ctx)
	if err != nil {
		t.Fatalf("ReconcileTeams: %v", err)
	}
	if rep.Pruned != 0 || len(gw.deletedTeams) != 0 {
		t.Errorf("must refuse to prune foreign/mismatched teams: pruned=%d deleted=%v", rep.Pruned, gw.deletedTeams)
	}
}

// Root-cause fix: a row's gateway identity is its hashed token. When /key/list
// reports that token (not the raw key), reconciliation must match by it — no false
// orphan, no false stale.
func TestReconcileKeys_MatchesByToken(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	_, err := db.VirtualKey.Create().
		SetLitellmKey("sk-raw-secret"). // raw key, never returned by /key/list
		SetLitellmToken("hash-abc").    // hashed token, what /key/list returns
		SetUserID(uuid.New()).
		SetStatus(virtualkey.StatusActive).
		Save(ctx)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	gw := &fakeKeyGateway{keys: []gateway.KeyInfo{{Key: "hash-abc"}}}
	r := &Reconciler{Ent: db, Gateway: gw, Prune: true}

	rep, err := r.ReconcileKeys(ctx)
	if err != nil {
		t.Fatalf("ReconcileKeys: %v", err)
	}
	if len(rep.GatewayOrphans) != 0 {
		t.Errorf("token-matched gateway key wrongly flagged orphan: %v", rep.GatewayOrphans)
	}
	if len(rep.StaleRows) != 0 {
		t.Errorf("token-matched row wrongly flagged stale: %v", rep.StaleRows)
	}
	if rep.Pruned != 0 || rep.Revoked != 0 {
		t.Errorf("nothing should be pruned/revoked: pruned=%d revoked=%d", rep.Pruned, rep.Revoked)
	}
}

func mkDept(t *testing.T, c *ent.Client, name, teamID string) *ent.Department {
	t.Helper()
	b := c.Department.Create().SetName(name)
	if teamID != "" {
		b.SetLitellmTeamID(teamID)
	}
	d, err := b.Save(context.Background())
	if err != nil {
		t.Fatalf("create dept %s: %v", name, err)
	}
	return d
}

// Report-only: team orphans + dangling department links are found, nothing mutated.
func TestReconcileTeams_ReportOnly(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	mkDept(t, db, "research", "t-A")          // backed by gateway team
	dangling := mkDept(t, db, "sales", "t-C") // litellm_team_id set, team gone at gateway
	mkDept(t, db, "no-team", "")              // unlinked — ignored entirely

	gw := &fakeKeyGateway{teams: []gateway.TeamInfo{
		{TeamID: "t-A"}, // matches a department
		{TeamID: "t-B"}, // orphan: no department
	}}

	r := &Reconciler{Ent: db, Gateway: gw, Prune: false}
	rep, err := r.ReconcileTeams(ctx)
	if err != nil {
		t.Fatalf("ReconcileTeams: %v", err)
	}
	if rep.DBDepartments != 2 { // only the two linked departments count
		t.Errorf("DBDepartments = %d, want 2", rep.DBDepartments)
	}
	if len(rep.TeamOrphans) != 1 || rep.TeamOrphans[0] != "t-B" {
		t.Errorf("TeamOrphans = %v, want [t-B]", rep.TeamOrphans)
	}
	if len(rep.DanglingDepts) != 1 || rep.DanglingDepts[0] != dangling.ID.String() {
		t.Errorf("DanglingDepts = %v, want [%s]", rep.DanglingDepts, dangling.ID)
	}
	if rep.Pruned != 0 || len(gw.deletedTeams) != 0 {
		t.Errorf("report-only must not prune: pruned=%d deleted=%v", rep.Pruned, gw.deletedTeams)
	}
}

// Prune: orphan gateway teams are deleted; dangling department links are NOT
// auto-healed (only reported).
func TestReconcileTeams_Prune(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	mkDept(t, db, "research", "t-A")
	mkDept(t, db, "sales", "t-C") // dangling

	gw := &fakeKeyGateway{teams: []gateway.TeamInfo{{TeamID: "t-A"}, {TeamID: "t-B"}}}
	r := &Reconciler{Ent: db, Gateway: gw, Prune: true}

	rep, err := r.ReconcileTeams(ctx)
	if err != nil {
		t.Fatalf("ReconcileTeams: %v", err)
	}
	if rep.Pruned != 1 || len(gw.deletedTeams) != 1 || gw.deletedTeams[0] != "t-B" {
		t.Errorf("want orphan t-B pruned, got pruned=%d deleted=%v", rep.Pruned, gw.deletedTeams)
	}
	if len(rep.DanglingDepts) != 1 {
		t.Errorf("dangling dept should still be reported under prune, got %v", rep.DanglingDepts)
	}
}

func TestReconcileTeams_ListError(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	gw := &fakeKeyGateway{listTeamErr: errors.New("gateway down")}
	r := &Reconciler{Ent: db, Gateway: gw}
	if _, err := r.ReconcileTeams(context.Background()); err == nil {
		t.Fatal("expected error when ListTeams fails")
	}
}
