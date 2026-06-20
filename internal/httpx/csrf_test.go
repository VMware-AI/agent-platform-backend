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
