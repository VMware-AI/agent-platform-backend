package auth

import (
	"net/http"
	"strings"

	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
)

// SessionCookie is the name of the session cookie.
const SessionCookie = "ap_session"

// SessionMiddleware loads the session (if any) into the request context as a
// CurrentUser, and stashes the writer/request so resolvers can manage cookies.
// The session id (token) is taken from the `Authorization: Bearer <token>` header
// (the console's transport) first, falling back to the cookie for same-origin
// browser use.
func SessionMiddleware(store session.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := httpx.WithHTTP(r.Context(), w, r)
			if sid := SessionTokenFromRequest(r); sid != "" {
				if d, err := store.Get(sid); err == nil {
					ctx = WithCurrentUser(ctx, &CurrentUser{
						ID:                 d.UserID,
						Username:           d.Username,
						Role:               Role(d.Role),
						TenantID:           d.TenantID,
						MustChangePassword: d.MustChange,
					})
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SessionTokenFromRequest extracts the session id from the Bearer Authorization
// header, or the session cookie as a fallback. Exported so Logout can revoke the
// exact session that authenticated the request, using the SAME precedence as the
// middleware (Bearer-borne sessions, not just cookie ones).
func SessionTokenFromRequest(r *http.Request) string {
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if t = strings.TrimSpace(t); t != "" {
			return t
		}
	}
	if c, err := r.Cookie(SessionCookie); err == nil {
		return c.Value
	}
	return ""
}
