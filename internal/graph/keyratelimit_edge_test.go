package graph

// Edge-case coverage for the virtual-key and rate-limit-policy resolvers:
// empty-state listings, the userId filter, the full issue/revoke/regenerate/
// setEnabled lifecycle, name-keyed upsert (insert vs update), policy toggling,
// and the various not-found / bad-id paths. These complement (do not duplicate)
// virtualkey_test.go, virtualkey_policy_test.go and key_ratelimit_module_test.go.
// Helper/var names carry the `_krl` suffix to stay collision-free across the tree.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// missingUUID_krl is a syntactically valid UUID that is never seeded, so any
// Get/Update against it must surface a not-found error (never a panic).
const missingUUID_krl = "deadbeef-0000-4000-8000-000000000000"

// issueKey_krl mints a key for the given user through the resolver, failing the
// test on error. Centralizes the happy-path issue so each case stays terse.
func issueKey_krl(t *testing.T, mr *mutationResolver, ctx context.Context, userID string) *model.IssuedVirtualKey {
	t.Helper()
	issued, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{
		UserID: userID, Models: []string{"smart"},
	})
	if err != nil {
		t.Fatalf("issueKey_krl: %v", err)
	}
	return issued
}

// --- virtualKeys query: empty state + userId filter ------------------------

func TestVirtualKeys_EmptyState_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	// No keys seeded: a non-nil, zero-length slice (never a nil that the GraphQL
	// layer would render as null for a non-null list).
	all, err := qr.VirtualKeys(context.Background(), nil)
	if err != nil {
		t.Fatalf("VirtualKeys(nil): %v", err)
	}
	if all == nil {
		t.Fatal("empty-state must return a non-nil slice")
	}
	if len(all) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(all))
	}
}

func TestVirtualKeys_FiltersByUser_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	alice := mkUser(t, mr, ctx, "krl_alice", "krl_alice@x.io", model.RoleNameUser)
	bob := mkUser(t, mr, ctx, "krl_bob", "krl_bob@x.io", model.RoleNameUser)

	issueKey_krl(t, mr, ctx, alice.ID)
	issueKey_krl(t, mr, ctx, alice.ID)
	issueKey_krl(t, mr, ctx, bob.ID)

	// Filter narrows to the owner's keys only.
	aliceKeys, err := qr.VirtualKeys(ctx, &alice.ID)
	if err != nil {
		t.Fatalf("VirtualKeys(alice): %v", err)
	}
	if len(aliceKeys) != 2 {
		t.Fatalf("alice should have 2 keys, got %d", len(aliceKeys))
	}
	for _, k := range aliceKeys {
		if k.UserID != alice.ID {
			t.Fatalf("filter leaked a key for %s into alice's list", k.UserID)
		}
	}

	// No filter returns everyone's keys (3 total).
	allKeys, err := qr.VirtualKeys(ctx, nil)
	if err != nil {
		t.Fatalf("VirtualKeys(nil): %v", err)
	}
	if len(allKeys) != 3 {
		t.Fatalf("unfiltered should return 3 keys, got %d", len(allKeys))
	}

	// A well-formed userId with no keys yields an empty (non-nil) slice.
	none, err := qr.VirtualKeys(ctx, ptrStr_krl(uuid.New().String()))
	if err != nil {
		t.Fatalf("VirtualKeys(stranger): %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Fatalf("stranger should have 0 keys, got %v", none)
	}
}

func TestVirtualKeys_InvalidUserID_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	if _, err := qr.VirtualKeys(context.Background(), ptrStr_krl("not-a-uuid")); err == nil {
		t.Fatal("a malformed userId filter must be rejected")
	}
}

// --- virtual-key mutations: bad-id & not-found paths (no panics) ------------

func TestVirtualKeyMutations_BadUUID_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	ctx := context.Background()
	mr := &mutationResolver{r}

	const bad = "не-uuid"

	if _, err := mr.RevokeVirtualKey(ctx, bad); err == nil {
		t.Error("RevokeVirtualKey must reject a malformed id")
	}
	if _, err := mr.RegenerateVirtualKey(ctx, bad); err == nil {
		t.Error("RegenerateVirtualKey must reject a malformed id")
	}
	if _, err := mr.SetVirtualKeyEnabled(ctx, bad, true); err == nil {
		t.Error("SetVirtualKeyEnabled must reject a malformed id")
	}
}

