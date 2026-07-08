package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Envelope format (stored base64-encoded):
//
//	v0 (legacy): [nonce] [ciphertext+tag]
//	v1 (rotating):  [0x01] [key_id_len:1 byte] [key_id:N bytes] [nonce] [ciphertext+tag]
//
// v0 rows are still readable under the active key (treated as key_id="default"
// by Resolve); v1 lets each row carry the key_id it was sealed under so a
// rotation cycle can re-encrypt them in place while the OLD key still decrypts
// them. The first byte is a version tag — added before the nonce so the
// envelope is self-describing.
//
// Empty plaintext stays empty (a "field not set" reads as empty without
// needing to decrypt). A fresh random nonce per call makes equal plaintexts
// produce different ciphertexts.

// envelopeVersion0 marks a pre-rotation, single-key ciphertext. Stored as the
// leading byte == 0x00 so v0 envelopes (which start with the nonce) are
// distinguishable from v1 envelopes (which start with 0x01).
const envelopeVersion0 byte = 0x00

// envelopeVersion1 marks a rotating, key-id-tagged ciphertext. The
// version byte itself is followed by [key_id_len, key_id, nonce, sealed].
const envelopeVersion1 byte = 0x01

// maxKeyIDLen bounds the key_id field we embed in the envelope. Configured
// key ids are operator-chosen labels; capping them defends against a
// misconfigured SECRETS_ENCRYPTION_KEYS entry that supplies a multi-KB id
// (which would balloon every row's storage).
const maxKeyIDLen = 64

// keyID returns the AEAD for the given key id from the configured keys map,
// or an error if the id is unknown. Used internally by DBStore.
func (d *DBStore) keyID(id string) (cipher.AEAD, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	a, ok := d.keys[id]
	if !ok {
		return nil, fmt.Errorf("secrets: unknown key_id %q (configure SECRETS_ENCRYPTION_KEYS or roll back the row)", id)
	}
	return a, nil
}

// encryptWith seals plaintext under aead and returns the base64 envelope
// tagged with keyID. Empty plaintext → empty string. The caller picks the
// keyID; DBStore passes its active key id here.
func encryptWith(aead cipher.AEAD, plaintext, keyID string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if len(keyID) == 0 || len(keyID) > maxKeyIDLen {
		return "", fmt.Errorf("secrets: key_id length %d out of range (1..%d)", len(keyID), maxKeyIDLen)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secrets: nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), nil)

	buf := make([]byte, 0, 1+1+len(keyID)+len(sealed))
	buf = append(buf, envelopeVersion1)
	buf = append(buf, byte(len(keyID)))
	buf = append(buf, keyID...)
	buf = append(buf, sealed...)
	return base64.StdEncoding.EncodeToString(buf), nil
}

// decryptWith is the inverse of encryptWith: parses the envelope, looks up
// the embedded key_id, and opens the ciphertext under the matching AEAD.
// v0 envelopes (no version byte) are opened under the supplied aead and
// report keyID="default" — preserving pre-rotation rows transparently.
//
// A wrong key (key_id not in the configured map) or any tampered byte fails
// the GCM auth tag and returns an error — never silently wrong plaintext.
func decryptWith(activeAEAD cipher.AEAD, encoded, defaultKeyID string) (plaintext, keyID string, err error) {
	if encoded == "" {
		return "", defaultKeyID, nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", fmt.Errorf("secrets: decode: %w", err)
	}
	if len(raw) == 0 {
		return "", defaultKeyID, errors.New("secrets: empty envelope")
	}

	// v1: leading byte is envelopeVersion1. v0 starts with the nonce —
	// indistinguishable from random, so anything that doesn't START with
	// the v1 tag is treated as v0.
	if raw[0] == envelopeVersion1 {
		if len(raw) < 2 {
			return "", "", errors.New("secrets: v1 envelope truncated (missing key_id length)")
		}
		idLen := int(raw[1])
		if idLen == 0 || idLen > maxKeyIDLen {
			return "", "", fmt.Errorf("secrets: v1 envelope has invalid key_id length %d", idLen)
		}
		if len(raw) < 2+idLen {
			return "", "", errors.New("secrets: v1 envelope truncated (missing key_id)")
		}
		keyID = string(raw[2 : 2+idLen])
		raw = raw[2+idLen:]
	} else {
		keyID = defaultKeyID
		// raw is left as the v0 envelope (starts with nonce).
	}

	ns := activeAEAD.NonceSize()
	if len(raw) < ns {
		return "", keyID, errors.New("secrets: ciphertext too short")
	}
	nonce, sealed := raw[:ns], raw[ns:]
	out, err := activeAEAD.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", keyID, fmt.Errorf("secrets: decrypt failed (wrong key or corrupt data): %w", err)
	}
	return string(out), keyID, nil
}

// encrypt / decrypt are the v0 (legacy, single-key) envelope primitives,
// retained so pre-rotation rows (and tests that round-trip through the
// cipher directly) keep working unchanged. New writes go through
// encryptWith / decryptWith, which embed a key_id for rotation; these
// remain here so v0 rows remain decryptable after a key rotation cycle.
func encrypt(aead cipher.AEAD, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secrets: nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func decrypt(aead cipher.AEAD, encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("secrets: decode: %w", err)
	}
	ns := aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secrets: ciphertext too short")
	}
	nonce, ciphertext := raw[:ns], raw[ns:]
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("secrets: decrypt failed (wrong key or corrupt data): %w", err)
	}
	return string(plaintext), nil
}

// newAEAD derives a 32-byte AES-256 key from the operator-provided passphrase
// (SHA-256 so any non-empty string works) and returns an AES-GCM AEAD. One
// passphrase → one AEAD; DBStore holds a map of these indexed by key id.
func newAEAD(key string) (cipher.AEAD, error) {
	if key == "" {
		return nil, errors.New("secrets: encryption key is empty (set SECRETS_ENCRYPTION_KEY or SECRETS_ENCRYPTION_KEYS)")
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("secrets: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: new gcm: %w", err)
	}
	return aead, nil
}
