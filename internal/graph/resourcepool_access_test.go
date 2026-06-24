package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

// 模块② 接入: the 接入表单 submits a vCenter username/password; the backend writes
// them to the secret store and persists only the returned ref — plaintext never
// lands in the DB. An explicit secretRef is the alternative; no creds → untouched.
func TestRegisterResourcePool_StoresCredentials(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	store := secrets.NewStaticResolver(nil)
	r.Secrets = store
	ctx := adminCtx()
	bg := context.Background()
	mr := &mutationResolver{r}

	u, p := "administrator@vsphere.local", "Secret123!"
	created, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{
		Name: "dc1", Endpoint: "https://vc1", Username: &u, Password: &p,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pool := created.Pool
	row := r.Ent.ResourcePool.GetX(bg, uuid.MustParse(pool.ID))
	if !strings.HasPrefix(row.SecretRef, "vault://") {
		t.Fatalf("secret_ref not a store ref: %q", row.SecretRef)
	}
	// the stored ref resolves back to the submitted creds (plaintext only in store)
	cred, err := store.Resolve(bg, row.SecretRef)
	if err != nil || cred.Username != u || cred.Password != p {
		t.Fatalf("creds not stored: %+v / %v", cred, err)
	}

	// secretRef-only path: stored verbatim, no Put
	ref := "vault://preexisting-9"
	created2, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{
		Name: "dc2", Endpoint: "https://vc2", SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("create secretRef: %v", err)
	}
	if got := r.Ent.ResourcePool.GetX(bg, uuid.MustParse(created2.Pool.ID)).SecretRef; got != ref {
		t.Fatalf("secretRef path: %q", got)
	}

	// no creds → empty secret_ref (pool registered, test-connection will fail later)
	created3, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{Name: "dc3", Endpoint: "https://vc3"})
	if err != nil {
		t.Fatalf("create no-cred: %v", err)
	}
	if got := r.Ent.ResourcePool.GetX(bg, uuid.MustParse(created3.Pool.ID)).SecretRef; got != "" {
		t.Fatalf("no-cred pool should have empty secret_ref: %q", got)
	}

	// update rotates the credential (re-submit → new stored ref)
	u2, p2 := "svc-account", "Rotated456!"
	if _, err := mr.UpdateResourcePool(ctx, pool.ID, model.UpdateResourcePoolInput{Username: &u2, Password: &p2}); err != nil {
		t.Fatalf("update: %v", err)
	}
	rowU := r.Ent.ResourcePool.GetX(bg, uuid.MustParse(pool.ID))
	credU, err := store.Resolve(bg, rowU.SecretRef)
	if err != nil || credU.Username != u2 || credU.Password != p2 {
		t.Fatalf("rotation failed: %+v / %v", credU, err)
	}
}
