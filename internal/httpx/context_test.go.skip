package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestEnvironmentContext_RoundTrip(t *testing.T) {
	id := uuid.New()
	ctx := WithEnvironment(context.Background(), id)
	got, ok := EnvironmentFromContext(ctx)
	if !ok {
		t.Fatal("EnvironmentFromContext ok=false after WithEnvironment")
	}
	if got != id {
		t.Fatalf("EnvironmentFromContext = %s, want %s", got, id)
	}
}

func TestEnvironmentFromContext_AbsentIsNotOK(t *testing.T) {
	if _, ok := EnvironmentFromContext(context.Background()); ok {
		t.Fatal("empty context must report ok=false")
	}
}

func TestEnvironment_Middleware(t *testing.T) {
	valid := uuid.New()
	cases := []struct {
		name     string
		header   string // "" means header not set at all
		wantOK   bool
		wantUUID uuid.UUID
	}{
		{"valid uuid parsed into context", valid.String(), true, valid},
		{"absent header → no env filter", "", false, uuid.Nil},
		{"malformed value → soft no-op", "not-a-uuid", false, uuid.Nil},
		{"empty header value → no-op", " ", false, uuid.Nil}, // non-empty but unparseable
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotOK bool
			var gotUUID uuid.UUID
			h := Environment(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				gotUUID, gotOK = EnvironmentFromContext(r.Context())
			}))

			req := httptest.NewRequest(http.MethodPost, "/query", nil)
			if tc.header != "" {
				req.Header.Set(EnvironmentHeader, tc.header)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)

			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if gotUUID != tc.wantUUID {
				t.Fatalf("uuid = %s, want %s", gotUUID, tc.wantUUID)
			}
		})
	}
}

func TestWithHTTP_WriterAndRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	ctx := WithHTTP(context.Background(), rec, req)

	if Writer(ctx) != rec {
		t.Fatal("Writer did not return the stashed ResponseWriter")
	}
	if Request(ctx) != req {
		t.Fatal("Request did not return the stashed *http.Request")
	}
}

func TestWriterAndRequest_AbsentReturnNil(t *testing.T) {
	if Writer(context.Background()) != nil {
		t.Fatal("Writer on empty context must be nil")
	}
	if Request(context.Background()) != nil {
		t.Fatal("Request on empty context must be nil")
	}
}
