// Package httpx stashes the HTTP writer/request in context so GraphQL resolvers
// (login/logout) can set or clear the session cookie.
package httpx

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey int

const (
	writerKey ctxKey = iota
	requestKey
	environmentKey
)

// EnvironmentHeader is the request header carrying the active environment id
// (LLD-10 §2.3 env_scope soft boundary).
const EnvironmentHeader = "X-Environment"

// WithEnvironment returns a context carrying the requested environment id.
func WithEnvironment(ctx context.Context, env uuid.UUID) context.Context {
	return context.WithValue(ctx, environmentKey, env)
}

// EnvironmentFromContext returns the requested environment id, or ok=false if no
// (valid) X-Environment was supplied on the request.
func EnvironmentFromContext(ctx context.Context) (uuid.UUID, bool) {
	e, ok := ctx.Value(environmentKey).(uuid.UUID)
	return e, ok
}

// Environment parses the X-Environment header into the context (no-op if absent
// or malformed — env_scope is a soft boundary, so a bad value simply means "no
// env filter", never an error). Mounted ahead of the GraphQL handler.
func Environment(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get(EnvironmentHeader); v != "" {
			if id, err := uuid.Parse(v); err == nil {
				r = r.WithContext(WithEnvironment(r.Context(), id))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// WithHTTP returns a context carrying the response writer and request.
func WithHTTP(ctx context.Context, w http.ResponseWriter, r *http.Request) context.Context {
	ctx = context.WithValue(ctx, writerKey, w)
	ctx = context.WithValue(ctx, requestKey, r)
	return ctx
}

// Writer returns the response writer carried in ctx, or nil.
func Writer(ctx context.Context) http.ResponseWriter {
	w, _ := ctx.Value(writerKey).(http.ResponseWriter)
	return w
}

// Request returns the request carried in ctx, or nil.
func Request(ctx context.Context) *http.Request {
	r, _ := ctx.Value(requestKey).(*http.Request)
	return r
}
