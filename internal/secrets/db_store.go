package secrets

import (
	"context"
	"crypto/cipher"
	"fmt"
	"strings"
	"sync"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/platformsecret"
	"github.com/google/uuid"
)

// ParseKeyConfig parses the SECRETS_ENCRYPTION_KEYS env value into a map of
// id→passphrase plus the active id (the first listed entry). The format is
// `id1:passphrase1,id2:passphrase2,...`. Empty segments, empty ids, and
// duplicate ids are rejected so misconfiguration fails loud at startup
// rather than silently dropping a key.
//
// The active id is whichever id appears first in the list — its position
// (not its name) marks it, so an operator rotates by prepending the new key:
//
//	SECRETS_ENCRYPTION_KEYS=k2:new,k1:old
//
// (new is active; old stays around so existing rows keep decrypting until the
// worker migrates them).
func ParseKeyConfig(s string) (map[string]string, string, error) {
	out := map[string]string{}
	var first string
	for _, seg := range strings.Split(s, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		idx := strings.IndexByte(seg, ':')
		if idx <= 0 || idx == len(seg)-1 {
			return nil, "", fmt.Errorf("malformed segment %q (want id:passphrase)", seg)
		}
		id, pass := seg[:idx], seg[idx+1:]
		if id == "" || pass == "" {
			return nil, "", fmt.Errorf("malformed segment %q (empty id or passphrase)", seg)
		}
		if _, dup := out[id]; dup {
			return nil, "", fmt.Errorf("duplicate key id %q", id)
		}
		out[id] = pass
		if first == "" {
			first = id
		}
	}
	if first == "" {
		return nil, "", fmt.Errorf("empty SECRETS_ENCRYPTION_KEYS")
	}
	return out, first, nil
}

// defaultKeyID is the implicit key id when the operator uses the legacy
// SECRETS_ENCRYPTION_KEY single-value config (no rotation). It is also the
// value pre-rotation rows carry in platform_secrets.key_id, so v0 envelopes
// can be opened under this id without further bookkeeping.
const defaultKeyID = "default"

// DBStore is the platform's single secrets backend: a persistent Store + Resolver
// that keeps credentials in the platform database, ENCRYPTED at rest. Used in
// every environment (no dev/prod split) so credentials survive backend restarts
// and there is one code path to reason about.
//
// password and api_key are sealed with AES-256-GCM. In rotation mode each
// envelope carries the key id it was sealed under, and DBStore keeps an AEAD
// per configured key so old ciphertexts keep decrypting after the active key
// changes — the rotation worker migrates them in place at the operator's pace.
//
// The username is stored in clear for operability. The opaque ref string
// stored on the owning row (e.g. ResourcePool.secret_ref) keys each lookup.
type DBStore struct {
	ent *ent.Client
	// keys maps key id → AEAD. In single-key mode this holds a single entry
	// under defaultKeyID; in rotation mode it holds every key the operator
	// configured (active + retired), so any row's envelope can be opened as
	// long as its key id is still in the map.
	keys map[string]cipher.AEAD
	// activeID is the key id new Put writes under. Empty means "use the
	// defaultKeyID" so a freshly constructed DBStore in legacy single-key
	// mode can be distinguished from one explicitly wired to a non-default
	// active key (it isn't, behaviorally — both write under whatever id is
	// in activeID — but tests may assert on it).
	activeID string
	// mu guards keys + activeID. Resolve holds the read lock briefly while
	// looking up the AEAD; Put holds the write lock while reading activeID
	// to choose the AEAD. NewDBStoreWithKeys runs at startup before any
	// resolver, so contention is bounded.
	mu sync.RWMutex
}

// NewDBStore constructs a DBStore from a single key (legacy / no-rotation mode).
// The key is implicitly tagged with key id "default"; new rows are written
// under this id, and pre-rotation ciphertexts (also tagged "default") decrypt
// transparently. Empty key fails fast (a server that started with an empty
// key would otherwise silently store plaintext).
func NewDBStore(client *ent.Client, key string) (*DBStore, error) {
	a, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &DBStore{
		ent:      client,
		keys:     map[string]cipher.AEAD{defaultKeyID: a},
		activeID: defaultKeyID,
	}, nil
}

// NewDBStoreWithKeys constructs a DBStore in rotation mode. `keys` maps each
// configured key id → its raw passphrase; the first id is the active key (new
// Put writes go under it). All listed keys remain available for Resolve, so
// rows written under a now-retired key keep decrypting until the operator
// migrates them. At least one entry is required, and the active id must be
// one of the keys.
func NewDBStoreWithKeys(client *ent.Client, keys map[string]string, activeID string) (*DBStore, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("secrets: NewDBStoreWithKeys requires at least one key")
	}
	if activeID == "" {
		return nil, fmt.Errorf("secrets: NewDBStoreWithKeys requires a non-empty activeID")
	}
	if _, ok := keys[activeID]; !ok {
		return nil, fmt.Errorf("secrets: active key id %q is not in the configured keys", activeID)
	}
	aeads := make(map[string]cipher.AEAD, len(keys))
	for id, k := range keys {
		a, err := newAEAD(k)
		if err != nil {
			return nil, fmt.Errorf("secrets: build AEAD for key %q: %w", id, err)
		}
		aeads[id] = a
	}
	return &DBStore{
		ent:      client,
		keys:     aeads,
		activeID: activeID,
	}, nil
}

// ActiveKeyID returns the key id new rows are written under. Used by the
// rotation worker to filter rows whose key_id != ActiveKeyID (those are the
// migration candidates).
func (d *DBStore) ActiveKeyID() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.activeID
}

