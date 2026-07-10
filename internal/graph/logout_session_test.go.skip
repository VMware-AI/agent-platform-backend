package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
)

// #35: Logout must revoke the session that authenticated the request via the SAME
// precedence as the middleware (Bearer header first, then cookie). The console
// uses Bearer transport, so cookie-only revocation left the session live until
// its TTL.
func TestLogout_RevokesBearerSession(t *testing.T) {
	r, cleanup := newTestResolver(t) // wires r.Sessions = MemoryStore
	defer cleanup()
	mr := &mutationResolver{r}

	sid, err := r.Sessions.Create(session.Data{UserID: "u1", Username: "bob", Role: "user"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Request carries the session as a Bearer token, no cookie (the console transport).
	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	ctx := httpx.WithHTTP(context.Background(), httptest.NewRecorder(), req)

	if _, err := mr.Logout(ctx); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := r.Sessions.Get(sid); err == nil {
		t.Fatal("Bearer-borne session must be revoked on logout, not left live until TTL")
	}
}

// The cookie path still works (same-origin browser use).
func TestLogout_RevokesCookieSession(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}

	sid, err := r.Sessions.Create(session.Data{UserID: "u2", Username: "carol", Role: "user"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.AddCookie(&http.Cookie{Name: "ap_session", Value: sid})
	ctx := httpx.WithHTTP(context.Background(), httptest.NewRecorder(), req)

	if _, err := mr.Logout(ctx); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := r.Sessions.Get(sid); err == nil {
		t.Fatal("cookie-borne session must still be revoked on logout")
	}
}
