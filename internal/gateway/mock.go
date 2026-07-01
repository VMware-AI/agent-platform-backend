package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// MockClient implements Client with an in-memory key/team store.
// Deploy succeeds with real-looking keys; ListKeys/ListTeams return what was
// created through this client. All state is lost on restart — suitable only
// for local development / testing when no LiteLLM proxy is available.
type MockClient struct {
	mu    sync.Mutex
	keys  map[string]*MockKey
	teams map[string]*MockTeam
}

type MockKey struct {
	Token     string
	Secret    string
	UserID    string
	TeamID    string
	Models    []string
	MaxBudget *float64
	Blocked   bool
}

type MockTeam struct {
	TeamID string
	Alias  string
}

func NewMockClient() *MockClient {
	return &MockClient{
		keys:  make(map[string]*MockKey),
		teams: make(map[string]*MockTeam),
	}
}

func genToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *MockClient) GenerateKey(_ context.Context, req GenerateKeyRequest) (*KeyResponse, error) {
	secret := "sk-mock-" + genToken()
	token := genToken()

	m.mu.Lock()
	m.keys[token] = &MockKey{
		Token:     token,
		Secret:    secret,
		UserID:    req.UserID,
		TeamID:    req.TeamID,
		Models:    req.Models,
		MaxBudget: req.MaxBudget,
	}
	m.mu.Unlock()

	return &KeyResponse{
		Key:       secret,
		Token:     token,
		UserID:    req.UserID,
		TeamID:    req.TeamID,
		MaxBudget: req.MaxBudget,
	}, nil
}

func (m *MockClient) UpdateKey(_ context.Context, req UpdateKeyRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[req.Key]
	if !ok {
		return fmt.Errorf("key not found: %s", req.Key)
	}
	if req.MaxBudget != nil {
		k.MaxBudget = req.MaxBudget
	}
	if req.Blocked != nil {
		k.Blocked = *req.Blocked
	}
	return nil
}

func (m *MockClient) DeleteKey(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, key)
	return nil
}

func (m *MockClient) RegenerateKey(_ context.Context, key string) (*KeyResponse, error) {
	m.mu.Lock()
	k, ok := m.keys[key]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("key not found: %s", key)
	}
	newSecret := "sk-mock-" + genToken()
	k.Secret = newSecret
	m.mu.Unlock()

	return &KeyResponse{Key: newSecret, Token: key}, nil
}

func (m *MockClient) CreateTeam(_ context.Context, req TeamRequest) (*TeamResponse, error) {
	tid := req.TeamID
	if tid == "" {
		tid = "team-" + genToken()[:12]
	}
	m.mu.Lock()
	m.teams[tid] = &MockTeam{TeamID: tid, Alias: req.TeamAlias}
	m.mu.Unlock()

	return &TeamResponse{TeamID: tid}, nil
}

func (m *MockClient) DeleteTeam(_ context.Context, teamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.teams, teamID)
	return nil
}

func (m *MockClient) ListKeys(_ context.Context) ([]KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]KeyInfo, 0, len(m.keys))
	for _, k := range m.keys {
		out = append(out, KeyInfo{Key: k.Token, UserID: k.UserID, TeamID: k.TeamID})
	}
	return out, nil
}

func (m *MockClient) ListTeams(_ context.Context) ([]TeamInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TeamInfo, 0, len(m.teams))
	for _, t := range m.teams {
		out = append(out, TeamInfo{TeamID: t.TeamID, Alias: t.Alias})
	}
	return out, nil
}
