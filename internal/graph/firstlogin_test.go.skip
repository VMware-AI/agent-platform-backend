package graph

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
)

func fieldCtx(ctx context.Context, object, field string) context.Context {
	return graphql.WithFieldContext(ctx, &graphql.FieldContext{
		Object: object,
		Field:  graphql.CollectedField{Field: &ast.Field{Name: field}},
	})
}

func TestRequirePasswordChange(t *testing.T) {
	mw := RequirePasswordChange()
	next := func(context.Context) (any, error) { return "ok", nil }

	mustChange := &auth.CurrentUser{ID: "u1", Role: "user", MustChangePassword: true}
	settled := &auth.CurrentUser{ID: "u2", Role: "user", MustChangePassword: false}

	run := func(u *auth.CurrentUser, object, field string) (any, error) {
		ctx := context.Background()
		if u != nil {
			ctx = auth.WithCurrentUser(ctx, u)
		}
		return mw(fieldCtx(ctx, object, field), next)
	}

	t.Run("blocks non-allowlisted mutation when must change", func(t *testing.T) {
		if _, err := run(mustChange, "Mutation", "createUser"); err == nil {
			t.Fatal("expected createUser to be blocked for must-change user")
		}
	})

	t.Run("allows changePassword when must change", func(t *testing.T) {
		if res, err := run(mustChange, "Mutation", "changePassword"); err != nil || res != "ok" {
			t.Fatalf("changePassword should pass: res=%v err=%v", res, err)
		}
	})

	t.Run("allows logout when must change", func(t *testing.T) {
		if res, err := run(mustChange, "Mutation", "logout"); err != nil || res != "ok" {
			t.Fatalf("logout should pass: res=%v err=%v", res, err)
		}
	})

	t.Run("allows queries when must change", func(t *testing.T) {
		if res, err := run(mustChange, "Query", "me"); err != nil || res != "ok" {
			t.Fatalf("query me should pass: res=%v err=%v", res, err)
		}
	})

	t.Run("does not block nested mutation-return fields", func(t *testing.T) {
		// Sub-fields of a returned object have Object != "Mutation"; only the
		// top-level mutation field is gated, so nested resolvers must pass.
		if res, err := run(mustChange, "User", "email"); err != nil || res != "ok" {
			t.Fatalf("nested field should pass: res=%v err=%v", res, err)
		}
	})

	t.Run("allows all mutations once password settled", func(t *testing.T) {
		if res, err := run(settled, "Mutation", "createUser"); err != nil || res != "ok" {
			t.Fatalf("settled user mutation should pass: res=%v err=%v", res, err)
		}
	})

	t.Run("ignores unauthenticated callers", func(t *testing.T) {
		// login is unauthenticated; the directive layer guards auth, not this mw.
		if res, err := run(nil, "Mutation", "login"); err != nil || res != "ok" {
			t.Fatalf("unauthenticated should pass through: res=%v err=%v", res, err)
		}
	})
}
