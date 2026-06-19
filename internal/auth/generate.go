package auth

import (
	"crypto/rand"
	"math/big"
)

// pwAlphabet excludes visually ambiguous chars (0/O, 1/l/I).
const pwAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// GenerateTempPassword returns a strong random password that satisfies
// ValidatePasswordStrength. Used for admin-initiated password resets.
func GenerateTempPassword() (string, error) {
	for {
		b := make([]byte, 16)
		for i := range b {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(pwAlphabet))))
			if err != nil {
				return "", err
			}
			b[i] = pwAlphabet[n.Int64()]
		}
		s := string(b)
		if ValidatePasswordStrength(s) == nil {
			return s, nil
		}
	}
}
