package graph_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

// loginCookieHandler builds the real HTTP GraphQL stack and seeds one user whose
// password is "LoginPass1234", returning the handler + the user's email.
func loginCookieHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	c, err := store.Open(context.Background(), "", true)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	hash, err := auth.HashPassword("LoginPass1234")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := c.User.Create().
		SetUsername("cookieuser").SetEmail("cookie@x.io").
		SetPasswordHash(hash).SetRole(user.RoleAdmin).
		Save(context.Background()); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	sess := session.NewMemoryStore()
	r := &graph.Resolver{Ent: c, Sessions: sess, SessionTTL: time.Hour}
	es := graph.NewExecutableSchema(graph.Config{
		Resolvers: r,
		Directives: graph.DirectiveRoot{
			HasRole:       graph.HasRole,
			HasPermission: r.HasPermission,
		},
	})
	srv := handler.New(es)
	srv.AddTransport(transport.POST{})
	return auth.SessionMiddleware(sess)(srv), "cookie@x.io"
}

// sessionCookieFromLogin POSTs the login mutation with the given `remember`
// variable (nil ⇒ omitted) and returns the ap_session cookie that was set.
func sessionCookieFromLogin(t *testing.T, h http.Handler, email string, remember *bool) *http.Cookie {
	t.Helper()
	input := map[string]any{"email": email, "password": "LoginPass1234"}
	if remember != nil {
		input["remember"] = *remember
	}
	body, _ := json.Marshal(map[string]any{
		"query":     `mutation($i: LoginInput!){ login(input: $i){ user { id } } }`,
		"variables": map[string]any{"i": input},
	})
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login HTTP %d: %s", rec.Code, rec.Body.String())
	}
	// Surface GraphQL-level errors (e.g. a schema/resolver mismatch).
	var gqlResp struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &gqlResp)
	if len(gqlResp.Errors) > 0 {
		t.Fatalf("login returned errors: %s", gqlResp.Errors[0].Message)
	}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == auth.SessionCookie {
			return ck
		}
	}
	t.Fatalf("no %s cookie set by login", auth.SessionCookie)
	return nil
}

func TestLoginCookie_RememberControlsMaxAge(t *testing.T) {
	h, email := loginCookieHandler(t)

	t.Run("remember=true → persistent cookie (MaxAge>0)", func(t *testing.T) {
		v := true
		ck := sessionCookieFromLogin(t, h, email, &v)
		if ck.MaxAge <= 0 {
			t.Fatalf("remember=true: want MaxAge>0 (persistent), got %d", ck.MaxAge)
		}
		if !ck.HttpOnly {
			t.Fatal("session cookie must be HttpOnly")
		}
	})

	t.Run("remember omitted → persistent cookie (MaxAge>0, back-compat)", func(t *testing.T) {
		ck := sessionCookieFromLogin(t, h, email, nil)
		if ck.MaxAge <= 0 {
			t.Fatalf("remember omitted: want MaxAge>0 (default persistent), got %d", ck.MaxAge)
		}
	})

	t.Run("remember=false → session cookie (MaxAge==0)", func(t *testing.T) {
		v := false
		ck := sessionCookieFromLogin(t, h, email, &v)
		if ck.MaxAge != 0 {
			t.Fatalf("remember=false: want MaxAge==0 (session cookie), got %d", ck.MaxAge)
		}
		if !ck.HttpOnly {
			t.Fatal("session cookie must be HttpOnly")
		}
	})
}
