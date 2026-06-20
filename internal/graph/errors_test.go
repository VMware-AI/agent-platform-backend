package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

// A plain (non-gqlerror) error returned by a resolver must NEVER leak its detail
// to the client: the presenter masks it behind a generic message + correlation id.
func TestErrorPresenter_MasksInternalError(t *testing.T) {
	secret := "connect vcenter: dial 10.0.0.5:443: secret=hunter2"
	out := ErrorPresenter(context.Background(), fmt.Errorf("%s", secret))

	if strings.Contains(out.Message, "hunter2") || strings.Contains(out.Message, "vcenter") {
		t.Fatalf("internal detail leaked to client: %q", out.Message)
	}
	if out.Message != msgInternal {
		t.Fatalf("want generic message %q, got %q", msgInternal, out.Message)
	}
	if out.Extensions["code"] != codeInternal {
		t.Fatalf("want code %q, got %v", codeInternal, out.Extensions["code"])
	}
	if id, _ := out.Extensions["errorId"].(string); id == "" {
		t.Fatal("want a non-empty errorId for server-side correlation")
	}
}

// A wrapped internal error (errors.Wrap / %w around a plain error) is still masked.
func TestErrorPresenter_MasksWrappedInternalError(t *testing.T) {
	out := ErrorPresenter(context.Background(), fmt.Errorf("provision: %w", errors.New("boom at /etc/secret")))
	if strings.Contains(out.Message, "secret") || out.Message != msgInternal {
		t.Fatalf("wrapped internal detail leaked: %q", out.Message)
	}
	if out.Extensions["code"] != codeInternal {
		t.Fatalf("want INTERNAL code, got %v", out.Extensions["code"])
	}
}

// Intentional client-facing gqlerrors pass through with their message preserved
// and a classified code so clients can branch on it.
func TestErrorPresenter_PassesThroughClientErrors(t *testing.T) {
	cases := []struct {
		msg  string
		code string
	}{
		{"unauthenticated", codeUnauthenticated},
		{"forbidden: not your agent", codeForbidden},
		{"invalid agentId", codeBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.msg, func(t *testing.T) {
			out := ErrorPresenter(context.Background(), gqlerror.Errorf("%s", tc.msg))
			if out.Message != tc.msg {
				t.Fatalf("client message mangled: want %q, got %q", tc.msg, out.Message)
			}
			if out.Extensions["code"] != tc.code {
				t.Fatalf("want code %q, got %v", tc.code, out.Extensions["code"])
			}
		})
	}
}

// RecoverFunc converts a panic into a masked error — no stack/detail to the client.
func TestRecoverFunc_MasksPanic(t *testing.T) {
	err := RecoverFunc(context.Background(), "nil pointer at resolver.go:42")
	var gErr *gqlerror.Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *gqlerror.Error, got %T", err)
	}
	if strings.Contains(gErr.Message, "resolver.go") || gErr.Message != msgInternal {
		t.Fatalf("panic detail leaked: %q", gErr.Message)
	}
	if gErr.Extensions["code"] != codeInternal {
		t.Fatalf("want INTERNAL code, got %v", gErr.Extensions["code"])
	}
	if id, _ := gErr.Extensions["errorId"].(string); id == "" {
		t.Fatal("want a non-empty errorId")
	}
}
