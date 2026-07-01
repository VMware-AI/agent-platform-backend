package secrets

import (
	"context"
	"crypto/cipher"
	"fmt"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/platformsecret"
	"github.com/google/uuid"
)

// DBStore is the platform's single secrets backend: a persistent Store + Resolver
// that keeps credentials in the platform database, ENCRYPTED at rest. It is used
// in every environment (no dev/prod split) so credentials survive backend restarts
// and there is one code path to reason about.
//
// password and api_key are sealed with AES-256-GCM under a single symmetric key
// (SECRETS_ENCRYPTION_KEY); the username is stored in clear for operability. The
// opaque ref string stored on the owning row (e.g. ResourcePool.secret_ref) keys
// each lookup.
type DBStore struct {
	ent  *ent.Client
	aead cipher.AEAD
}

// NewDBStore creates a DBStore. The encryption key is required: an empty key is a
// fail-fast error rather than silently storing plaintext.
func NewDBStore(client *ent.Client, encryptionKey string) (*DBStore, error) {
	aead, err := newAEAD(encryptionKey)
	if err != nil {
		return nil, err
	}
	return &DBStore{ent: client, aead: aead}, nil
}

// Put encrypts the secret fields and stores them under a freshly generated ref,
// returning the ref. The ref format ("vault://<name>-<id>") is an opaque, stable
// scheme kept for compatibility with refs already stored on owning rows.
func (d *DBStore) Put(ctx context.Context, name string, cred Credential) (string, error) {
	encPassword, err := encrypt(d.aead, cred.Password)
	if err != nil {
		return "", fmt.Errorf("db store: encrypt password for %q: %w", name, err)
	}
	encAPIKey, err := encrypt(d.aead, cred.APIKey)
	if err != nil {
		return "", fmt.Errorf("db store: encrypt api_key for %q: %w", name, err)
	}
	ref := fmt.Sprintf("vault://%s-%s", name, uuid.New().String()[:8])
	if _, err := d.ent.PlatformSecret.Create().
		SetRef(ref).
		SetUsername(cred.Username).
		SetPassword(encPassword).
		SetAPIKey(encAPIKey).
		Save(ctx); err != nil {
		return "", fmt.Errorf("db store: put %q: %w", name, err)
	}
	return ref, nil
}

// Resolve looks up and decrypts the credential stored under ref. Returns
// ErrNotFound if the ref does not exist.
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
	password, err := decrypt(d.aead, s.Password)
	if err != nil {
		return Credential{}, fmt.Errorf("db store: resolve %q: %w", ref, err)
	}
	apiKey, err := decrypt(d.aead, s.APIKey)
	if err != nil {
		return Credential{}, fmt.Errorf("db store: resolve %q: %w", ref, err)
	}
	return Credential{
		Username: s.Username,
		Password: password,
		APIKey:   apiKey,
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
