package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCSRF(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CSRF([]string{"https://app.example.com"})(ok)

	cases := []struct {
		name    string
		method  string
		origin  string
		referer string
		host    string
		want    int
	}{
		{"GET exempt", http.MethodGet, "", "", "api.example.com", http.StatusOK},
		{"POST allowed origin", http.MethodPost, "https://app.example.com", "", "api.example.com", http.StatusOK},
		{"POST allowed trailing slash", http.MethodPost, "https://app.example.com/", "", "api.example.com", http.StatusOK},
		{"POST same-origin", http.MethodPost, "https://api.example.com", "", "api.example.com", http.StatusOK},
		{"POST via referer", http.MethodPost, "", "https://app.example.com/login", "api.example.com", http.StatusOK},
		{"POST no origin/referer", http.MethodPost, "", "", "api.example.com", http.StatusForbidden},
		{"POST cross-origin", http.MethodPost, "https://evil.example.com", "", "api.example.com", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/query", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

// A non-allowlisted Origin that is also unparseable as a URL (here: an embedded
// control character) must make sameOrigin's url.Parse fail closed → the request
// is rejected, never accidentally treated as same-origin. Covers the error
// branch of sameOrigin.
func TestCSRF_UnparseableOriginRejected(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CSRF([]string{"https://app.example.com"})(ok)

	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.Host = "api.example.com"
	req.Header.Set("Origin", "http://api.example.com\x7f") // DEL byte → url.Parse error
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for unparseable origin", rec.Code)
	}
}
