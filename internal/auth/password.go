// Package auth provides password hashing and session/RBAC primitives for the
// Agent Platform control plane. See design: LLD-01 (data model + RBAC).
package auth

import (
	"errors"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the bcrypt work factor. LLD-01 requires >= 12.
const BcryptCost = 12

// MinPasswordLength is the minimum acceptable password length.
const MinPasswordLength = 12

var (
	// ErrPasswordTooShort is returned when a password is below MinPasswordLength.
	ErrPasswordTooShort = errors.New("password must be at least 12 characters")
	// ErrPasswordTooWeak is returned when a password lacks character diversity.
	ErrPasswordTooWeak = errors.New("password must include upper, lower, and digit characters")
	// ErrPasswordMismatch is returned when a password does not match its hash.
	ErrPasswordMismatch = errors.New("password does not match")
)

// HashPassword validates strength and returns a bcrypt hash.
// Validation happens at this boundary (fail fast) before any storage.
func HashPassword(plain string) (string, error) {
	if err := ValidatePasswordStrength(plain); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword reports whether plain matches the stored bcrypt hash.
// Returns ErrPasswordMismatch on a non-match (never leaks timing-sensitive detail).
func VerifyPassword(hash, plain string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		return ErrPasswordMismatch
	}
	return nil
}

// ValidatePasswordStrength enforces length + character diversity.
func ValidatePasswordStrength(plain string) error {
	if len(plain) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	var hasUpper, hasLower, hasDigit bool
	for _, r := range plain {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		return ErrPasswordTooWeak
	}
	return nil
}
