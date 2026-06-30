package secrets

import (
	"context"
	"fmt"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/platformsecret"
	"github.com/google/uuid"
)

// DBStore is a persistent secrets Store + Resolver backed by the platform
// database. It replaces StaticResolver for dev / air-gapped deployments where
// Vaultwarden is not available, so credentials survive backend restarts.
//
// All writes go to the platform_secrets table; reads look up by the opaque ref
// string stored on the owning row (e.g. ResourcePool.secret_ref).
type DBStore struct {
	ent *ent.Client
}

// NewDBStore creates a DBStore backed by the given Ent client.
func NewDBStore(client *ent.Client) *DBStore {
	return &DBStore{ent: client}
}

// Put stores cred under a freshly generated ref and returns the ref.
// The ref format matches StaticResolver ("vault://<name>-<id>") so callers
// are agnostic to which store is in use.
func (d *DBStore) Put(ctx context.Context, name string, cred Credential) (string, error) {
	ref := fmt.Sprintf("vault://%s-%s", name, uuid.New().String()[:8])
	_, err := d.ent.PlatformSecret.Create().
		SetRef(ref).
		SetUsername(cred.Username).
		SetPassword(cred.Password).
		SetAPIKey(cred.APIKey).
		Save(ctx)
	if err != nil {
		return "", fmt.Errorf("db store: put %q: %w", name, err)
	}
	return ref, nil
}

// Resolve looks up the credential stored under ref.
// Returns ErrNotFound if the ref does not exist in the DB.
func (d *DBStore) Resolve(ctx context.Context, ref string) (Credential, error) {
	s, err := d.ent.PlatformSecret.Query().
		Where(platformsecret.Ref(ref)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Credential{}, fmt.Errorf("%w: %s", ErrNotFound, ref)
		}
		return Credential{}, fmt.Errorf("db store: resolve %q: %w", ref, err)
	}
	return Credential{
		Username: s.Username,
		Password: s.Password,
		APIKey:   s.APIKey,
	}, nil
}

// Delete removes the secret stored under ref (idempotent — a missing ref is not
// an error).
func (d *DBStore) Delete(ctx context.Context, ref string) error {
	_, err := d.ent.PlatformSecret.Delete().
		Where(platformsecret.Ref(ref)).
		Exec(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("db store: delete %q: %w", ref, err)
	}
	return nil
}
