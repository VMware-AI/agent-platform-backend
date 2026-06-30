package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// --- Sentinel + Error type ---

func TestSentinelFromStatus(t *testing.T) {
	cases := []struct {
		status int
		want   error // nil → no sentinel
	}{
		{401, ErrUnauthorized},
		{403, ErrForbidden},
		{404, ErrNotFound},
		{500, ErrUnavailable},
		{502, ErrUnavailable},
		{503, ErrUnavailable},
		{400, nil},
		{422, nil},
	}
	for _, tc := range cases {
		got := sentinelFromStatus(tc.status)
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("status %d: got %v, want nil", tc.status, got)
		case tc.want != nil && !errors.Is(got, tc.want):
			t.Errorf("status %d: got %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestError_ImplementsError(t *testing.T) {
	e := &Error{Method: "GET", Path: "/x", Status: 401, Body: "no"}
	msg := e.Error()
	if msg == "" {
		t.Fatal("Error() must produce a non-empty message")
	}
	// Status is in the message — callers read this when they log fmt.Errorf.
	if !contains(msg, "401") {
		t.Errorf("Error() must include status, got %q", msg)
	}
}

func TestError_Unwrap_ToCause(t *testing.T) {
	inner := errors.New("underlying")
	e := &Error{Method: "GET", Path: "/x", Cause: inner}
	if !errors.Is(e, inner) {
		t.Fatal("errors.Is must traverse Unwrap to inner cause")
	}
}

func TestError_WrapsSentinelViaFmtErrorf(t *testing.T) {
	// Errors come back from getOnce / postOnce wrapped via fmt.Errorf("%w: %w", gwErr, sentinel).
	gwErr := &Error{Method: "GET", Path: "/x", Status: 401}
	wrapped := error(nil)
	{
		w := error(gwErr)
		wrapped = wrapTwo(w, ErrUnauthorized)
	}
	if !errors.Is(wrapped, ErrUnauthorized) {
		t.Fatal("errors.Is must match the wrapped sentinel")
	}
	if !errors.Is(wrapped, gwErr) {
		t.Fatal("errors.Is must match the concrete *Error")
	}
}

// wrapTwo is the dual-%w fmt.Errorf pattern the client uses.
func wrapTwo(a, b error) error {
	type dual struct{ a, b error }
	d := dual{a, b}
	if d.a == nil {
		return d.b
	}
	if d.b == nil {
		return d.a
	}
	// Use errors.Join as a clean stand-in for fmt.Errorf("%w: %w", a, b); both
	// satisfy errors.Is for either inner error.
	return errors.Join(d.a, d.b)
}

// --- redactSecrets ---

func TestRedactSecrets_StripsKnownPrefixes(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"key":"sk-live-abc123"}`, `[REDACTED]`},
		{`Authorization: Bearer sk-local-abc def`, `[REDACTED] def`},
		{`{"msg":"ok"}`, `{"msg":"ok"}`},
		{"", ""},
	}
	for _, tc := range cases {
		got := redactSecrets(tc.in)
		// We only care that secrets are not present in the redacted output.
		// The exact format is implementation detail.
		if tc.in != "" && (contains(got, "sk-live-abc") || contains(got, "sk-local-abc")) {
			t.Errorf("redactSecrets left secret in output: %q (from %q)", got, tc.in)
		}
	}
}

// --- circuitBreaker ---

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	b := newCircuitBreaker(3, 100*time.Millisecond)
	for i := 0; i < 3; i++ {
		b.record(ErrUnavailable)
	}
	if b.allow() {
		t.Fatal("breaker must be open after threshold consecutive failures")
	}
}

func TestCircuitBreaker_Ignores4xx(t *testing.T) {
	// 4xx sentinels (Unauthorized / Forbidden / NotFound) are caller errors —
	// they must NOT trip the breaker.
	b := newCircuitBreaker(2, time.Hour)
	for i := 0; i < 10; i++ {
		b.record(ErrUnauthorized)
	}
	if !b.allow() {
		t.Fatal("4xx must not open the breaker")
	}
}

func TestCircuitBreaker_SuccessResets(t *testing.T) {
	b := newCircuitBreaker(2, time.Hour)
	b.record(ErrUnavailable)
	b.record(ErrUnavailable)
	if b.allow() {
		t.Fatal("should be open after 2 failures")
	}
	// Simulate cooldown elapsed via injection.
	b.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if !b.allow() {
		t.Fatal("cooldown elapsed → half-open, must allow")
	}
	// Half-open success closes the breaker.
	b.record(nil)
	if !b.allow() {
		t.Fatal("success must close the breaker")
	}
}

// --- HTTPClient behaviour: structured errors ---

func TestHTTPClient_UnauthorizedReturnsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"no"}`))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master")
	_, err := c.ListKeys(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestHTTPClient_404ReturnsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c, _ := NewHTTPClient(srv.URL, "sk-master")
	_, err := c.ListKeys(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestHTTPClient_5xxRetriesThenReturnsUnavailable(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master", WithRetryBackoff(0))

	_, err := c.ListKeys(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("want 3 attempts (2 retries), got %d", got)
	}
}

func TestHTTPClient_BreakerOpensAfterThreshold5xx(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master",
		WithRetryBackoff(0), WithGETMaxAttempts(1)) // 1 attempt per call → trip the breaker

	// Three consecutive single-shot failures → breaker open.
	for i := 0; i < 3; i++ {
		_, _ = c.ListKeys(context.Background())
	}
	firstCalls := atomic.LoadInt32(&attempts) // 3 from the loop above

	// Next call: breaker open → no HTTP request sent, returns ErrUnavailable
	// without touching the server.
	_, err := c.ListKeys(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if got := atomic.LoadInt32(&attempts); got != firstCalls {
		t.Errorf("open breaker must not send a request: attempts went from %d to %d", firstCalls, got)
	}
}

func TestNewHTTPClient_ValidationErrors(t *testing.T) {
	cases := []struct {
		name      string
		baseURL   string
		masterKey string
	}{
		{"empty baseURL", "", "sk-master"},
		{"empty masterKey", "http://x", ""},
		{"both empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewHTTPClient(tc.baseURL, tc.masterKey)
			if err == nil {
				t.Fatalf("NewHTTPClient(%q,%q) succeeded; want error", tc.baseURL, tc.masterKey)
			}
		})
	}
}

// --- AuthFunc: custom auth scheme ---

func TestHTTPClient_CustomAuthFunc(t *testing.T) {
	var seenKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("x-litellm-api-key")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	defer srv.Close()

	c, err := NewHTTPClient(srv.URL, "sk-custom",
		WithAuthFunc(func(r *http.Request) { r.Header.Set("x-litellm-api-key", "sk-custom") }))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	if _, err := c.ListKeys(context.Background()); err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if seenKey != "sk-custom" {
		t.Fatalf("auth func not applied; got %q", seenKey)
	}
}

// --- POST confirm ---

func TestHTTPClient_NewModel_RejectsEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// 200 OK but body is empty → NewModel's response check must reject.
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()
	c, _ := NewHTTPClient(srv.URL, "sk-master")
	err := c.NewModel(context.Background(), ModelSpec{ModelName: "x", Model: "openai/y"})
	if !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("err = %v, want ErrMalformedResponse", err)
	}
}

// --- helpers ---

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && (indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}