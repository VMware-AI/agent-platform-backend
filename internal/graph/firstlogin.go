package graph

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
)

// passwordChangeAllowlist names the top-level mutations a user with
// must_change_password=true may still call before settling their password.
var passwordChangeAllowlist = map[string]bool{
	"changePassword": true,
	"logout":         true,
}

// RequirePasswordChange returns a gqlgen field middleware enforcing LLD-01 §6:
// while a user's must_change_password flag is set, every top-level mutation
// except the allowlist (changePassword, logout) is rejected. Queries are left
// untouched so the UI can still render the change-password screen.
//
// This is centralized (fail-closed) rather than a per-field @directive: any
// mutation added later is guarded automatically without remembering to annotate
// it. Only top-level mutation fields are gated (Object == "Mutation"); nested
// field resolvers on returned objects pass through.
func RequirePasswordChange() graphql.FieldMiddleware {
	return func(ctx context.Context, next graphql.Resolver) (any, error) {
		fc := graphql.GetFieldContext(ctx)
		if fc == nil || fc.Object != "Mutation" || passwordChangeAllowlist[fc.Field.Name] {
			return next(ctx)
		}
		if u := auth.FromContext(ctx); u != nil && u.MustChangePassword {
			return nil, gqlerror.Errorf("password change required: call changePassword before other operations")
		}
		return next(ctx)
	}
}