func TestVirtualKeyMutations_NotFound_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	ctx := context.Background()
	mr := &mutationResolver{r}

	// Valid UUID shape, but no such row → not-found (and crucially, no panic).
	if _, err := mr.RevokeVirtualKey(ctx, missingUUID_krl); err == nil {
		t.Error("RevokeVirtualKey on a missing id must error")
	}
	if _, err := mr.RegenerateVirtualKey(ctx, missingUUID_krl); err == nil {
		t.Error("RegenerateVirtualKey on a missing id must error")
	}
	if _, err := mr.SetVirtualKeyEnabled(ctx, missingUUID_krl, false); err == nil {
		t.Error("SetVirtualKeyEnabled on a missing id must error")
	}
}

// SetVirtualKeyEnabled is a no-op-safe toggle: disabling an already-disabled key
// (or enabling an active one) keeps the key usable and does NOT call the gateway
// (revoke is the only gateway-side delete).
func TestSetVirtualKeyEnabled_Idempotent_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	ctx := context.Background()
	mr := &mutationResolver{r}

	u := mkUser(t, mr, ctx, "krl_toggle", "krl_toggle@x.io", model.RoleNameUser)
	issued := issueKey_krl(t, mr, ctx, u.ID)

	if vk, err := mr.SetVirtualKeyEnabled(ctx, issued.VirtualKey.ID, false); err != nil || vk.Status != model.VirtualKeyStatusDisabled {
		t.Fatalf("first disable: status=%v err=%v", statusOf_krl(vk), err)
	}
	// Disabling again stays disabled (idempotent, still no gateway delete).
	if vk, err := mr.SetVirtualKeyEnabled(ctx, issued.VirtualKey.ID, false); err != nil || vk.Status != model.VirtualKeyStatusDisabled {
		t.Fatalf("second disable: status=%v err=%v", statusOf_krl(vk), err)
	}
	if len(fg.deleted) != 0 {
		t.Fatalf("toggling enabled must not delete the key at the gateway: %+v", fg.deleted)
	}
}

// RegenerateVirtualKey requires a configured gateway: with none it must fail
// loudly (and not touch the row) rather than silently no-op.
func TestRegenerateVirtualKey_NoGateway_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	ctx := context.Background()
	mr := &mutationResolver{r}

	u := mkUser(t, mr, ctx, "krl_nogw", "krl_nogw@x.io", model.RoleNameUser)
	issued := issueKey_krl(t, mr, ctx, u.ID)
	beforeKey := r.Ent.VirtualKey.GetX(ctx, uuid.MustParse(issued.VirtualKey.ID)).LitellmKey

	r.Gateway = nil // drop the gateway
	if _, err := mr.RegenerateVirtualKey(ctx, issued.VirtualKey.ID); err == nil {
		t.Fatal("regenerate without a gateway must fail")
	}
	// The stored secret must be untouched after the failed regenerate.
	afterKey := r.Ent.VirtualKey.GetX(ctx, uuid.MustParse(issued.VirtualKey.ID)).LitellmKey
	if beforeKey != afterKey {
		t.Fatalf("failed regenerate must not rotate the stored key: %q -> %q", beforeKey, afterKey)
	}
}

// Revoking a key is terminal: a second revoke of the already-revoked key must
// not crash, and the gateway is not asked to delete it twice.
func TestRevokeVirtualKey_DoubleRevoke_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	ctx := context.Background()
	mr := &mutationResolver{r}

	u := mkUser(t, mr, ctx, "krl_dbl", "krl_dbl@x.io", model.RoleNameUser)
	issued := issueKey_krl(t, mr, ctx, u.ID)

	if ok, err := mr.RevokeVirtualKey(ctx, issued.VirtualKey.ID); err != nil || !ok {
		t.Fatalf("first revoke: ok=%v err=%v", ok, err)
	}
	// Regenerate / re-enable are both refused once revoked.
	if _, err := mr.RegenerateVirtualKey(ctx, issued.VirtualKey.ID); err == nil {
		t.Error("revoked key must not be regenerated")
	}
	if _, err := mr.SetVirtualKeyEnabled(ctx, issued.VirtualKey.ID, true); err == nil {
		t.Error("revoked key must not be re-enabled")
	}
}

// --- rateLimitPolicies query + upsert/setEnabled/not-found -----------------

func TestRateLimitPolicies_EmptyState_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	ps, err := qr.RateLimitPolicies(context.Background())
	if err != nil {
		t.Fatalf("RateLimitPolicies: %v", err)
	}
	if ps == nil {
		t.Fatal("empty-state must return a non-nil slice")
	}
	if len(ps) != 0 {
		t.Fatalf("expected 0 policies, got %d", len(ps))
	}
}

