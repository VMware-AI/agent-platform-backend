package session

import (
	"errors"
	"testing"
	"time"
)

func TestMemoryStore_CreateGetDelete(t *testing.T) {
	s := NewMemoryStore()
	id, err := s.Create(Data{UserID: "u1", Role: "admin", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(id) != 64 { // 32 bytes hex
		t.Fatalf("session id length = %d, want 64", len(id))
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserID != "u1" || got.Role != "admin" {
		t.Fatalf("Get = %+v", got)
	}
	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete want ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_Expiry(t *testing.T) {
	s := NewMemoryStore()
	id, _ := s.Create(Data{UserID: "u1", ExpiresAt: time.Now().Add(-time.Second)})
	if _, err := s.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session want ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_DeleteByUser(t *testing.T) {
	s := NewMemoryStore()
	exp := time.Now().Add(time.Hour)
	// two sessions for u1 (two devices), one for u2
	a, _ := s.Create(Data{UserID: "u1", ExpiresAt: exp})
	b, _ := s.Create(Data{UserID: "u1", ExpiresAt: exp})
	c, _ := s.Create(Data{UserID: "u2", ExpiresAt: exp})

	if err := s.DeleteByUser("u1"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	// both of u1's sessions are gone
	if _, err := s.Get(a); !errors.Is(err, ErrNotFound) {
		t.Fatalf("u1 session a should be revoked, got %v", err)
	}
	if _, err := s.Get(b); !errors.Is(err, ErrNotFound) {
		t.Fatalf("u1 session b should be revoked, got %v", err)
	}
	// u2's session survives
	if _, err := s.Get(c); err != nil {
		t.Fatalf("u2 session must survive, got %v", err)
	}
	// the user index is cleaned up (no leak): deleting again is a no-op
	if err := s.DeleteByUser("u1"); err != nil {
		t.Fatalf("DeleteByUser idempotent: %v", err)
	}
}

// Delete must also de-index the session from its user set (no stale index entry
// that a later DeleteByUser would resurrect).
func TestMemoryStore_DeleteDeindexes(t *testing.T) {
	s := NewMemoryStore()
	id, _ := s.Create(Data{UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)})
	_ = s.Delete(id)
	if set, ok := s.byUser["u1"]; ok && len(set) > 0 {
		t.Fatalf("user index not cleaned after Delete: %v", set)
	}
}

func TestNewID_Unique(t *testing.T) {
	a, _ := NewID()
	b, _ := NewID()
	if a == b {
		t.Fatal("session ids must be unique")
	}
}
