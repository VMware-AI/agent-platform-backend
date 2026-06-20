package graph

import (
	"context"
	"errors"
	"log"
	"runtime/debug"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/VMware-AI/agent-platform-backend/ent"
)

// Generic message and error codes surfaced to GraphQL clients. Internal failures
// are collapsed to msgInternal so resolver/infra detail (hosts, secrets, stack
// traces) never reaches the wire; the real cause is logged server-side under an
// errorId the client can quote to support.
const (
	msgInternal = "internal server error"
	msgNotFound = "not found"

	codeInternal        = "INTERNAL"
	codeUnauthenticated = "UNAUTHENTICATED"
	codeForbidden       = "FORBIDDEN"
	codeBadRequest      = "BAD_REQUEST"
	codeNotFound        = "NOT_FOUND"
)

// ErrorPresenter is the global gqlgen error presenter. It distinguishes two kinds
// of errors:
//
//   - Intentional client errors — resolvers build these with gqlerror.Errorf
//     (e.g. "unauthenticated", "invalid id"). Their message is safe by
//     construction, so it passes through with a classified code attached.
//   - Everything else (plain Go errors from ent/vcenter/secrets, %w-wrapped
//     errors) — masked behind msgInternal with a correlation errorId; the full
//     error is logged so operators can trace it.
//
// The discriminator is the type, not the text: resolvers must return a raw error
// (fmt.Errorf("...: %w", err)) for internal failures and reserve gqlerror.Errorf
// for messages they intend the client to read. Never interpolate err.Error() into
// a gqlerror — that defeats the mask.
func ErrorPresenter(ctx context.Context, e error) *gqlerror.Error {
	var gErr *gqlerror.Error
	if errors.As(e, &gErr) {
		presented := graphql.DefaultErrorPresenter(ctx, e)
		if presented.Extensions == nil {
			presented.Extensions = map[string]interface{}{}
		}
		if presented.Extensions["code"] == nil {
			presented.Extensions["code"] = classifyCode(presented.Message)
		}
		return presented
	}
	// A bare ent "not found" (resolvers commonly `return nil, err` after a Get on a
	// client-supplied id) is safe to surface to an authorized caller — and far more
	// useful than masking it as INTERNAL. Use a generic message so the entity name
	// never leaks.
	if ent.IsNotFound(e) {
		return &gqlerror.Error{
			Message:    msgNotFound,
			Extensions: map[string]interface{}{"code": codeNotFound},
		}
	}
	return maskInternal(ctx, "graphql internal error", e.Error())
}

// notFoundErr is the uniform client error for "absent OR not yours", used by
// owner-scoped resolvers so the two cases are indistinguishable — a caller must
// not be able to probe which ids exist by comparing NOT_FOUND vs FORBIDDEN.
func notFoundErr(resource string) *gqlerror.Error {
	return &gqlerror.Error{
		Message:    resource + " not found",
		Extensions: map[string]interface{}{"code": codeNotFound},
	}
}

// RecoverFunc replaces gqlgen's default recover so a panicking resolver yields a
// masked error (with a logged stack trace) instead of crashing the request or
// leaking the panic value to the client.
func RecoverFunc(ctx context.Context, panicValue interface{}) error {
	return maskInternal(ctx, "graphql panic recovered", "%v\n%s", panicValue, debug.Stack())
}

// maskInternal logs the full detail under a fresh errorId and returns the generic
// client-facing error carrying only that id.
func maskInternal(ctx context.Context, label, detail string, args ...interface{}) *gqlerror.Error {
	errID := uuid.NewString()
	log.Printf("%s: id=%s path=%v detail="+detail, append([]interface{}{label, errID, graphql.GetPath(ctx)}, args...)...)
	return &gqlerror.Error{
		Message:    msgInternal,
		Extensions: map[string]interface{}{"code": codeInternal, "errorId": errID},
	}
}

// classifyCode derives a stable error code from a client-facing message prefix so
// clients can branch programmatically without parsing free text.
func classifyCode(msg string) string {
	switch {
	case strings.HasPrefix(msg, "unauthenticated"):
		return codeUnauthenticated
	case strings.HasPrefix(msg, "forbidden"):
		return codeForbidden
	case strings.HasPrefix(msg, "invalid"):
		return codeBadRequest
	default:
		return codeBadRequest
	}
}
