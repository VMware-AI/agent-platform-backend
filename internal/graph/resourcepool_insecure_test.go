package graph

import (
	"context"
	"fmt"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

// TestResourcePool_Insecure pins LLD-13's per-pool TLS-skip: the `insecure` flag
// is persisted + projected by create/update, and connectPool passes EACH pool's
// own value into the vCenter dial (no global VCENTER_INSECURE env anymore).
func TestResourcePool_Insecure(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://oc": {Username: "u", Password: "p"},
	})
	// Capture-only stub: record the insecure flag connectPool dials with, then
	// short-circuit (no real vCenter needed to assert the flag plumbing).
	var gotInsecure bool
	r.VCenterConnect = func(_ context.Context, _, _, _ string, insecure bool) (VCenterClient, error) {
		gotInsecure = insecure
		return nil, fmt.Errorf("stub: capture only")
	}

	ctx := adminCtx()
	mr := &mutationResolver{r}
	ref := "vault://oc"
	tru := true

	// 1) create with insecure=true → persisted + projected.
	skip, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{
		Name: "skip-tls", Endpoint: "https://vc.local", SecretRef: &ref, Insecure: &tru,
	})
	if err != nil {
		t.Fatalf("create insecure pool: %v", err)
	}
	if !skip.Pool.Insecure {
		t.Fatal("create with insecure=true must project Insecure=true")
	}

	// 2) create with insecure omitted → defaults to false (verify on).
	verify, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{
		Name: "verify-tls", Endpoint: "https://vc2.local", SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("create default pool: %v", err)
	}
	if verify.Pool.Insecure {
		t.Fatal("omitted insecure must default to false (TLS verification on)")
	}

	// 3) connectPool dials each pool with ITS OWN insecure value.
	_, _ = mr.SyncResourcePool(ctx, skip.Pool.ID)
	if !gotInsecure {
		t.Fatal("insecure pool must dial vCenter with insecure=true")
	}
	_, _ = mr.SyncResourcePool(ctx, verify.Pool.ID)
	if gotInsecure {
		t.Fatal("default pool must dial vCenter with insecure=false")
	}

	// 4) update toggles the flag → reflected on the next dial.
	upd, err := mr.UpdateResourcePool(ctx, verify.Pool.ID, model.UpdateResourcePoolInput{Insecure: &tru})
	if err != nil {
		t.Fatalf("update insecure: %v", err)
	}
	if !upd.Pool.Insecure {
		t.Fatal("update insecure=true must project Insecure=true")
	}
	_, _ = mr.SyncResourcePool(ctx, verify.Pool.ID)
	if !gotInsecure {
		t.Fatal("after update, pool must dial vCenter with insecure=true")
	}

	// 5) update an UNRELATED field with insecure omitted must NOT clobber the
	// existing true (the nil-guard regression a future refactor could reintroduce).
	nm := "verify-tls-renamed"
	kept, err := mr.UpdateResourcePool(ctx, verify.Pool.ID, model.UpdateResourcePoolInput{Name: &nm})
	if err != nil {
		t.Fatalf("update name only: %v", err)
	}
	if !kept.Pool.Insecure {
		t.Fatal("update with insecure omitted must preserve the existing insecure=true")
	}
}
