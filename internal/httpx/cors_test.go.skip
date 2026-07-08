package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS(t *testing.T) {
	// Allowlist includes a blank and a trailing-slash entry to exercise
	// normalization in the constructor.
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS([]string{"https://app.example.com/", "", "  "})(next)

	t.Run("allowed origin echoed with credentials", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/query", nil)
		req.Header.Set("Origin", "https://app.example.com")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (passes through to next)", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Fatalf("Allow-Origin = %q, want echoed origin", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Fatalf("Allow-Credentials = %q, want true", got)
		}
		if got := rec.Header().Get("Vary"); got != "Origin" {
			t.Fatalf("Vary = %q, want Origin", got)
		}
	})

	t.Run("allowed origin with trailing slash still matches", func(t *testing.T) {
		// The browser sends Origin without a trailing slash, but the raw value is
		// echoed; normalization is only used for the allowlist comparison.
		req := httptest.NewRequest(http.MethodGet, "/query", nil)
		req.Header.Set("Origin", "https://app.example.com/")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com/" {
			t.Fatalf("Allow-Origin = %q, want raw echoed origin", got)
		}
	})

	t.Run("preflight OPTIONS short-circuits 204 with method/header allowances", func(t *testing.T) {
		nextCalled := false
		hh := CORS([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodOptions, "/query", nil)
		req.Header.Set("Origin", "https://app.example.com")
		rec := httptest.NewRecorder()
		hh.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("preflight status = %d, want 204", rec.Code)
		}
		if nextCalled {
			t.Fatal("preflight must short-circuit and NOT call next")
		}
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
			t.Fatal("preflight must set Allow-Methods")
		}
		if got := rec.Header().Get("Access-Control-Max-Age"); got != "600" {
			t.Fatalf("Max-Age = %q, want 600", got)
		}
	})

	t.Run("disallowed origin gets no CORS headers but still proceeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/query", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (CORS does not block, only annotates)", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Allow-Origin = %q, want empty for disallowed origin", got)
		}
	})

	t.Run("no Origin header → no CORS headers (same-origin/non-browser)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/query", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		// Without an allowed Origin, even OPTIONS falls through to next (200), not 204.
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (no preflight short-circuit without allowed origin)", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Allow-Origin = %q, want empty", got)
		}
	})
}
