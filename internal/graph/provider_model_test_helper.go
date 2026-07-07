package graph

import (
	"context"
	"errors"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
)

// privateSpecTestMessage collapses a gateway error into a console-facing
// message for the testPrivateModelSpecConnection dry-run. Mirrors the masking
// in probeGatewayConnectionStatus / testResultMessage: the raw transport
// error is logged server-side; the client gets a short class-only label so
// the UI never leaks IP/host/error-stack details.
func privateSpecTestMessage(err error) string {
	switch {
	case err == nil:
		return "connection ok"
	case errors.Is(err, gateway.ErrUnauthorized):
		return "unauthorized (401) — check apiKey"
	case errors.Is(err, gateway.ErrForbidden):
		return "forbidden (403)"
	case errors.Is(err, gateway.ErrNotFound):
		return "not found (404) — check apiBase path"
	case errors.Is(err, gateway.ErrUnavailable):
		return "upstream unavailable (5xx)"
	case errors.Is(err, gateway.ErrTransport):
		return "transport error — check apiBase host/port"
	case errors.Is(err, gateway.ErrMalformedResponse):
		return "malformed response from upstream"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout (10s) — upstream too slow"
	default:
		return "connection failed"
	}
}
