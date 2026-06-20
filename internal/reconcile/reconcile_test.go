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
