// Package httpx stashes the HTTP writer/request in context so GraphQL resolvers
// (login/logout) can set or clear the session cookie.
package httpx

import (
	"context"
	"net/http"
)

type ctxKey int

const (
	writerKey ctxKey = iota
	requestKey
)

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
