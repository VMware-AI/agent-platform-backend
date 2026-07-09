// Package gateway is a client for the LiteLLM proxy admin API. The backend
// governs per-user virtual keys, budgets and routing through this client rather
// than reimplementing the gateway. See LLD-04.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	gatewayHTTPTimeout  = 15 * time.Second
	gatewayRetryBackoff = 200 * time.Millisecond
	defaultMaxAttempts  = 3
)

// RetryPolicy controls per-method retry behaviour. Zero value = the default
// policy (GET retries 3× on 5xx/transport, POST never retries). Configure via
// NewHTTPClient options (WithRetryBackoff, WithMaxAttempts, WithPOSTRetryOn5xx).
type RetryPolicy struct {
	GETMaxAttempts  int           // total attempts including the first; 0 → 3
	POSTMaxAttempts int           // total attempts including the first; 0 → 1
	POSTRetryOn5xx  bool          // if true, POST retries 5xx (NOT 4xx); default false
	RetryBackoff    time.Duration // linear backoff per attempt; 0 → 200ms
}

// AuthFunc attaches credentials to a request. The default is Bearer; pass a
// custom AuthFunc (via WithAuthFunc) to use litellm's x-litellm-api-key header
// (or any other scheme) instead.
type AuthFunc func(*http.Request)

// Option configures a client at construction. The zero value of every option
// is a no-op, so unconfigured clients use the package defaults (Bearer auth,
// 3 GET attempts, 1 POST attempt, 200ms backoff).
type Option func(*HTTPClient)

// WithRetryBackoff sets the linear backoff between retry attempts (default
// 200ms). Test code uses this to keep the retry path near-instant.
func WithRetryBackoff(d time.Duration) Option {
	return func(c *HTTPClient) { c.policy.RetryBackoff = d }
}

// WithGETMaxAttempts overrides the GET retry count (default 3).
func WithGETMaxAttempts(n int) Option {
	return func(c *HTTPClient) { c.policy.GETMaxAttempts = n }
}

// WithPOSTMaxAttempts overrides the POST retry count (default 1, i.e. no retry).
// Use WithPOSTRetryOn5xx to opt into 5xx retries specifically.
func WithPOSTMaxAttempts(n int) Option {
	return func(c *HTTPClient) { c.policy.POSTMaxAttempts = n }
}

// WithPOSTRetryOn5xx enables 5xx retries for POST. Default off — mutations are
// exactly-once (LLD-04 §2). Enable only for safe idempotent endpoints.
func WithPOSTRetryOn5xx(enabled bool) Option {
	return func(c *HTTPClient) { c.policy.POSTRetryOn5xx = enabled }
}

// WithAuthFunc replaces the default Bearer Authorization header. Useful for
// litellm deployments that authenticate via x-litellm-api-key (e.g. behind a
// reverse proxy that strips Authorization).
func WithAuthFunc(auth AuthFunc) Option {
	return func(c *HTTPClient) { c.auth = auth }
}

// NewHTTPClient returns a gateway client with the default Bearer auth, 3 GET
// retry attempts, 1 POST attempt (no retry), 200ms backoff. baseURL is the
// proxy base (e.g. http://litellm:4000); masterKey authenticates admin calls.
// Both inputs are required — an empty key would emit "Authorization: Bearer "
// and 401 every request.
//
// Use the WithXxx options to override defaults; see Option.
func NewHTTPClient(baseURL, masterKey string, opts ...Option) (*HTTPClient, error) {
	if baseURL == "" {
		return nil, errors.New("gateway: baseURL required")
	}
	if masterKey == "" {
		return nil, errors.New("gateway: masterKey required")
	}
	c := &HTTPClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		masterKey: masterKey,
		http:      &http.Client{Timeout: gatewayHTTPTimeout},
		policy:    defaultRetryPolicy(),
		auth:      defaultAuthFunc(masterKey),
		breaker:   newCircuitBreaker(3, 30*time.Second),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

func defaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		GETMaxAttempts:  defaultMaxAttempts,
		POSTMaxAttempts: 1,
		RetryBackoff:    gatewayRetryBackoff,
	}
}

func defaultAuthFunc(masterKey string) AuthFunc {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+masterKey)
	}
}

