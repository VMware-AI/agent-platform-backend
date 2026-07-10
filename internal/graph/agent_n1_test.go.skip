package graph_test

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/VMware-AI/agent-platform-backend/ent"
	entagent "github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
)

// countingDriver wraps an ent dialect.Driver and tallies SELECTs per table by
// matching the table name in the SQL text. It lets the N+1 test assert that the
// number of User / VirtualKey round-trips is O(1) in the agent count.
type countingDriver struct {
	dialect.Driver
	users       int64
	virtualKeys int64
}

func (d *countingDriver) Query(ctx context.Context, query string, args, v any) error {
	// Match the table regardless of the dialect's identifier quoting (sqlite uses
	// backticks, postgres double-quotes): strip quote chars, collapse whitespace,
	// lowercase, then look for "from <table>".
	norm := strings.NewReplacer("`", " ", `"`, " ").Replace(query)
	norm = strings.ToLower(strings.Join(strings.Fields(norm), " "))
	if strings.Contains(norm, "from users") {
		atomic.AddInt64(&d.users, 1)
	}
	if strings.Contains(norm, "from virtual_keys") {
		atomic.AddInt64(&d.virtualKeys, 1)
	}
	return d.Driver.Query(ctx, query, args, v)
}

// reset zeroes the counters (call after seeding, before the measured query).
func (d *countingDriver) reset() {
	atomic.StoreInt64(&d.users, 0)
	atomic.StoreInt64(&d.virtualKeys, 0)
}

// n1Env is a full HTTP GraphQL stack whose ent driver counts queries.
// seedVK mirrors package graph's seedVirtualKey (invisible from this external
// test package): a VirtualKey builder pre-filled with the schema's required set.
func seedVK(ec *ent.Client, key string) *ent.VirtualKeyCreate {
	return ec.VirtualKey.Create().SetLitellmKey(key).SetMaskedKey("sk-***").
		SetName(key).SetModelGatewayID(uuid.New())
}

type n1Env struct {
	gql     *client.Client
	ent     *ent.Client
	sess    *session.MemoryStore
	driver  *countingDriver
	cleanup func()
}

