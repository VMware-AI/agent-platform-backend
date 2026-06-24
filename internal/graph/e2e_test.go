package graph_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

// e2eEnv builds the full HTTP GraphQL stack (executable schema + directives +
// session middleware) so directive enforcement runs on the real execution path.
type e2eEnv struct {
	gql     *client.Client
	ent     *ent.Client
	sess    *session.MemoryStore
	cleanup func()
}

func setupE2E(t *testing.T) *e2eEnv {
	t.Helper()
	c, err := store.Open(context.Background(), "", true)
	if err != nil {
		t.Fatalf("store: %v", err)
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
	h := auth.SessionMiddleware(sess)(srv)
	return &e2eEnv{gql: client.New(h), ent: c, sess: sess, cleanup: func() { _ = c.Close() }}
}

// seedUser creates a user and a live session, returning the session cookie.
func (e *e2eEnv) seedUser(t *testing.T, username string, role user.Role) *http.Cookie {
	t.Helper()
	hash, _ := auth.HashPassword("SeedPass1234")
	u, err := e.ent.User.Create().
		SetUsername(username).SetEmail(username + "@x.io").
		SetPasswordHash(hash).SetRole(role).Save(context.Background())
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	sid, _ := e.sess.Create(session.Data{
		UserID: u.ID.String(), Username: username, Role: string(role),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	return &http.Cookie{Name: auth.SessionCookie, Value: sid}
}

const createUserMut = `mutation { createUser(input:{username:"newbie", displayName:"newbie", email:"nb@x.io", roleId:"user", passwordMode:CUSTOM, customPassword:"NewbiePass12"}){ user{ id username role{ id } } } }`
const usersQuery = `{ users { totalCount nodes { username } } }`

func TestE2E_DirectiveBlocksUnauthenticated(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()
	var resp struct {
		Users struct{ TotalCount int }
	}
	// No cookie => @hasRole(admin) must reject.
	if err := e.gql.Post(usersQuery, &resp); err == nil {
		t.Fatal("unauthenticated users query should be rejected by @hasRole")
	}
}

func TestE2E_DirectiveBlocksNonAdmin(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()
	userCookie := e.seedUser(t, "plain", user.RoleUser)

	var resp struct {
		CreateUser struct {
			User struct{ ID string }
		}
	}
	err := e.gql.Post(createUserMut, &resp, client.AddCookie(userCookie))
	if err == nil {
		t.Fatal("non-admin createUser must be rejected by @hasRole(admin)")
	}
}

func TestE2E_AdminAllowed(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()
	adminCookie := e.seedUser(t, "boss", user.RoleAdmin)

	var resp struct {
		CreateUser struct {
			User struct {
				ID       string
				Username string
				Role     struct{ ID string }
			}
		}
	}
	e.gql.MustPost(createUserMut, &resp, client.AddCookie(adminCookie))
	if resp.CreateUser.User.Username != "newbie" || resp.CreateUser.User.Role.ID != "user" {
		t.Fatalf("unexpected createUser result: %+v", resp.CreateUser)
	}

	// admin can list users (self + created)
	var listResp struct {
		Users struct {
			TotalCount int
			Nodes      []struct{ Username string }
		}
	}
	e.gql.MustPost(usersQuery, &listResp, client.AddCookie(adminCookie))
	if listResp.Users.TotalCount < 2 {
		t.Fatalf("expected >=2 users, got %d", listResp.Users.TotalCount)
	}
}

func TestE2E_LoginFlowSetsCookie(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()
	// seed a user whose password we know
	hash, _ := auth.HashPassword("KnownPass1234")
	_, err := e.ent.User.Create().
		SetUsername("loginuser").SetEmail("l@x.io").
		SetPasswordHash(hash).SetRole(user.RoleUser).Save(context.Background())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	var resp struct {
		Login struct {
			Token              string
			User               struct{ Username string }
			MustChangePassword bool
		}
	}
	e.gql.MustPost(
		`mutation { login(input:{email:"loginuser", password:"KnownPass1234"}){ token user{ username } mustChangePassword } }`,
		&resp,
	)
	if resp.Login.User.Username != "loginuser" {
		t.Fatalf("login failed: %+v", resp.Login)
	}
	if resp.Login.Token == "" {
		t.Fatal("login must return a bearer token")
	}

	// The console login form collects an email — login must accept the email as the
	// identifier too (not just the username).
	var byEmail struct {
		Login struct {
			Token string
			User  struct{ Username string }
		}
	}
	e.gql.MustPost(
		`mutation { login(input:{email:"l@x.io", password:"KnownPass1234"}){ token user{ username } } }`,
		&byEmail,
	)
	if byEmail.Login.User.Username != "loginuser" {
		t.Fatalf("login by email failed: %+v", byEmail.Login)
	}
}