// HTTPClient talks to a LiteLLM proxy over HTTP. Safe for concurrent use.
type HTTPClient struct {
	baseURL   string
	masterKey string
	http      *http.Client
	policy    RetryPolicy
	auth      AuthFunc
	breaker   *circuitBreaker
}

// --- Client (key/team governance) ---

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
	UserID string `json:"user_id,omitempty"`
	// KeyAlias is the human-readable name forwarded to LiteLLM as `key_alias`.
	// The platform-side ent.VirtualKey.Name is the source of truth for our
	// display; we mirror it onto the gateway so /key/list shows it too and
	// spend.go's list-render path can use it as a label.
	KeyAlias            string   `json:"key_alias,omitempty"`
	TeamID              string   `json:"team_id,omitempty"`
	Models              []string `json:"models,omitempty"`
	MaxBudget           *float64 `json:"max_budget,omitempty"`
	BudgetDuration      string   `json:"budget_duration,omitempty"`
	RPMLimit            *int     `json:"rpm_limit,omitempty"`
	TPMLimit            *int     `json:"tpm_limit,omitempty"`
	RPMLimitType        string   `json:"rpm_limit_type,omitempty"`
	TPMLimitType        string   `json:"tpm_limit_type,omitempty"`
	MaxParallelRequests *int     `json:"max_parallel_requests,omitempty"`
	AllowedRoutes       []string `json:"allowed_routes,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	Blocked             *bool    `json:"blocked,omitempty"`
	KeyType             string   `json:"key_type,omitempty"`
	AutoRotate          *bool    `json:"auto_rotate,omitempty"`
	RotationInterval    string   `json:"rotation_interval,omitempty"`
	// Metadata is opaque auxiliary payload forwarded to the gateway as-is.
	// Values may be scalars (the deploy flows' "agent": <name>) or nested
	// JSON (the issue flow's "tags": [...]).
	Metadata map[string]any `json:"metadata,omitempty"`
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
	if err := c.post(ctx, "/"+url.PathEscape("key/"+key+"/regenerate"), map[string]any{}, &out); err != nil {
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

// --- Low-level HTTP ---

// get sends an admin GET with the configured AuthFunc and decodes the JSON
// response. Retries transient failures (transport errors + 5xx) per the
// configured policy. 4xx and decode errors are terminal — returned once.
func (c *HTTPClient) get(ctx context.Context, path string, out any) error {
	attempts := c.policy.GETMaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := sleepCtx(ctx, c.policy.RetryBackoff*time.Duration(attempt-1)); err != nil {
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
// re-trying (transport error or 5xx) vs terminal (4xx, decode error, breaker
// open).
func (c *HTTPClient) getOnce(ctx context.Context, path string, out any) (retryable bool, err error) {
	// logPath never carries the raw query string (which may hold a secret, e.g.
	// /key/info?key=sk-...); the request itself still uses the full path.
	logPath := redactPath(path)
	if !c.breaker.allow() {
		return false, fmt.Errorf("%w: %s", ErrUnavailable, logPath)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return false, &Error{Method: http.MethodGet, Path: logPath, Cause: err}
	}
	c.auth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		// The CALLER giving up (its ctx cancelled / its own deadline) is not a
		// gateway fault: don't feed the breaker and don't retry (#87). Gate on
		// ctx.Err() so this catches only the caller's ctx — NOT the transport-level
		// http.Client.Timeout, which Go ALSO surfaces as context.DeadlineExceeded
		// but which means the gateway hung and MUST still trip the breaker.
		if isContextError(err) && ctx.Err() != nil {
			return false, &Error{Method: http.MethodGet, Path: logPath, Cause: err}
		}
		c.breaker.record(ErrTransport)
		slog.WarnContext(ctx, "gateway transport error",
			"base_url", c.baseURL, "path", logPath, "err", err)
		return true, &Error{Method: http.MethodGet, Path: logPath, Cause: fmt.Errorf("%w: %v", ErrTransport, err)}
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := redactSecrets(string(data))
		slog.WarnContext(ctx, "gateway request failed",
			"base_url", c.baseURL, "path", logPath, "status", resp.StatusCode, "body", body)
		gwErr := &Error{Method: http.MethodGet, Path: logPath, Status: resp.StatusCode, Body: body}
		if sentinel := sentinelFromStatus(resp.StatusCode); sentinel != nil {
			if resp.StatusCode >= 500 {
				c.breaker.record(sentinel)
				return true, fmt.Errorf("%w: %w", gwErr, sentinel)
			}
			return false, fmt.Errorf("%w: %w", gwErr, sentinel)
		}
		return false, gwErr
	}
	c.breaker.record(nil) // success closes the breaker
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			slog.WarnContext(ctx, "gateway decode error",
				"base_url", c.baseURL, "path", logPath, "err", err)
			return false, &Error{
				Method: http.MethodGet, Path: logPath,
				Cause: fmt.Errorf("%w: %v", ErrMalformedResponse, err),
			}
		}
	}
	return false, nil
}

// sleepCtx waits d or until ctx is canceled, whichever comes first.
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

// post sends an admin POST with the configured AuthFunc and decodes the JSON
// response. Default policy: exactly-once, no retry (LLD-04 §2 — mutations
// can't be safely retried without server-side idempotency). Enable via
// RetryPolicy.POSTRetryOn5xx for retryable 5xx paths (the caller is
// responsible for the upstream key/PUT semantics being safe).
func (c *HTTPClient) post(ctx context.Context, path string, body, out any) error {
	attempts := c.policy.POSTMaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := sleepCtx(ctx, c.policy.RetryBackoff*time.Duration(attempt-1)); err != nil {
				return err
			}
		}
		retryable, err := c.postOnce(ctx, path, body, out)
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

func (c *HTTPClient) postOnce(ctx context.Context, path string, body, out any) (retryable bool, err error) {
	// logPath never carries the raw query string (which may hold a secret); the
	// request itself still uses the full path.
	logPath := redactPath(path)
	if !c.breaker.allow() {
		return false, fmt.Errorf("%w: %s", ErrUnavailable, logPath)
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return false, &Error{Method: http.MethodPost, Path: logPath, Cause: fmt.Errorf("marshal: %w", err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return false, &Error{Method: http.MethodPost, Path: logPath, Cause: err}
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// The CALLER giving up (its ctx cancelled / its own deadline) is not a
		// gateway fault: don't feed the breaker and don't retry (#87). Gate on
		// ctx.Err() so this catches only the caller's ctx — NOT the transport-level
		// http.Client.Timeout, which Go ALSO surfaces as context.DeadlineExceeded
		// but which means the gateway hung and MUST still trip the breaker.
		if isContextError(err) && ctx.Err() != nil {
			return false, &Error{Method: http.MethodPost, Path: logPath, Cause: err}
		}
		c.breaker.record(ErrTransport)
		slog.WarnContext(ctx, "gateway transport error",
			"base_url", c.baseURL, "path", logPath, "err", err)
		return true, &Error{Method: http.MethodPost, Path: logPath, Cause: fmt.Errorf("%w: %v", ErrTransport, err)}
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := redactSecrets(string(data))
		slog.WarnContext(ctx, "gateway request failed",
			"base_url", c.baseURL, "path", logPath, "status", resp.StatusCode, "body", body)
		gwErr := &Error{Method: http.MethodPost, Path: logPath, Status: resp.StatusCode, Body: body}
		if sentinel := sentinelFromStatus(resp.StatusCode); sentinel != nil {
			if resp.StatusCode >= 500 {
				// 5xx always feeds the breaker — the gateway is unhealthy regardless
				// of method (#87, "5xx-only breaker"). Recording is decoupled from the
				// retry decision: RETRYING is still opt-in via POSTRetryOn5xx (mutations
				// are exactly-once by default), but the breaker must see the failure
				// either way, otherwise a run of 5xx POSTs never opens it.
				c.breaker.record(sentinel)
				return c.policy.POSTRetryOn5xx, fmt.Errorf("%w: %w", gwErr, sentinel)
			}
			// 4xx (incl. 401/403/404) is a caller error — terminal, and it must NOT
			// trip the breaker (re-POSTing won't change a malformed request, and the
			// gateway is healthy).
			return false, fmt.Errorf("%w: %w", gwErr, sentinel)
		}
		return false, gwErr
	}
	c.breaker.record(nil)
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			slog.WarnContext(ctx, "gateway decode error",
				"base_url", c.baseURL, "path", path, "method", http.MethodPost, "err", err)
			return false, &Error{
				Method: http.MethodPost, Path: logPath,
				Cause: fmt.Errorf("%w: %v", ErrMalformedResponse, err),
			}
		}
	}
	slog.InfoContext(ctx, "gateway request ok",
		"base_url", c.baseURL, "method", http.MethodPost, "path", path, "status", resp.StatusCode)
	return false, nil
}

// patch sends a PATCH with the configured AuthFunc and decodes the JSON
// response. It mirrors c.post — same retry policy (POSTRetryOn5xx gate), same
// breaker, same redact-on-error — but the verb differs because PATCH is the
// idiomatic partial-update verb on LiteLLM's /model/{id}/update. Mutations
// are still exactly-once by default (LLD-04 §2), so the default
// POSTMaxAttempts=1 carries over.
func (c *HTTPClient) patch(ctx context.Context, path string, body, out any) error {
	attempts := c.policy.POSTMaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := sleepCtx(ctx, c.policy.RetryBackoff*time.Duration(attempt-1)); err != nil {
				return err
			}
		}
		retryable, err := c.patchOnce(ctx, path, body, out)
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

func (c *HTTPClient) patchOnce(ctx context.Context, path string, body, out any) (retryable bool, err error) {
	if !c.breaker.allow() {
		return false, fmt.Errorf("%w: %s", ErrUnavailable, path)
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return false, &Error{Method: http.MethodPatch, Path: path, Cause: fmt.Errorf("marshal: %w", err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return false, &Error{Method: http.MethodPatch, Path: path, Cause: err}
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		c.breaker.record(ErrTransport)
		slog.WarnContext(ctx, "gateway transport error",
			"base_url", c.baseURL, "path", path, "err", err)
		return true, &Error{Method: http.MethodPatch, Path: path, Cause: fmt.Errorf("%w: %v", ErrTransport, err)}
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := redactSecrets(string(data))
		slog.WarnContext(ctx, "gateway request failed",
			"base_url", c.baseURL, "path", path, "status", resp.StatusCode, "body", body)
		gwErr := &Error{Method: http.MethodPatch, Path: path, Status: resp.StatusCode, Body: body}
		if sentinel := sentinelFromStatus(resp.StatusCode); sentinel != nil {
			if resp.StatusCode >= 500 && c.policy.POSTRetryOn5xx {
				c.breaker.record(sentinel)
				return true, fmt.Errorf("%w: %w", gwErr, sentinel)
			}
			return false, fmt.Errorf("%w: %w", gwErr, sentinel)
		}
		return false, gwErr
	}
	c.breaker.record(nil)
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return false, &Error{
				Method: http.MethodPatch, Path: path,
				Cause: fmt.Errorf("%w: %v", ErrMalformedResponse, err),
			}
		}
	}
	return false, nil
}

// redactPath returns a request path safe to log or store in Error.Path: the
// path component is kept, but the raw query string is dropped (replaced with a
// "?[REDACTED]" marker when present). Some endpoints put secrets in the query —
// e.g. spend.go's BudgetInfo builds "/key/info?key=sk-<real>" — so logging the
// verbatim path (#95's leak, closed there for bodies) would leak the key. The
// path segment alone (/key/info, /team/info) is the useful diagnostic signal.
func redactPath(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i] + "?[REDACTED]"
	}
	return path
}

// redactSecrets strips bearer / api-key content from response bodies so a
// leaked 4xx body never propagates a master key to logs. Uses strings.ReplaceAll
// with sentinel placeholders so the redacted output round-trips cleanly through
// repeated calls.
func redactSecrets(body string) string {
	if body == "" {
		return body
	}
	// Order matters: longer prefixes first so "sk-live-" wins over "sk-".
	// We replace `prefix + token` with `prefix + [REDACTED]` (prefix itself
	// is kept, the variable-length token portion is what leaks the secret).
	type pattern struct{ prefix, stop string }
	patterns := []pattern{
		{"sk-live-", " \"}`,\n\t"},
		{"sk-test-", " \"}`,\n\t"},
		{"sk-local-", " \"}`,\n\t"},
		{"Bearer ", " \"}`,\n\t"},
		// Bare "sk-" LAST (#95): litellm-issued virtual keys carry no named
		// prefix (just "sk-<random>") and 4xx bodies echo the request key, so
		// without this entry those keys reached the logs unredacted. Running
		// after the longer prefixes, the sentinel guard in replaceTokens keeps
		// their already-redacted output intact.
		{"sk-", " \"}`,\n\t"},
	}
	for _, p := range patterns {
		body = replaceTokens(body, p.prefix, p.stop)
	}
	return body
}

