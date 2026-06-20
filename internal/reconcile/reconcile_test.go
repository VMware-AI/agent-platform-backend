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

// fakeKeyGateway is an in-memory KeyGateway for reconciler tests.
type fakeKeyGateway struct {
	keys    []gateway.KeyInfo
	listErr error
	deleted []string
	delErr  error
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
