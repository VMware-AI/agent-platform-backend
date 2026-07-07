package graph

import (
	"context"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// intOrZero dereferences an optional int, defaulting to 0.
func intOrZero(p *int) int {
	if p != nil {
		return *p
	}
	return 0
}

// derefString dereferences an optional string, defaulting to "".
func derefString(p *string) string {
	if p != nil {
		return *p
	}
	return ""
}

// parseOptionalUUID parses an optional id input into a *uuid.UUID. nil input →
// nil result (no error); a malformed id → a user-facing error naming the field.
func parseOptionalUUID(s *string, field string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, gqlerror.Errorf("invalid %s", field)
	}
	return &id, nil
}

// parseRequiredUUID parses a non-empty UUID string. Empty input → a clear
// "field is required" error; a malformed id → a user-facing error naming
// the field. Used for inputs that the schema marks as non-null and the
// resolver must enforce as live (e.g. backendGatewayId on model route
// create/upsert).
func parseRequiredUUID(s, field string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, gqlerror.Errorf("%s is required", field)
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, gqlerror.Errorf("invalid %s", field)
	}
	return id, nil
}

// resolveLiveGateway fetches a GatewayConnection by id, returning a GraphQL
// error if it doesn't exist. Model-route resolvers call this to validate
// backendGatewayId before persisting — a route bound to a deleted gateway
// would orphan its router-settings push.
func resolveLiveGateway(ctx context.Context, entc *ent.Client, id uuid.UUID) (*ent.GatewayConnection, error) {
	g, err := entc.GatewayConnection.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, gqlerror.Errorf("backendGatewayId %s not found", id)
		}
		return nil, err
	}
	return g, nil
}

// orEmptyStrings returns the slice unchanged, or an empty (non-nil) slice when nil,
// so a stored string list is never NULL.
func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// mapSlice projects a slice of ent rows to a value slice of GraphQL models,
// preserving order. f must return a non-nil pointer for every element.
func mapSlice[E any, M any](xs []*E, f func(*E) *M) []M {
	out := make([]M, 0, len(xs))
	for _, x := range xs {
		out = append(out, *f(x))
	}
	return out
}
