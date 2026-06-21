// Package agentmgr is the backend side of the agent-manager integration
// (LLD-08): VM enrollment, bearer-token auth, heartbeat processing and
// credential-rotation command dispatch. The VM-side daemon lives elsewhere
// (research/agent-manager-daemon-design.md); this package only serves it.
package agentmgr

import (
	"crypto/rand"
	"encoding/base64"

	"golang.org/x/crypto/bcrypt"
)

// tokenBytes is the entropy of enroll / VM bearer tokens (≥32B per LLD-08 §8).
const tokenBytes = 32

// bcryptCost matches LLD-01 (≥12); tokens are stored as bcrypt fingerprints,
// never plaintext, so a DB leak cannot recover them.
const bcryptCost = 12

// newToken returns a high-entropy URL-safe token (plaintext, caller hashes it).
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the bcrypt fingerprint stored in the DB.
func hashToken(tok string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(tok), bcryptCost)
	return string(h), err
}

// verifyToken reports whether tok matches the stored bcrypt fingerprint.
func verifyToken(hash, tok string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(tok)) == nil
}
