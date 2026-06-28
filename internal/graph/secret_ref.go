package graph

import (
	"context"
	"fmt"
	"log"

	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// resolvePoolSecretRef turns a resource-pool credential submission into a stored
// secret reference (模块② 接入). The 接入表单 sends a vCenter username/password;
// the backend writes them to the secret store (Vaultwarden) and persists ONLY the
// returned ref — plaintext never lands in the DB. An explicit secretRef (pre-existing
// item) is accepted as an alternative. Returns set=false when no credential was given
// (leave secret_ref untouched). label seeds the secret-store item name.
func (r *Resolver) resolvePoolSecretRef(ctx context.Context, label string, username, password, secretRef *string) (ref string, set bool, err error) {
	u, p := derefString(username), derefString(password)
	if u != "" || p != "" {
		store, ok := r.Secrets.(secrets.Store)
		if !ok {
			return "", false, gqlerror.Errorf("secret store not configured; cannot accept credentials")
		}
		ref, err := store.Put(ctx, "resourcepool/"+label, secrets.Credential{Username: u, Password: p})
		if err != nil {
			return "", false, fmt.Errorf("store pool credentials: %w", err)
		}
		return ref, true, nil
	}
	if secretRef != nil && *secretRef != "" {
		return *secretRef, true, nil
	}
	return "", false, nil
}

// resolveKeySecretRef turns a single raw API/master key submission into a stored
// secret reference (模块③ 路由 / 网关接入). Mirrors resolvePoolSecretRef but for a
// one-field key: the form sends a raw key, the backend writes it to the secret
// store and persists ONLY the ref — plaintext never lands in the DB. An explicit
// existing ref is the alternative; set=false when neither is given. label = the
// secret-store item name (caller-qualified, e.g. "upstream/<name>").
func (r *Resolver) resolveKeySecretRef(ctx context.Context, label string, rawKey, existingRef *string) (ref string, set bool, err error) {
	if k := derefString(rawKey); k != "" {
		store, ok := r.Secrets.(secrets.Store)
		if !ok {
			return "", false, gqlerror.Errorf("secret store not configured; cannot accept credentials")
		}
		ref, err := store.Put(ctx, label, secrets.Credential{APIKey: k})
		if err != nil {
			return "", false, fmt.Errorf("store credential: %w", err)
		}
		return ref, true, nil
	}
	if existingRef != nil && *existingRef != "" {
		return *existingRef, true, nil
	}
	return "", false, nil
}

// deleteSecretRef best-effort removes a secret-store entry (e.g. a gateway master
// key) when its owning row is deleted or its key rotated, so the store doesn't
// accumulate orphans. Never fatal — a resolver that can't delete (store missing,
// or not a Store) is logged, not surfaced: the DB row is already gone, so failing
// the mutation would be worse than a lingering secret.
func (r *Resolver) deleteSecretRef(ctx context.Context, ref string) {
	if ref == "" {
		return
	}
	store, ok := r.Secrets.(secrets.Store)
	if !ok {
		return
	}
	if err := store.Delete(ctx, ref); err != nil {
		log.Printf("secret cleanup: delete ref failed (orphan possible): %v", err)
	}
}