// HasKey reports whether the given key id is currently configured on this
// store. A row whose key_id returns false from HasKey cannot be decrypted
// until the operator re-installs the matching key.
func (d *DBStore) HasKey(id string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.keys[id]
	return ok
}

// Put encrypts the secret fields and stores them under a freshly generated ref,
// returning the ref. The ref format ("vault://<name>-<id>") is an opaque, stable
// scheme kept for compatibility with refs already stored on owning rows.
//
// Each row is sealed under the active key and tagged with that key's id, so a
// future rotation cycle can identify and migrate it without scanning the table.
func (d *DBStore) Put(ctx context.Context, name string, cred Credential) (string, error) {
	d.mu.RLock()
	aead := d.keys[d.activeID]
	activeID := d.activeID
	d.mu.RUnlock()

	encPassword, err := encryptWith(aead, cred.Password, activeID)
	if err != nil {
		return "", fmt.Errorf("db store: encrypt password for %q: %w", name, err)
	}
	encAPIKey, err := encryptWith(aead, cred.APIKey, activeID)
	if err != nil {
		return "", fmt.Errorf("db store: encrypt api_key for %q: %w", name, err)
	}
	ref := fmt.Sprintf("vault://%s-%s", name, uuid.New().String()[:8])
	if _, err := d.ent.PlatformSecret.Create().
		SetRef(ref).
		SetUsername(cred.Username).
		SetPassword(encPassword).
		SetAPIKey(encAPIKey).
		SetKeyID(activeID).
		Save(ctx); err != nil {
		return "", fmt.Errorf("db store: put %q: %w", name, err)
	}
	return ref, nil
}

// Resolve looks up and decrypts the credential stored under ref. Returns
// ErrNotFound if the ref does not exist. Decryption failures (wrong key,
// missing key_id in the configured map, tampered ciphertext) all surface as
// errors — never silent plaintext.
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
	// Use the row's key_id column to pick the right AEAD. The envelope's
	// embedded key_id must match (Resolve asserts this below) — a mismatch
	// means the row was migrated under one id but the envelope still
	// claims another, which is a corruption symptom.
	aead, err := d.keyID(s.KeyID)
	if err != nil {
		return Credential{}, fmt.Errorf("db store: resolve %q: %w", ref, err)
	}
	password, keyID, err := decryptWith(aead, s.Password, defaultKeyID)
	if err != nil {
		return Credential{}, fmt.Errorf("db store: resolve %q: %w", ref, err)
	}
	if keyID != s.KeyID {
		return Credential{}, fmt.Errorf("db store: resolve %q: envelope key_id %q != row key_id %q (corruption)",
			ref, keyID, s.KeyID)
	}
	apiKey, keyID, err := decryptWith(aead, s.APIKey, defaultKeyID)
	if err != nil {
		return Credential{}, fmt.Errorf("db store: resolve %q: %w", ref, err)
	}
	if keyID != s.KeyID {
		return Credential{}, fmt.Errorf("db store: resolve %q: envelope key_id %q != row key_id %q (corruption)",
			ref, keyID, s.KeyID)
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

// Reencrypt writes the credential under `ref` again, sealed under the active
// key. Used by the rotation worker: Resolve → Reencrypt instead of Put, so the
// ref (and therefore the link from owning rows) stays intact. The credential
// must already exist; a missing ref returns ErrNotFound. The caller (worker)
// is responsible for NOT calling Reencrypt on rows already under the active
// key — it'd be a no-op rewrite at best, and a needless DB write at worst.
func (d *DBStore) Reencrypt(ctx context.Context, ref string) error {
	d.mu.RLock()
	aead := d.keys[d.activeID]
	activeID := d.activeID
	d.mu.RUnlock()

	s, err := d.ent.PlatformSecret.Query().
		Where(platformsecret.Ref(ref)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, ref)
		}
		return fmt.Errorf("db store: reencrypt %q: load: %w", ref, err)
	}
	if s.KeyID == activeID {
		// Already on the active key — nothing to do. The worker filters
		// these out by WHERE key_id != activeID, so reaching here is a
		// race (a concurrent Put on the same ref). Idempotent.
		return nil
	}
	oldAEAD, err := d.keyID(s.KeyID)
	if err != nil {
		// The row's key isn't in our map anymore — we cannot migrate it.
		// Surface the error so the worker logs and continues with the next
		// row; the operator will see these accumulate and re-install the
		// missing key.
		return fmt.Errorf("db store: reencrypt %q: old key %q unavailable: %w", ref, s.KeyID, err)
	}
	password, _, err := decryptWith(oldAEAD, s.Password, defaultKeyID)
	if err != nil {
		return fmt.Errorf("db store: reencrypt %q: decrypt password: %w", ref, err)
	}
	apiKey, _, err := decryptWith(oldAEAD, s.APIKey, defaultKeyID)
	if err != nil {
		return fmt.Errorf("db store: reencrypt %q: decrypt api_key: %w", ref, err)
	}
	newPassword, err := encryptWith(aead, password, activeID)
	if err != nil {
		return fmt.Errorf("db store: reencrypt %q: re-encrypt password: %w", ref, err)
	}
	newAPIKey, err := encryptWith(aead, apiKey, activeID)
	if err != nil {
		return fmt.Errorf("db store: reencrypt %q: re-encrypt api_key: %w", ref, err)
	}
	if _, err := d.ent.PlatformSecret.Update().
		Where(platformsecret.Ref(ref)).
		SetPassword(newPassword).
		SetAPIKey(newAPIKey).
		SetKeyID(activeID).
		Save(ctx); err != nil {
		return fmt.Errorf("db store: reencrypt %q: save: %w", ref, err)
	}
	return nil
}
