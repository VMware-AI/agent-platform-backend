package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// #36 coverage: the Me query had no real test (name-match false positives only).

func TestMe_Unauthenticated(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	if _, err := qr.Me(context.Background()); err == nil {
		t.Fatal("Me with no current user must be unauthenticated")
	}
}

func TestMe_ReturnsCurrentUser(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	u := mkUser(t, mr, context.Background(), "meuser", "me@x.io", model.RoleNameUser)
	got, err := qr.Me(userCtx(u.ID, "user"))
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if got.ID != u.ID || got.Username != "meuser" || got.Email != "me@x.io" {
		t.Fatalf("Me returned wrong user: %+v", got)
	}
}
