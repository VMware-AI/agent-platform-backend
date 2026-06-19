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

func TestNewID_Unique(t *testing.T) {
	a, _ := NewID()
	b, _ := NewID()
	if a == b {
		t.Fatal("session ids must be unique")
	}
}
