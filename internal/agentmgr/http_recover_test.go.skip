package agentmgr

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// #36/low: the daemon REST plane must mask a handler panic as an opaque 500
// (mirroring the GraphQL RecoverFunc) rather than crashing the server.
func TestRecoverMiddleware_MasksPanic(t *testing.T) {
	h := recoverMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/agents/x/heartbeat", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic should be masked as 500, got %d", rec.Code)
	}
}

// A non-panicking handler passes through untouched.
func TestRecoverMiddleware_PassThrough(t *testing.T) {
	h := recoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("non-panicking handler should pass through, got %d", rec.Code)
	}
}