func setupN1(t *testing.T) *n1Env {
	t.Helper()
	db, err := sql.Open("sqlite", "file:n1test?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	cd := &countingDriver{Driver: entsql.OpenDB(dialect.SQLite, db)}
	c := ent.NewClient(ent.Driver(cd))
	if err := c.Schema.Create(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
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
	r.InstallLoaders(srv)
	h := auth.SessionMiddleware(sess)(srv)
	return &n1Env{gql: client.New(h), ent: c, sess: sess, driver: cd, cleanup: func() { _ = c.Close() }}
}

func (e *n1Env) adminCookie(t *testing.T) *http.Cookie {
	t.Helper()
	hash, _ := auth.HashPassword("SeedPass1234")
	u, err := e.ent.User.Create().
		SetUsername("admin").SetEmail("admin@x.io").
		SetPasswordHash(hash).SetRole("admin").Save(context.Background())
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	sid, _ := e.sess.Create(session.Data{
		UserID: u.ID.String(), Username: "admin", Role: "admin",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	return &http.Cookie{Name: auth.SessionCookie, Value: sid}
}

// agentsWithOwnerKey selects the exact fields the console Agents list uses that
// drive the N+1: owner{...} + apiKey{...} for every row.
const agentsWithOwnerKey = `{
  agents(pagination:{page:1,pageSize:100}) {
    totalCount
    nodes {
      id
      name
      owner { id displayName email }
      apiKey { id name }
      credentials { username }
    }
  }
}`

type agentsResp struct {
	Agents struct {
		TotalCount int
		Nodes      []struct {
			ID    string
			Name  string
			Owner *struct {
				ID          string
				DisplayName string
				Email       string
			}
			APIKey *struct {
				ID   string
				Name string
			} `json:"apiKey"`
			Credentials *struct{ Username string }
		}
	}
}

// TestAgents_NoN1_OwnerAndKeyBatched is the core regression test: a list of N
// agents selecting owner + apiKey + credentials must batch the User and
// VirtualKey lookups into O(1) queries (one IN(...) each), NOT O(N).
//
// Owners and keys are deliberately MIXED — some shared, some distinct — so the
// test exercises de-duplication (shared) and breadth (distinct) at once.
func TestAgents_NoN1_OwnerAndKeyBatched(t *testing.T) {
	e := setupN1(t)
	defer e.cleanup()
	bg := context.Background()
	cookie := e.adminCookie(t)

	const n = 20
	// 4 distinct owners and 3 distinct keys spread across 20 agents: forces a
	// real batch (multiple ids) while also covering shared ids.
	owners := make([]uuid.UUID, 4)
	for i := range owners {
		u := e.ent.User.Create().
			SetUsername("owner" + string(rune('a'+i))).
			SetEmail("owner" + string(rune('a'+i)) + "@x.io").
			SetPasswordHash("x").SetRole("user").SaveX(bg)
		owners[i] = u.ID
	}
	keys := make([]uuid.UUID, 3)
	for i := range keys {
		vk := seedVK(e.ent, "sk-"+string(rune('a'+i))).
			SetModels([]string{"smart"}).SaveX(bg)
		keys[i] = vk.ID
	}
	for i := 0; i < n; i++ {
		c := e.ent.Agent.Create().
			SetName("agent-" + string(rune('a'+i))).
			SetAgentType("goose").
			SetStatus(entagent.StatusRunning).
			SetOwnerUserID(owners[i%len(owners)])
		// leave a few agents key-less to cover the nil branch
		if i%4 != 0 {
			c.SetVirtualKeyID(keys[i%len(keys)])
		}
		c.SaveX(bg)
	}

	e.driver.reset()
	var resp agentsResp
	e.gql.MustPost(agentsWithOwnerKey, &resp, client.AddCookie(cookie))

	if resp.Agents.TotalCount != n || len(resp.Agents.Nodes) != n {
		t.Fatalf("expected %d agents, got total=%d nodes=%d", n, resp.Agents.TotalCount, len(resp.Agents.Nodes))
	}

	// --- the N+1 assertion: O(1), not O(N) ---
	users := atomic.LoadInt64(&e.driver.users)
	vks := atomic.LoadInt64(&e.driver.virtualKeys)
	// Batching coalesces all owner+credentials User lookups into a small constant
	// number of IN(...) queries (typically 1; allow a tiny margin for batch-window
	// races). Anything that scales with n means the N+1 regressed.
	if users == 0 || users > 3 {
		t.Fatalf("User queries = %d for %d agents: expected O(1) batched, not O(N)", users, n)
	}
	if vks == 0 || vks > 3 {
		t.Fatalf("VirtualKey queries = %d for %d agents: expected O(1) batched, not O(N)", vks, n)
	}
	t.Logf("for %d agents: User queries=%d, VirtualKey queries=%d (batched O(1))", n, users, vks)

	// --- correctness: owner + apiKey + credentials resolved right per row ---
	for _, node := range resp.Agents.Nodes {
		if node.Owner == nil || node.Owner.ID == "" {
			t.Fatalf("agent %s missing owner", node.Name)
		}
		if node.Credentials == nil || node.Credentials.Username == "" {
			t.Fatalf("agent %s missing credentials.username", node.Name)
		}
		// credentials.username must equal the owner's username (sourced from owner)
		// and password must never appear (the type only has username).
		if !strings.HasPrefix(node.Credentials.Username, "owner") {
			t.Fatalf("agent %s credentials.username = %q, want owner-sourced", node.Name, node.Credentials.Username)
		}
	}
}

// TestAgents_NilSafety_DeletedOwnerAndKey proves the field resolvers return nil
// (not an error) when the owner or virtual key referenced by the FK is gone.
func TestAgents_NilSafety_DeletedOwnerAndKey(t *testing.T) {
	e := setupN1(t)
	defer e.cleanup()
	bg := context.Background()
	cookie := e.adminCookie(t)

	owner := e.ent.User.Create().SetUsername("ghost").SetEmail("ghost@x.io").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	vk := seedVK(e.ent, "sk-ghost").
		SetModels([]string{"smart"}).SetName("ghost-key").SaveX(bg)

	// Agent points at an owner id and a key id that we then delete: the FK ids
	// stay on the agent row, but the related rows vanish.
	deadOwnerID := uuid.New()
	deadKeyID := uuid.New()
	e.ent.Agent.Create().SetName("orphan").SetAgentType("goose").
		SetStatus(entagent.StatusRunning).
		SetOwnerUserID(deadOwnerID).SetVirtualKeyID(deadKeyID).SaveX(bg)
	// A healthy agent so the page is non-trivial.
	e.ent.Agent.Create().SetName("healthy").SetAgentType("goose").
		SetStatus(entagent.StatusRunning).
		SetOwnerUserID(owner.ID).SetVirtualKeyID(vk.ID).SaveX(bg)
	_ = deadOwnerID
	_ = deadKeyID

	var resp agentsResp
	e.gql.MustPost(agentsWithOwnerKey, &resp, client.AddCookie(cookie))
	if len(resp.Agents.Nodes) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(resp.Agents.Nodes))
	}
	for _, node := range resp.Agents.Nodes {
		switch node.Name {
		case "orphan":
			if node.Owner != nil {
				t.Fatalf("orphan owner should be nil, got %+v", node.Owner)
			}
			if node.APIKey != nil {
				t.Fatalf("orphan apiKey should be nil, got %+v", node.APIKey)
			}
			if node.Credentials != nil {
				t.Fatalf("orphan credentials should be nil (owner gone), got %+v", node.Credentials)
			}
		case "healthy":
			if node.Owner == nil || node.Owner.DisplayName == "" {
				t.Fatalf("healthy owner should resolve, got %+v", node.Owner)
			}
			if node.APIKey == nil || node.APIKey.Name != "ghost-key" {
				t.Fatalf("healthy apiKey wrong: %+v", node.APIKey)
			}
		}
	}
}