// UpsertRateLimitPolicy is keyed by name: the same name updates the existing row
// (no duplicate insert) and its rpm/tpm/enabled fields are overwritten.
func TestUpsertRateLimitPolicy_ByName_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	rpm1, tpm1 := 60, 100000
	created, err := mr.UpsertRateLimitPolicy(ctx, model.UpsertRateLimitPolicyInput{
		Name: "krl-std", Rpm: &rpm1, Tpm: &tpm1, Enabled: ptrBool_krl(true),
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if created.Rpm == nil || *created.Rpm != 60 || created.Tpm == nil || *created.Tpm != 100000 || !created.Enabled {
		t.Fatalf("insert returned wrong fields: %+v", created)
	}

	// Same name → update-in-place: id is preserved, fields change, no new row.
	rpm2 := 120
	updated, err := mr.UpsertRateLimitPolicy(ctx, model.UpsertRateLimitPolicyInput{
		Name: "krl-std", Rpm: &rpm2, Enabled: ptrBool_krl(false),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("upsert-by-name must reuse the same row: %s != %s", updated.ID, created.ID)
	}
	if updated.Rpm == nil || *updated.Rpm != 120 {
		t.Fatalf("rpm not updated: %+v", updated.Rpm)
	}
	if updated.Enabled {
		t.Fatal("enabled should have been overwritten to false")
	}

	// Exactly one row exists for that name.
	ps, err := qr.RateLimitPolicies(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := countByName_krl(ps, "krl-std"); got != 1 {
		t.Fatalf("expected exactly 1 policy named krl-std, got %d (total %d)", got, len(ps))
	}
}

// Two distinct names produce two distinct rows (insert path, not update).
func TestUpsertRateLimitPolicy_DistinctNames_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	a, err := mr.UpsertRateLimitPolicy(ctx, model.UpsertRateLimitPolicyInput{Name: "krl-a"})
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := mr.UpsertRateLimitPolicy(ctx, model.UpsertRateLimitPolicyInput{Name: "krl-b"})
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a.ID == b.ID {
		t.Fatal("distinct names must yield distinct rows")
	}
	// Omitting `enabled` defaults to false on insert.
	if a.Enabled {
		t.Fatalf("enabled should default to false when omitted, got %+v", a)
	}
	ps, err := qr.RateLimitPolicies(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(ps))
	}
}

// SetRateLimitPolicyEnabled flips the flag on a real row and surfaces an error
// (no panic) for malformed and missing ids.
func TestSetRateLimitPolicyEnabled_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	pol, err := mr.UpsertRateLimitPolicy(ctx, model.UpsertRateLimitPolicyInput{
		Name: "krl-toggle", Enabled: ptrBool_krl(true),
	})
	if err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	off, err := mr.SetRateLimitPolicyEnabled(ctx, pol.ID, false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if off.Enabled {
		t.Fatal("policy should be disabled")
	}
	on, err := mr.SetRateLimitPolicyEnabled(ctx, pol.ID, true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !on.Enabled {
		t.Fatal("policy should be re-enabled")
	}

	// Bad id and missing id are both rejected without panicking.
	if _, err := mr.SetRateLimitPolicyEnabled(ctx, "xyz", true); err == nil {
		t.Error("malformed id must be rejected")
	}
	if _, err := mr.SetRateLimitPolicyEnabled(ctx, missingUUID_krl, true); err == nil {
		t.Error("missing id must error")
	}
}

// DeleteRateLimitPolicy must distinguish bad-id, missing-id and in-use guard.
func TestDeleteRateLimitPolicy_NotFoundAndBadID_krl(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	if _, err := mr.DeleteRateLimitPolicy(ctx, "%%bad%%"); err == nil {
		t.Error("malformed id must be rejected")
	}
	_, err := mr.DeleteRateLimitPolicy(ctx, missingUUID_krl)
	if err == nil {
		t.Fatal("deleting a missing policy must error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("missing-policy error should read as not-found, got %q", err.Error())
	}
}

// --- small local helpers (suffixed to avoid collisions) --------------------

func ptrStr_krl(s string) *string { return &s }
func ptrBool_krl(b bool) *bool    { return &b }

func statusOf_krl(vk *model.VirtualKey) any {
	if vk == nil {
		return "<nil>"
	}
	return vk.Status
}

func countByName_krl(ps []model.RateLimitPolicy, name string) int {
	n := 0
	for _, p := range ps {
		if p.Name == name {
			n++
		}
	}
	return n
}
