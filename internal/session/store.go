// Package session provides server-side session storage (LLD-01 §3).
// M1.0 ships an in-memory store; a Redis-backed store implements the same
// interface for production (sessions live in redis, see HLD §5.2).
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned when a session id is unknown or expired.
var ErrNotFound = errors.New("session not found")

// Data is the payload stored for an authenticated session.
type Data struct {
	UserID     string
	Username   string
	Role       string
	TenantID   string
	MustChange bool
	IP         string
	ExpiresAt  time.Time
}

// Store is the session persistence contract. Implemented by MemoryStore (dev/test)
// and (later) a Redis store for production.
type Store interface {
	Create(data Data) (id string, err error)
	Get(id string) (Data, error)
	Delete(id string) error
}

// NewID returns a cryptographically random 256-bit session id.
func NewID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// MemoryStore is a goroutine-safe in-memory Store for dev/test.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]Data
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]Data)}
}

func (m *MemoryStore) Create(d Data) (string, error) {
	id, err := NewID()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.data[id] = d
	m.mu.Unlock()
	return id, nil
}

func (m *MemoryStore) Get(id string) (Data, error) {
	m.mu.RLock()
	d, ok := m.data[id]
	m.mu.RUnlock()
	if !ok {
		return Data{}, ErrNotFound
	}
	if time.Now().After(d.ExpiresAt) {
		_ = m.Delete(id)
		return Data{}, ErrNotFound
	}
	return d, nil
}

func (m *MemoryStore) Delete(id string) error {
	m.mu.Lock()
	delete(m.data, id)
	m.mu.Unlock()
	return nil
}
