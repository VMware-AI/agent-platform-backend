package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
)

// The content-library inventory resolvers enumerate vCenter using privileged pool
// credentials, so they must reject non-admins via the inline gate (belt-and-
// suspenders with the @hasRole directive), exactly like VsphereNetworks/VMTemplates.
func TestContentLibraryResolvers_RequireAdmin(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	for _, role := range []auth.Role{auth.RoleUser, auth.RoleReadOnly} {
		ctx := auth.WithCurrentUser(context.Background(), &auth.CurrentUser{ID: uuid.NewString(), Role: role})
		if _, err := qr.ContentLibraries(ctx, uuid.NewString()); err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Errorf("ContentLibraries[%s] must be forbidden, got %v", role, err)
		}
		if _, err := qr.ContentLibraryItems(ctx, uuid.NewString(), "lib"); err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Errorf("ContentLibraryItems[%s] must be forbidden, got %v", role, err)
		}
	}

	// No authenticated user at all is also rejected.
	if _, err := qr.ContentLibraries(context.Background(), uuid.NewString()); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("ContentLibraries with no user must be forbidden, got %v", err)
	}
}

// Admin reaches input validation: a malformed id and a missing pool surface
// client-readable errors (not a vCenter dial).
func TestContentLibraryResolvers_AdminInputValidation(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	admin := auth.WithCurrentUser(context.Background(),
		&auth.CurrentUser{ID: "00000000-0000-0000-0000-000000000001", Role: auth.RoleAdmin})

	if _, err := qr.ContentLibraries(admin, "not-a-uuid"); err == nil || !strings.Contains(err.Error(), "invalid resourcePoolId") {
		t.Errorf("bad id: want invalid resourcePoolId, got %v", err)
	}
	if _, err := qr.ContentLibraryItems(admin, "not-a-uuid", "lib"); err == nil || !strings.Contains(err.Error(), "invalid resourcePoolId") {
		t.Errorf("bad id (items): want invalid resourcePoolId, got %v", err)
	}
	if _, err := qr.ContentLibraries(admin, uuid.NewString()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing pool: want not found, got %v", err)
	}
}