// replaceTokens replaces every occurrence of `prefix + run-of-non-stop-runes`
// with `prefix + [REDACTED]`. Stops at any rune in stop (single-byte ascii).
func replaceTokens(s, prefix, stop string) string {
	if !strings.Contains(s, prefix) {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	i := 0
	for {
		j := strings.Index(s[i:], prefix)
		if j < 0 {
			out.WriteString(s[i:])
			return out.String()
		}
		j += i
		// A key boundary is preceded by whitespace/punctuation, never by a
		// word byte — skip matches embedded in longer words ("task-", "desk-")
		// so the bare "sk-" pattern doesn't mangle ordinary log text.
		if j > 0 && isWordByte(s[j-1]) {
			out.WriteString(s[i : j+len(prefix)])
			i = j + len(prefix)
			continue
		}
		// Copy everything up to and INCLUDING the prefix.
		out.WriteString(s[i : j+len(prefix)])
		// Skip the token (prefix+1 onwards), emit [REDACTED].
		k := j + len(prefix)
		for k < len(s) && !strings.ContainsRune(stop, rune(s[k])) {
			k++
		}
		// Already handled by an earlier, longer-prefix pass (e.g. the bare
		// "sk-" pass seeing "sk-live-[REDACTED]"): keep that output verbatim
		// instead of re-consuming the sentinel. Safe because real keys are
		// alphanumeric and can never contain "[REDACTED]".
		if tok := s[j+len(prefix) : k]; strings.HasSuffix(tok, "[REDACTED]") {
			out.WriteString(tok)
		} else {
			out.WriteString("[REDACTED]")
		}
		i = k
	}
}

// isWordByte reports whether b can appear inside an ordinary word — used to
// reject secret-prefix matches that are really the tail of a longer token.
func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

// isContextError reports whether err is (or wraps) a context cancellation or
// deadline. Such an error means the CALLER gave up (request cancelled, per-call
// timeout) — it is not evidence the gateway is unhealthy, so it must neither trip
// the circuit breaker nor be retried. Guarding it keeps a burst of cancellations
// (e.g. a shutdown) from falsely opening the breaker (#87).
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// --- circuit breaker ---

// circuitBreaker opens after N consecutive 5xx/transport failures and stays
// open for a cooldown; a single success closes it. 4xx does not trip the
// breaker (caller errors are not the server's fault).
type circuitBreaker struct {
	mu          sync.Mutex
	consecutive int
	openUntil   time.Time
	threshold   int
	cooldown    time.Duration
	now         func() time.Time // injectable for tests
}

func newCircuitBreaker(threshold int, cooldown time.Duration) *circuitBreaker {
	return &circuitBreaker{
		threshold: threshold,
		cooldown:  cooldown,
		now:       time.Now,
	}
}

// allow reports whether the next request should be sent. While the breaker is
// open (openUntil is in the future) every call returns false until the cooldown
// elapses, at which point the next call returns true and the breaker
// half-opens (success closes, failure re-opens).
func (b *circuitBreaker) allow() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true
	}
	if b.now().After(b.openUntil) {
		// half-open: allow one request to test recovery. Reset counter so a
		// single failure re-opens cleanly.
		b.consecutive = 0
		b.openUntil = time.Time{}
		return true
	}
	return false
}

// record updates the breaker after a request. nil = success; non-nil = the
// sentinel (caller passes ErrUnavailable, ErrTransport — anything else is
// treated as a 4xx-class failure that doesn't trip the breaker).
func (b *circuitBreaker) record(failureSentinel error) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if failureSentinel == nil {
		// success closes the breaker
		b.consecutive = 0
		b.openUntil = time.Time{}
		return
	}
	// Only 5xx / transport failures count. 4xx sentinels (Unauthorized /
	// Forbidden / NotFound) are caller errors — they should not affect the
	// breaker's view of gateway health.
	if failureSentinel != ErrUnavailable && failureSentinel != ErrTransport {
		return
	}
	b.consecutive++
	if b.consecutive >= b.threshold {
		b.openUntil = b.now().Add(b.cooldown)
	}
}
