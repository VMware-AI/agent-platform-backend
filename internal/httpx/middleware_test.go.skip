package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestLoggerWith_CapturesStatus(t *testing.T) {
	var gotMethod, gotPath string
	var gotStatus int
	var called bool
	mw := RequestLoggerWith(func(method, path string, status int, _ time.Duration) {
		gotMethod, gotPath, gotStatus, called = method, path, status, true
	})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/query", nil))

	if !called {
		t.Fatal("log func not invoked")
	}
	if gotMethod != http.MethodPost || gotPath != "/query" || gotStatus != http.StatusTeapot {
		t.Fatalf("captured %s %s %d", gotMethod, gotPath, gotStatus)
	}
	if rr.Code != http.StatusTeapot || rr.Body.String() != "hi" {
		t.Fatalf("response not passed through: %d %q", rr.Code, rr.Body.String())
	}
}

func TestRequestLoggerWith_DefaultStatus200(t *testing.T) {
	var gotStatus int
	mw := RequestLoggerWith(func(_, _ string, status int, _ time.Duration) { gotStatus = status })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok")) // no explicit WriteHeader
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if gotStatus != http.StatusOK {
		t.Fatalf("default status = %d, want 200", gotStatus)
	}
}
