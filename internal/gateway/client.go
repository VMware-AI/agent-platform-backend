// Package gateway is a client for the LiteLLM proxy admin API. The backend
// governs per-user virtual keys, budgets and routing through this client rather
// than reimplementing the gateway. See LLD-04.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client governs the LiteLLM proxy via its admin API.
type Client interface {
	GenerateKey(ctx context.Context, req GenerateKeyRequest) (*KeyResponse, error)
	UpdateKey(ctx context.Context, req UpdateKeyRequest) error
	DeleteKey(ctx context.Context, key string) error
	CreateTeam(ctx context.Context, req TeamRequest) (*TeamResponse, error)
	DeleteTeam(ctx context.Context, teamID string) error
}

// GenerateKeyRequest mints a per-user virtual key (LLD-04 §3). Budget/rate
// limits are set HERE (per-key), never globally (research §2.3).
type GenerateKeyRequest struct {
	UserID         string            `json:"user_id,omitempty"`
	TeamID         string            `json:"team_id,omitempty"`
	Models         []string          `json:"models,omitempty"`
	MaxBudget      *float64          `json:"max_budget,omitempty"`
	BudgetDuration string            `json:"budget_duration,omitempty"`
	RPMLimit       *int              `json:"rpm_limit,omitempty"`
	TPMLimit       *int              `json:"tpm_limit,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// KeyResponse is the result of generating/regenerating a key.
type KeyResponse struct {
	Key       string   `json:"key"`
	Expires   string   `json:"expires"`
	UserID    string   `json:"user_id"`
	TeamID    string   `json:"team_id"`
	MaxBudget *float64 `json:"max_budget"`
	Spend     float64  `json:"spend"`
}

// UpdateKeyRequest changes budget/limits on an existing key.
type UpdateKeyRequest struct {
	Key       string   `json:"key"`
	MaxBudget *float64 `json:"max_budget,omitempty"`
	RPMLimit  *int     `json:"rpm_limit,omitempty"`
	TPMLimit  *int     `json:"tpm_limit,omitempty"`
}

// TeamRequest creates a team (= department) carrying a shared budget.
type TeamRequest struct {
	TeamID    string   `json:"team_id,omitempty"`
	TeamAlias string   `json:"team_alias,omitempty"`
	MaxBudget *float64 `json:"max_budget,omitempty"`
	Models    []string `json:"models,omitempty"`
}

// TeamResponse is the result of creating a team.
type TeamResponse struct {
	TeamID string `json:"team_id"`
}

// HTTPClient talks to a LiteLLM proxy over HTTP with the master key.
type HTTPClient struct {
	baseURL   string
	masterKey string
	http      *http.Client
}

// NewHTTPClient returns a gateway client. baseURL is the proxy base (e.g.
// http://litellm:4000); masterKey authenticates admin calls.
func NewHTTPClient(baseURL, masterKey string) *HTTPClient {
	return &HTTPClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		masterKey: masterKey,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *HTTPClient) GenerateKey(ctx context.Context, req GenerateKeyRequest) (*KeyResponse, error) {
	var out KeyResponse
	if err := c.post(ctx, "/key/generate", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) UpdateKey(ctx context.Context, req UpdateKeyRequest) error {
	if req.Key == "" {
		return fmt.Errorf("UpdateKey: key is required")
	}
	return c.post(ctx, "/key/update", req, nil)
}

func (c *HTTPClient) DeleteKey(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("DeleteKey: key is required")
	}
	return c.post(ctx, "/key/delete", map[string]any{"keys": []string{key}}, nil)
}

func (c *HTTPClient) CreateTeam(ctx context.Context, req TeamRequest) (*TeamResponse, error) {
	var out TeamResponse
	if err := c.post(ctx, "/team/new", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) DeleteTeam(ctx context.Context, teamID string) error {
	if teamID == "" {
		return fmt.Errorf("DeleteTeam: teamID is required")
	}
	return c.post(ctx, "/team/delete", map[string]any{"team_ids": []string{teamID}}, nil)
}

// post sends an admin POST with Bearer auth and decodes the JSON response.
func (c *HTTPClient) post(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.masterKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gateway %s: %w", path, err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gateway %s: status %d: %s", path, resp.StatusCode, string(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s response: %w", path, err)
		}
	}
	return nil
}
