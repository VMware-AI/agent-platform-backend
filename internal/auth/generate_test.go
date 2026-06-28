package auth

import (
	"strings"
	"testing"
)

func TestGenerateTempPassword_SatisfiesStrength(t *testing.T) {
	// Many iterations: the generator loops until the candidate passes
	// ValidatePasswordStrength, so every returned value must validate and the
	// loop must terminate well within the iteration budget.
	const iterations = 200
	seen := make(map[string]struct{}, iterations)
	for i := 0; i < iterations; i++ {
		pw, err := GenerateTempPassword()
		if err != nil {
			t.Fatalf("GenerateTempPassword: %v", err)
		}
		if err := ValidatePasswordStrength(pw); err != nil {
			t.Fatalf("generated password %q failed strength check: %v", pw, err)
		}
		if len(pw) != 16 {
			t.Fatalf("generated password length = %d, want 16", len(pw))
		}
		// Every rune must come from the unambiguous alphabet.
		for _, r := range pw {
			if !strings.ContainsRune(pwAlphabet, r) {
				t.Fatalf("generated password %q contains out-of-alphabet rune %q", pw, r)
			}
		}
		seen[pw] = struct{}{}
	}
	// With 16 chars from a 56-symbol alphabet, collisions across 200 draws are
	// astronomically unlikely; a near-total set proves the source is random, not
	// constant.
	if len(seen) < iterations-1 {
		t.Fatalf("expected ~%d distinct passwords, got %d (generator not random?)", iterations, len(seen))
	}
}

// The generated password must be usable as a real credential: it hashes and
// verifies through the same path login uses.
func TestGenerateTempPassword_HashesAndVerifies(t *testing.T) {
	pw, err := GenerateTempPassword()
	if err != nil {
		t.Fatalf("GenerateTempPassword: %v", err)
	}
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword(generated): %v", err)
	}
	if err := VerifyPassword(hash, pw); err != nil {
		t.Fatalf("VerifyPassword(generated): %v", err)
	}
}
