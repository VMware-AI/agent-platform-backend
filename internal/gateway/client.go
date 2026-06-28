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
	"net/url"
	"strings"
	"time"
)

const (
	gatewayHTTPTimeout  = 15 * time.Second
	gatewayRetryBackoff = 200 * time.Millisecond
)

// Client governs the LiteLLM proxy via its admin API.
type Client interface {
	GenerateKey(ctx context.Context, req GenerateKeyRequest) (*KeyResponse, error)
	UpdateKey(ctx context.Context, req UpdateKeyRequest) error
	DeleteKey(ctx context.Context, key string) error
	// RegenerateKey rotates a key's secret, returning the new one (POST
	// /key/{key}/regenerate). The governance row/binding is unchanged. LLD-04 §3.
	RegenerateKey(ctx context.Context, key string) (*KeyResponse, error)
	CreateTeam(ctx context.Context, req TeamRequest) (*TeamResponse, error)
	DeleteTeam(ctx context.Context, teamID string) error
	// ListKeys enumerates the keys the gateway currently holds, for
	// reconciliation against the platform's governance rows (see internal/reconcile).
	ListKeys(ctx context.Context) ([]KeyInfo, error)
	// ListTeams enumerates the teams the gateway currently holds, for
	// reconciliation against department rows (see internal/reconcile).
	ListTeams(ctx context.Context) ([]TeamInfo, error)
}

// KeyInfo identifies a key as the gateway reports it (GET /key/list). Key is the
// comparable identifier: LiteLLM lists the hashed token, which is persisted at
// issue time as VirtualKey.litellm_token, so reconciliation matches by it (the raw
// litellm_key, never returned by /key/list, is matched too for legacy rows).
type KeyInfo struct {
	Key    string
	UserID string
	TeamID string
}

// TeamInfo identifies a team as the gateway reports it (GET /team/list). TeamID
// matches a Department.litellm_team_id.
type TeamInfo struct {
	TeamID string
	Alias  string
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
	Key string `json:"key"` // raw secret (sk-...), surfaced once
	// Token is LiteLLM's hashed key identifier — the value GET /key/list reports.
	// Persisted so reconciliation can match by it instead of the raw key (which
	// /key/list never returns). Empty if the gateway version omits it.
	Token     string   `json:"token"`
	Expires   string   `json:"expires"`
	UserID    string   `json:"user_id"`
	TeamID    string   `json:"team_id"`
	MaxBudget *float64 `json:"max_budget"`
	Spend     float64  `json:"spend"`
}

// UpdateKeyRequest changes budget/limits on an existing key, or toggles it
// blocked/unblocked (litellm /key/update). All fields are optional — only set
// ones change.
type UpdateKeyRequest struct {
	Key       string   `json:"key"`
	MaxBudget *float64 `json:"max_budget,omitempty"`
	RPMLimit  *int     `json:"rpm_limit,omitempty"`
	TPMLimit  *int     `json:"tpm_limit,omitempty"`
	// Blocked toggles the key's enabled state at the gateway: true disables it
	// (requests rejected) and false re-enables it, without deleting the key.
	Blocked *bool `json:"blocked,omitempty"`
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
	// Lightweight retry for IDEMPOTENT reads only (LLD-04 §2). Mutations (post)
	// are exactly-once and never retried.
	maxAttempts  int
	retryBackoff time.Duration
}

// NewHTTPClient returns a gateway client. baseURL is the proxy base (e.g.
// http://litellm:4000); masterKey authenticates admin calls.
func NewHTTPClient(baseURL, masterKey string) *HTTPClient {
	return &HTTPClient{
		baseURL:      strings.TrimRight(baseURL, "/"),
		masterKey:    masterKey,
		http:         &http.Client{Timeout: gatewayHTTPTimeout},
		maxAttempts:  3,
		retryBackoff: gatewayRetryBackoff,
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

func (c *HTTPClient) RegenerateKey(ctx context.Context, key string) (*KeyResponse, error) {
	if key == "" {
		return nil, fmt.Errorf("RegenerateKey: key is required")
	}
	var out KeyResponse
	if err := c.post(ctx, "/key/"+url.PathEscape(key)+"/regenerate", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
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

// ListKeys enumerates the gateway's keys via GET /key/list. The wire item carries
// both a hashed token and (when configured) a raw key; the raw key wins as the
// comparable identifier, falling back to the token.
func (c *HTTPClient) ListKeys(ctx context.Context) ([]KeyInfo, error) {
	var out struct {
		Keys []struct {
			Token  string `json:"token"`
			Key    string `json:"key"`
			UserID string `json:"user_id"`
			TeamID string `json:"team_id"`
		} `json:"keys"`
	}
	if err := c.get(ctx, "/key/list", &out); err != nil {
		return nil, err
	}
	keys := make([]KeyInfo, 0, len(out.Keys))
	for _, k := range out.Keys {
		id := k.Key
		if id == "" {
			id = k.Token
		}
		if id == "" {
			continue // unidentifiable entry — skip rather than treat "" as an orphan
		}
		keys = append(keys, KeyInfo{Key: id, UserID: k.UserID, TeamID: k.TeamID})
	}
	return keys, nil
}

// ListTeams enumerates the gateway's teams via GET /team/list. LiteLLM returns a
// top-level array of team objects.
func (c *HTTPClient) ListTeams(ctx context.Context) ([]TeamInfo, error) {
	var raw []struct {
		TeamID    string `json:"team_id"`
		TeamAlias string `json:"team_alias"`
	}
	if err := c.get(ctx, "/team/list", &raw); err != nil {
		return nil, err
	}
	teams := make([]TeamInfo, 0, len(raw))
	for _, t := range raw {
		if t.TeamID == "" {
			continue // unidentifiable entry — skip rather than treat "" as an orphan
		}
		teams = append(teams, TeamInfo{TeamID: t.TeamID, Alias: t.TeamAlias})
	}
	return teams, nil
}

// get sends an admin GET with Bearer auth and decodes the JSON response. Because
// GET is idempotent it retries transient failures (transport errors + 5xx) up to
// maxAttempts with a linear backoff; 4xx (client errors) are returned immediately.
func (c *HTTPClient) get(ctx context.Context, path string, out any) error {
	attempts := c.maxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := sleepCtx(ctx, c.retryBackoff*time.Duration(attempt-1)); err != nil {
				return err
			}
		}
		retryable, err := c.getOnce(ctx, path, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable {
			return err
		}
	}
	return lastErr
}

// getOnce performs a single GET. retryable reports whether a failure is worth
// re-trying (transport error or 5xx) vs terminal (4xx, decode error).
func (c *HTTPClient) getOnce(ctx context.Context, path string, out any) (retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.masterKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return true, fmt.Errorf("gateway %s: %w", path, err) // transport error: never reached/completed
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode >= 500, fmt.Errorf("gateway %s: status %d: %s", path, resp.StatusCode, string(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return false, fmt.Errorf("decode %s response: %w", path, err)
		}
	}
	return false, nil
}

// sleepCtx waits for d or until ctx is canceled, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
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
