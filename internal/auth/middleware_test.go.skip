package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
)

func TestSessionTokenFromRequest(t *testing.T) {
	cases := []struct {
		name    string
		authHdr string
		cookie  *http.Cookie
		want    string
	}{
		{
			name:    "bearer header wins",
			authHdr: "Bearer tok-bearer",
			cookie:  &http.Cookie{Name: SessionCookie, Value: "tok-cookie"},
			want:    "tok-bearer",
		},
		{
			name:    "bearer trimmed of surrounding whitespace",
			authHdr: "Bearer   tok-spaced  ",
			want:    "tok-spaced",
		},
		{
			name:    "empty bearer falls back to cookie",
			authHdr: "Bearer ",
			cookie:  &http.Cookie{Name: SessionCookie, Value: "tok-cookie"},
			want:    "tok-cookie",
		},
		{
			name:    "whitespace-only bearer falls back to cookie",
			authHdr: "Bearer    ",
			cookie:  &http.Cookie{Name: SessionCookie, Value: "tok-cookie"},
			want:    "tok-cookie",
		},
		{
			name:    "non-bearer scheme ignored, falls back to cookie",
			authHdr: "Basic abc",
			cookie:  &http.Cookie{Name: SessionCookie, Value: "tok-cookie"},
			want:    "tok-cookie",
		},
		{
			name:   "cookie only",
			cookie: &http.Cookie{Name: SessionCookie, Value: "tok-cookie"},
			want:   "tok-cookie",
		},
		{
			name: "nothing supplied yields empty",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/query", nil)
			if tc.authHdr != "" {
				req.Header.Set("Authorization", tc.authHdr)
			}
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			if got := SessionTokenFromRequest(req); got != tc.want {
				t.Fatalf("SessionTokenFromRequest = %q, want %q", got, tc.want)
			}
		})
	}
}

// captureUser is a terminal handler that records the CurrentUser the middleware
// placed in the request context.
func captureUser(dst **CurrentUser) http.HandlerFunc {
	return func(_ http.ResponseWriter, r *http.Request) {
		*dst = FromContext(r.Context())
	}
}

func TestSessionMiddleware_LoadsUserFromBearer(t *testing.T) {
	store := session.NewMemoryStore()
	sid, err := store.Create(session.Data{
		UserID:     "u-1",
		Username:   "alice",
		Role:       string(RoleAdmin),
		TenantID:   "t-1",
		MustChange: true,
		ExpiresAt:  time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	var got *CurrentUser
	h := SessionMiddleware(store)(captureUser(&got))

	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got == nil {
		t.Fatal("expected CurrentUser in context, got nil")
	}
	if got.ID != "u-1" || got.Username != "alice" || got.Role != RoleAdmin ||
		got.TenantID != "t-1" || !got.MustChangePassword {
		t.Fatalf("CurrentUser = %+v, want fully-populated from session", got)
	}
}

func TestSessionMiddleware_LoadsUserFromCookie(t *testing.T) {
	store := session.NewMemoryStore()
	sid, err := store.Create(session.Data{
		UserID:    "u-2",
		Username:  "bob",
		Role:      string(RoleUser),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	var got *CurrentUser
	h := SessionMiddleware(store)(captureUser(&got))

	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: sid})
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got == nil || got.ID != "u-2" || got.Role != RoleUser {
		t.Fatalf("CurrentUser = %+v, want id=u-2 role=user", got)
	}
}

func TestSessionMiddleware_NoTokenLeavesContextAnonymous(t *testing.T) {
	store := session.NewMemoryStore()
	var got *CurrentUser
	h := SessionMiddleware(store)(captureUser(&got))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/query", nil))

	if got != nil {
		t.Fatalf("anonymous request must yield nil CurrentUser, got %+v", got)
	}
}

func TestSessionMiddleware_UnknownTokenLeavesContextAnonymous(t *testing.T) {
	store := session.NewMemoryStore()
	var got *CurrentUser
	h := SessionMiddleware(store)(captureUser(&got))

	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.Header.Set("Authorization", "Bearer does-not-exist")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != nil {
		t.Fatalf("unknown session must yield nil CurrentUser, got %+v", got)
	}
}

func TestSessionMiddleware_ExpiredTokenLeavesContextAnonymous(t *testing.T) {
	store := session.NewMemoryStore()
	sid, err := store.Create(session.Data{
		UserID:    "u-3",
		ExpiresAt: time.Now().Add(-time.Minute), // already expired
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	var got *CurrentUser
	h := SessionMiddleware(store)(captureUser(&got))

	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != nil {
		t.Fatalf("expired session must yield nil CurrentUser, got %+v", got)
	}
}

// SessionMiddleware must also stash the writer/request in context so resolvers
// (login/logout) can manage the session cookie.
func TestSessionMiddleware_StashesHTTPContext(t *testing.T) {
	store := session.NewMemoryStore()

	var sawWriter, sawRequest bool
	h := SessionMiddleware(store)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		sawWriter = httpx.Writer(r.Context()) != nil
		sawRequest = httpx.Request(r.Context()) != nil
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/query", nil))

	if !sawWriter || !sawRequest {
		t.Fatalf("middleware must stash writer+request: writer=%v request=%v", sawWriter, sawRequest)
	}
}
