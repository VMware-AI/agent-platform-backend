package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// #36/low: TestResourcePoolConnection must NOT echo the raw dial error (which can
// embed resolved IPs / internal topology) in its payload Message — that field
// bypasses the GraphQL ErrorPresenter. The client sees a coarse, safe message;
// the detail is logged server-side.
func TestResourcePoolConnection_FailureMessageIsGeneric(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}

	// Well-formed endpoint, but port 1 is refused → hits the dial-failure branch.
	res, err := mr.TestResourcePoolConnection(context.Background(), model.TestResourcePoolConnectionInput{
		Name: "p", Endpoint: "https://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("resolver should return a payload, not an error: %v", err)
	}
	if res.Ok {
		t.Fatal("expected unreachable")
	}
	if res.Message != "endpoint is not reachable" {
		t.Fatalf("failure message must be generic (no raw error leak), got %q", res.Message)
	}
}
