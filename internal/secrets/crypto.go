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

// newAEAD derives a 32-byte AES-256 key from the operator-provided passphrase
// (SHA-256 so any non-empty string works) and returns an AES-GCM AEAD. This is
// the single SECRETS_ENCRYPTION_KEY used everywhere — one key, no dev/prod split.
func newAEAD(key string) (cipher.AEAD, error) {
	if key == "" {
		return nil, errors.New("secrets: encryption key is empty (set SECRETS_ENCRYPTION_KEY)")
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

// encrypt returns base64(nonce || ciphertext+tag) for storage. NOTE: the stored
// format carries no key-version/algorithm prefix — it assumes a single
// SECRETS_ENCRYPTION_KEY. Rotating the key would strand existing rows (they fail
// the GCM tag under the new key); supporting rotation later means adding a version
// byte so old and new ciphertexts can coexist during re-encryption. An empty plaintext
// encrypts to empty so an unset field stays unset (and "is it set?" is answerable
// without decrypting); a fresh random nonce per call makes equal plaintexts differ.
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

// decrypt reverses encrypt. Empty → empty. A wrong key or tampered ciphertext
// fails the GCM auth tag and returns an error (never silently wrong plaintext).
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
