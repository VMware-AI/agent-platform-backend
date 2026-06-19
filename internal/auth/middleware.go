package auth

import (
	"net/http"

	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
)

// SessionCookie is the name of the session cookie.
const SessionCookie = "ap_session"

// SessionMiddleware loads the session (if any) into the request context as a
// CurrentUser, and stashes the writer/request so resolvers can manage cookies.
func SessionMiddleware(store session.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := httpx.WithHTTP(r.Context(), w, r)
			if c, err := r.Cookie(SessionCookie); err == nil {
				if d, err := store.Get(c.Value); err == nil {
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
