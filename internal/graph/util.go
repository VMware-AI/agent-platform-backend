package graph

import (
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

// orEmptyStrings returns the slice unchanged, or an empty (non-nil) slice when nil,
// so a stored string list is never NULL.
func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
