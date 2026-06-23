package httpx

import "net/http"

// CORS enables cross-origin requests from the configured frontend origins WITH
// credentials (cookies), which the dev split-origin setup needs (console on
// :5173 → backend on :8080). It echoes the request Origin only when it is in the
// allowlist — required because `Access-Control-Allow-Credentials: true` forbids
// the `*` wildcard. Same-origin and non-browser callers are unaffected (no Origin
// header → no CORS headers added). Preflight OPTIONS short-circuits with 204.
//
// This complements (does not replace) the CSRF Origin check: CORS tells the
// browser the cross-origin call is allowed; CSRF still validates state-changing
// requests server-side.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = normalizeOrigin(o); o != "" {
			allowed[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Echo the raw Origin back (browsers require an exact match), but compare
			// using the shared normalization so the allowlist agrees with CSRF.
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[normalizeOrigin(origin)] {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Vary", "Origin")
				if r.Method == http.MethodOptions {
					h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
					h.Set("Access-Control-Allow-Headers", "Content-Type, X-Environment")
					h.Set("Access-Control-Max-Age", "600")
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
