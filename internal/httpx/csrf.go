package httpx

import (
	"net/http"
	"net/url"
	"strings"
)

// CSRF rejects state-changing (non-safe-method) requests whose Origin/Referer is
// neither same-origin nor in the allowlist. GraphQL here is cookie-authenticated
// and all-POST, so a strict Origin check is the primary CSRF defense — SameSite
// alone does not stop simple cross-site POSTs.
func CSRF(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = normalizeOrigin(o); o != "" {
			allowed[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r) // safe methods: no state change
				return
			}
			origin := requestOrigin(r)
			if origin == "" {
				http.Error(w, "missing Origin/Referer on state-changing request", http.StatusForbidden)
				return
			}
			if allowed[origin] || sameOrigin(origin, r) {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		})
	}
}

// normalizeOrigin canonicalizes an origin / allowlist entry for comparison: trims
// surrounding whitespace and a trailing slash. Shared by CSRF and CORS so their
// allowlists agree (a "works server-side but the browser blocks it" mismatch
// otherwise occurs when one trims and the other doesn't).
func normalizeOrigin(o string) string {
	return strings.TrimRight(strings.TrimSpace(o), "/")
}

// requestOrigin returns the scheme://host of the request's Origin header, or
// derives it from Referer. Empty if neither is present/parseable.
func requestOrigin(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" {
		return normalizeOrigin(o)
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return ""
}

// sameOrigin reports whether origin's host matches the request Host (the call
// targets the same host the page was served from).
func sameOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}
