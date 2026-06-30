package gateway

import (
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors returned (wrapped) by HTTPClient. Callers use errors.Is to
// branch on cause without parsing strings:
//
//	if errors.Is(err, gateway.ErrUnauthorized) { ... }
//
// One sentinel per class of failure: 401/403/404/5xx are terminal at the
// HTTP level; ErrMalformedResponse and ErrTransport cover non-HTTP paths.
var (
	ErrUnauthorized      = errors.New("gateway: unauthorized")       // HTTP 401
	ErrForbidden         = errors.New("gateway: forbidden")          // HTTP 403
	ErrNotFound          = errors.New("gateway: not found")          // HTTP 404
	ErrUnavailable       = errors.New("gateway: unavailable")        // HTTP 5xx
	ErrMalformedResponse = errors.New("gateway: malformed response") // decode error
	ErrTransport         = errors.New("gateway: transport error")    // network / DNS / EOF
)

// Error is the wire error returned by HTTPClient for every non-2xx HTTP
// response or transport failure. Wraps a sentinel via fmt.Errorf("%w: %w")
// so errors.Is matches both the concrete *Error and the sentinel class.
//
// Empty Status + non-nil Cause means a transport / decode failure (the
// request never produced a usable HTTP response).
type Error struct {
	Method string
	Path   string
	Status int
	Body   string // redacted server response body
	Cause  error  // transport / decode cause, when Status == 0
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("gateway %s %s: %v", e.Method, e.Path, e.Cause)
	}
	return fmt.Sprintf("gateway %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Body)
}

func (e *Error) Unwrap() error { return e.Cause }

// sentinelFromStatus maps an HTTP status to the matching sentinel. Returns nil
// for 4xx other than 401/403/404 — those don't have a dedicated class
// because they're caller-specific (e.g. 422 unprocessable entity).
func sentinelFromStatus(status int) error {
	switch {
	case status == http.StatusUnauthorized:
		return ErrUnauthorized
	case status == http.StatusForbidden:
		return ErrForbidden
	case status == http.StatusNotFound:
		return ErrNotFound
	case status >= 500:
		return ErrUnavailable
	}
	return nil
}
