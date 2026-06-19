package auth

import (
	"errors"
	"testing"
)

func TestHashAndVerifyPassword_RoundTrip(t *testing.T) {
	const pw = "Sup3rSecret!!" // 13 chars, upper+lower+digit
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == pw {
		t.Fatal("hash must not equal plaintext")
	}
	if err := VerifyPassword(hash, pw); err != nil {
		t.Fatalf("VerifyPassword should succeed: %v", err)
	}
	if err := VerifyPassword(hash, "WrongPassword9"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("want ErrPasswordMismatch, got %v", err)
	}
}

func TestValidatePasswordStrength(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		want error
	}{
		{"ok", "GoodPass1234", nil},
		{"short", "Ab1", ErrPasswordTooShort},
		{"no upper", "alllower1234", ErrPasswordTooWeak},
		{"no lower", "ALLUPPER1234", ErrPasswordTooWeak},
		{"no digit", "NoDigitsHere", ErrPasswordTooWeak},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidatePasswordStrength(c.pw); !errors.Is(err, c.want) {
				t.Fatalf("ValidatePasswordStrength(%q) = %v, want %v", c.pw, err, c.want)
			}
		})
	}
}

func TestHashPassword_RejectsWeak(t *testing.T) {
	if _, err := HashPassword("weak"); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("want ErrPasswordTooShort, got %v", err)
	}
}
