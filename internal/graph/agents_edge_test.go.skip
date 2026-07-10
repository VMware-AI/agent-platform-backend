package graph

// agents_edge_test.go — edge-case coverage for the agents() list resolver and the
// per-row owner/apiKey/credentials field resolvers. The happy path (filters/sort/
// pagination) lives in agent_connection_test.go and agent_n1_test.go; this file
// targets the boundaries those don't: empty state, out-of-range / degenerate
// pagination, fail-closed cross-entity keyword filters, combined filters, role
// visibility scoping (incl. unauthenticated + malformed-id callers), and field
// resolver nil-safety for deleted owners/keys and bad ids.
//
// All helpers/vars carry an "Edge" suffix to avoid colliding with the ~30 sibling
// test files that share this package.

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// seedAgentEdge inserts one agent row directly (bypassing the CreateAgent
// mutation) so a test can pin owner/key/type/status/tenant without auth ceremony.
func seedAgentEdge(t *testing.T, r *Resolver, name, kind string, status agent.Status, owner uuid.UUID, key *uuid.UUID, tenant *uuid.UUID) {
	t.Helper()
	c := r.Ent.Agent.Create().SetName(name).SetAgentType(kind).
		SetStatus(status).SetOwnerUserID(owner)
	if key != nil {
		c.SetVirtualKeyID(*key)
	}
	if tenant != nil {
		c.SetTenantID(*tenant)
	}
	if _, err := c.Save(context.Background()); err != nil {
		t.Fatalf("seed agent %s: %v", name, err)
	}
}

// mustAgentsEdge runs the resolver and fails the test on a transport error.
func mustAgentsEdge(t *testing.T, qr *queryResolver, ctx context.Context, f *model.AgentFilter, p *model.Pagination, s *model.AgentSort) *model.AgentConnection {
	t.Helper()
	c, err := qr.Agents(ctx, f, p, s)
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	return c
}

// --- empty state -----------------------------------------------------------

// An admin querying with zero agents must get a well-formed empty connection:
// empty (non-nil) node slice, zero total, and PageInfo reflecting page 1 with
// zero total pages — not a nil PageInfo and not a panic.
func TestAgentsEdge_EmptyState(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	conn := mustAgentsEdge(t, qr, adminCtx(), nil, nil, nil)
	if conn == nil {
		t.Fatal("connection must not be nil")
	}
	if conn.TotalCount != 0 {
		t.Fatalf("empty state TotalCount = %d, want 0", conn.TotalCount)
	}
	if conn.Nodes == nil {
		t.Fatal("Nodes must be a non-nil empty slice (clients iterate it)")
	}
	if len(conn.Nodes) != 0 {
		t.Fatalf("empty state nodes = %d, want 0", len(conn.Nodes))
	}
	if conn.PageInfo == nil {
		t.Fatal("PageInfo must not be nil even on empty result")
	}
	// default page=1, pageSize=10; (0+10-1)/10 = 0 total pages for zero rows.
	if conn.PageInfo.Page != 1 || conn.PageInfo.PageSize != 10 || conn.PageInfo.TotalPages != 0 {
		t.Fatalf("empty PageInfo = %+v, want {page:1 pageSize:10 totalPages:0}", conn.PageInfo)
	}
}

// A regular user with no agents of their own sees an empty connection even when
// OTHER users own agents (owner-track scoping holds at the empty boundary).
func TestAgentsEdge_EmptyState_UserScopedAwayFromOthers(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	bg := context.Background()

	other := r.Ent.User.Create().SetUsername("other").SetEmail("other@x.io").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	seedAgentEdge(t, r, "theirs", "goose", agent.StatusRunning, other.ID, nil, nil)

	lonely := userCtx("aaaaaaaa-0000-0000-0000-000000000001", string(auth.RoleUser))
	conn := mustAgentsEdge(t, qr, lonely, nil, nil, nil)
	if conn.TotalCount != 0 || len(conn.Nodes) != 0 {
		t.Fatalf("user with no own agents should see empty, got total=%d nodes=%d", conn.TotalCount, len(conn.Nodes))
	}
}

// --- pagination bounds -----------------------------------------------------

// Pagination edge cases: a page past the end yields zero nodes but the correct
// total/totalPages; zero/negative page and pageSize fall back to defaults; an
// oversized pageSize returns every row on page 1.
func TestAgentsEdge_PaginationBounds(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	bg := context.Background()
	ctx := adminCtx()

	owner := r.Ent.User.Create().SetUsername("po").SetEmail("po@x.io").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	const total = 5
	for i := 0; i < total; i++ {
		seedAgentEdge(t, r, "p"+string(rune('a'+i)), "goose", agent.StatusRunning, owner.ID, nil, nil)
	}

	// page far beyond the data → empty page, but total + totalPages intact.
	beyond := mustAgentsEdge(t, qr, ctx, nil, &model.Pagination{Page: 99, PageSize: 2}, nil)
	if beyond.TotalCount != total {
		t.Fatalf("out-of-range page TotalCount = %d, want %d", beyond.TotalCount, total)
	}
	if len(beyond.Nodes) != 0 {
		t.Fatalf("page 99 should be empty, got %d nodes", len(beyond.Nodes))
	}
	if beyond.PageInfo.TotalPages != 3 { // ceil(5/2) = 3
		t.Fatalf("totalPages = %d, want 3", beyond.PageInfo.TotalPages)
	}

	// zero/negative page & pageSize → defaults (page 1, pageSize 10).
	deg := mustAgentsEdge(t, qr, ctx, nil, &model.Pagination{Page: 0, PageSize: 0}, nil)
	if deg.PageInfo.Page != 1 || deg.PageInfo.PageSize != 10 {
		t.Fatalf("zero pagination should default, got %+v", deg.PageInfo)
	}
	if len(deg.Nodes) != total { // all 5 fit in the default size-10 page
		t.Fatalf("default page nodes = %d, want %d", len(deg.Nodes), total)
	}
	negative := mustAgentsEdge(t, qr, ctx, nil, &model.Pagination{Page: -3, PageSize: -7}, nil)
	if negative.PageInfo.Page != 1 || negative.PageInfo.PageSize != 10 {
		t.Fatalf("negative pagination should default, got %+v", negative.PageInfo)
	}

	// oversized pageSize → one page holds everything.
	big := mustAgentsEdge(t, qr, ctx, nil, &model.Pagination{Page: 1, PageSize: 1000}, nil)
	if len(big.Nodes) != total || big.PageInfo.TotalPages != 1 {
		t.Fatalf("oversize pageSize: nodes=%d totalPages=%d, want %d/1", len(big.Nodes), big.PageInfo.TotalPages, total)
	}
}

// --- filter edge cases -----------------------------------------------------

// Cross-entity keyword filters (owner/key) resolve matching ids first, then
// constrain the agent query by FK. A keyword that matches NO user / NO key must
// fail closed (zero results) rather than ignoring the filter and returning all.
func TestAgentsEdge_KeywordFiltersFailClosed(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	bg := context.Background()
	ctx := adminCtx()

	owner := r.Ent.User.Create().SetUsername("realuser").SetEmail("real@x.io").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	vk := seedVirtualKey(r.Ent, "sk-real").
		SetModels([]string{"smart"}).SetName("real-key").SaveX(bg)
	seedAgentEdge(t, r, "agentA", "goose", agent.StatusRunning, owner.ID, &vk.ID, nil)
	seedAgentEdge(t, r, "agentB", "goose", agent.StatusRunning, owner.ID, nil, nil)

	// sanity: without a filter, both agents are visible.
	if all := mustAgentsEdge(t, qr, ctx, nil, nil, nil); all.TotalCount != 2 {
		t.Fatalf("baseline TotalCount = %d, want 2", all.TotalCount)
	}

	// ownerKeyword matching no user → empty (fail-closed IN()).
	noOwner := mustAgentsEdge(t, qr, ctx, &model.AgentFilter{OwnerKeyword: ptr("nobody-xyz")}, nil, nil)
	if noOwner.TotalCount != 0 || len(noOwner.Nodes) != 0 {
		t.Fatalf("non-matching ownerKeyword must be empty, got total=%d", noOwner.TotalCount)
	}
	// keyKeyword matching no virtual key → empty (fail-closed IN()).
	noKey := mustAgentsEdge(t, qr, ctx, &model.AgentFilter{KeyKeyword: ptr("ghost-key-zzz")}, nil, nil)
	if noKey.TotalCount != 0 || len(noKey.Nodes) != 0 {
		t.Fatalf("non-matching keyKeyword must be empty, got total=%d", noKey.TotalCount)
	}
	// a matching keyKeyword only catches the agent that actually carries the key
	// (agentB has no key, so it must be excluded — proving the FK constraint).
	hit := mustAgentsEdge(t, qr, ctx, &model.AgentFilter{KeyKeyword: ptr("real-key")}, nil, nil)
	if hit.TotalCount != 1 || findNode(hit.Nodes, "agentA") == nil {
		t.Fatalf("keyKeyword should match only agentA, got %+v", hit.Nodes)
	}
}

// An empty-string keyword/type is treated as "no filter" (derefString == ""),
// so it must NOT fail closed — every agent stays visible.
func TestAgentsEdge_EmptyStringFiltersAreNoOp(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	bg := context.Background()
	ctx := adminCtx()

	owner := r.Ent.User.Create().SetUsername("eo").SetEmail("eo@x.io").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	seedAgentEdge(t, r, "e1", "goose", agent.StatusRunning, owner.ID, nil, nil)
	seedAgentEdge(t, r, "e2", "xiaoguai", agent.StatusStopped, owner.ID, nil, nil)

	empties := &model.AgentFilter{
		Type:         ptr(""),
		NameKeyword:  ptr(""),
		OwnerKeyword: ptr(""),
		KeyKeyword:   ptr(""),
	}
	conn := mustAgentsEdge(t, qr, ctx, empties, nil, nil)
	if conn.TotalCount != 2 {
		t.Fatalf("empty-string filters must be no-ops, got total=%d, want 2", conn.TotalCount)
	}
}

// Combined filters AND together: status + type must intersect, not union.
func TestAgentsEdge_CombinedFiltersIntersect(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	bg := context.Background()
	ctx := adminCtx()

	owner := r.Ent.User.Create().SetUsername("co").SetEmail("co@x.io").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	seedAgentEdge(t, r, "running-goose", "goose", agent.StatusRunning, owner.ID, nil, nil)
	seedAgentEdge(t, r, "stopped-goose", "goose", agent.StatusStopped, owner.ID, nil, nil)
	seedAgentEdge(t, r, "running-xg", "xiaoguai", agent.StatusRunning, owner.ID, nil, nil)

	conn := mustAgentsEdge(t, qr, ctx, &model.AgentFilter{
		Status: ptr(model.AgentStatusRunning),
		Type:   ptr("goose"),
	}, nil, nil)
	if conn.TotalCount != 1 || findNode(conn.Nodes, "running-goose") == nil {
		t.Fatalf("status+type should intersect to running-goose only, got %+v", conn.Nodes)
	}
}

// nameKeyword matching is case-insensitive (NameContainsFold) and substring.
func TestAgentsEdge_NameKeywordCaseInsensitive(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	bg := context.Background()
	ctx := adminCtx()

	owner := r.Ent.User.Create().SetUsername("nk").SetEmail("nk@x.io").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	seedAgentEdge(t, r, "ProductionBot", "goose", agent.StatusRunning, owner.ID, nil, nil)
	seedAgentEdge(t, r, "staging", "goose", agent.StatusRunning, owner.ID, nil, nil)

	conn := mustAgentsEdge(t, qr, ctx, &model.AgentFilter{NameKeyword: ptr("PRODUCTION")}, nil, nil)
	if conn.TotalCount != 1 || findNode(conn.Nodes, "ProductionBot") == nil {
		t.Fatalf("case-insensitive nameKeyword should match ProductionBot, got %+v", conn.Nodes)
	}
}

// --- visibility scoping by role -------------------------------------------

// Three-track visibility at the boundary: admin sees every tenant's agents; a
// tenant-admin sees ONLY their own tenant's; a regular user sees ONLY their own
// agents — across the SAME seeded dataset.
func TestAgentsEdge_VisibilityScopingByRole(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	bg := context.Background()

	uA := r.Ent.User.Create().SetUsername("ua").SetEmail("ua@x.io").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	uB := r.Ent.User.Create().SetUsername("ub").SetEmail("ub@x.io").
		SetPasswordHash("x").SetRole("user").SaveX(bg)

	// Three agents: a-own (uA), a-other (random owner), b-one (uB).
	seedAgentEdge(t, r, "a-own", "goose", agent.StatusRunning, uA.ID, nil, nil)
	seedAgentEdge(t, r, "a-other", "goose", agent.StatusRunning, uuid.New(), nil, nil)
	seedAgentEdge(t, r, "b-one", "goose", agent.StatusRunning, uB.ID, nil, nil)

	// admin → all 3.
	if c := mustAgentsEdge(t, qr, adminCtx(), nil, nil, nil); c.TotalCount != 3 {
		t.Fatalf("admin should see all 3, got %d", c.TotalCount)
	}

	// read_only → all 3 (new in 3-role refactor: read_only sees all agents).
	roCtx := readOnlyCtx()
	roConn := mustAgentsEdge(t, qr, roCtx, nil, nil, nil)
	if roConn.TotalCount != 3 {
		t.Fatalf("read_only should see all 3, got %d", roConn.TotalCount)
	}

	// regular user uA → only the agent they own.
	uaConn := mustAgentsEdge(t, qr, userCtx(uA.ID.String(), string(auth.RoleUser)), nil, nil, nil)
	if uaConn.TotalCount != 1 || findNode(uaConn.Nodes, "a-own") == nil {
		t.Fatalf("user uA should see only a-own, got %+v", uaConn.Nodes)
	}
	if findNode(uaConn.Nodes, "a-other") != nil {
		t.Fatal("regular user must not see another owner's agent")
	}
	if findNode(uaConn.Nodes, "b-one") != nil {
		t.Fatal("regular user must not see another user's agent")
	}
}

// An unauthenticated caller (no CurrentUser on the context) is rejected, not
// silently served everyone's agents.
func TestAgentsEdge_UnauthenticatedRejected(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	if _, err := qr.Agents(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("unauthenticated agents query must error")
	}
}

// A regular user whose id is not a valid UUID must not panic the resolver. This
// documents and PINS the CURRENT behavior of the Agents resolver's `default`
// (regular-user) branch: it only applies the owner predicate when uuid.Parse
// succeeds, so a malformed cu.ID leaves the query UNSCOPED and the caller sees
// every agent.
//
// That is a latent fail-OPEN: a regular user normally only carries a valid id
// (it comes from the session), but if a bad id ever reached this branch the
// owner-track scope would be silently dropped. The secure behavior would be to
// fail closed (zero rows). When the resolver is hardened to do that, flip the
// assertion below from "sees the row" to "sees nothing" — the test will then
// guard the fix instead of the gap. Either way it must never panic.
func TestAgentsEdge_UserWithBadIDNoPanic(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	bg := context.Background()

	owner := r.Ent.User.Create().SetUsername("bid").SetEmail("bid@x.io").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	seedAgentEdge(t, r, "owned", "goose", agent.StatusRunning, owner.ID, nil, nil)

	// id is not a UUID → uuid.Parse fails in the resolver's default branch.
	badIDCtx := userCtx("i-am-not-a-uuid", string(auth.RoleUser))
	conn := mustAgentsEdge(t, qr, badIDCtx, nil, nil, nil) // must not panic / error

	// Hardened behavior: a non-UUID session id fails CLOSED — the owner track
	// scopes to zero rows, so the caller sees NO agents (never the whole system).
	if findNode(conn.Nodes, "owned") != nil {
		t.Fatalf("fail-closed expected: a bad (non-UUID) caller id must not surface another's agent; got %+v", conn.Nodes)
	}
	if len(conn.Nodes) != 0 {
		t.Fatalf("fail-closed expected: zero agents for a bad caller id, got %d", len(conn.Nodes))
	}
}

// --- field resolver nil-safety / bad ids ----------------------------------

// Owner / APIKey / Credentials must return (nil, nil) — not an error, not a
// panic — when the FK points at a deleted user or virtual key. Exercised via
// direct resolver calls so no request-loader cache is installed (the single-row
// fallback path in loadUser/loadVirtualKey).
func TestAgentsEdge_FieldResolversNilSafety_DeletedRefs(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	ar := &agentResolver{r}

	deadOwner := uuid.New()
	deadKey := uuid.New()
	obj := &model.Agent{
		ID:           uuid.New().String(),
		Name:         "orphan",
		Type:         "goose",
		OwnerUserID:  deadOwner, // no such user row
		VirtualKeyID: &deadKey,  // no such key row
	}

	owner, err := ar.Owner(ctx, obj)
	if err != nil {
		t.Fatalf("Owner on deleted owner should not error, got %v", err)
	}
	if owner != nil {
		t.Fatalf("Owner on deleted owner should be nil, got %+v", owner)
	}

	creds, err := ar.Credentials(ctx, obj)
	if err != nil {
		t.Fatalf("Credentials on deleted owner should not error, got %v", err)
	}
	if creds != nil {
		t.Fatalf("Credentials on deleted owner should be nil, got %+v", creds)
	}

	key, err := ar.APIKey(ctx, obj)
	if err != nil {
		t.Fatalf("APIKey on deleted key should not error, got %v", err)
	}
	if key != nil {
		t.Fatalf("APIKey on deleted key should be nil, got %+v", key)
	}
}

// APIKey on an agent that simply never had a key (VirtualKeyID nil) short-circuits
// to (nil, nil) without touching the DB.
func TestAgentsEdge_APIKeyNilWhenNoKey(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ar := &agentResolver{r}

	obj := &model.Agent{ID: uuid.New().String(), Name: "keyless", Type: "goose", OwnerUserID: uuid.New()}
	key, err := ar.APIKey(context.Background(), obj)
	if err != nil || key != nil {
		t.Fatalf("keyless agent APIKey must be (nil,nil), got %+v / %v", key, err)
	}
}

// TypeLabel falls back to the raw kind when no catalog template exists, and
// must not panic on an unknown/never-seeded type.
func TestAgentsEdge_TypeLabelFallbackUnknownKind(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ar := &agentResolver{r}

	obj := &model.Agent{ID: uuid.New().String(), Name: "x", Type: "totally-unknown-kind"}
	label, err := ar.TypeLabel(context.Background(), obj)
	if err != nil {
		t.Fatalf("TypeLabel should not error on unknown kind, got %v", err)
	}
	if label != "totally-unknown-kind" {
		t.Fatalf("TypeLabel fallback = %q, want the raw kind", label)
	}
}

// A non-existent agent id never panics the owner-scoped mutation guard; it reads
// as not-found (no existence oracle), identical to a malformed id error path.
func TestAgentsEdge_SetStatusNonexistentNoPanic(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}

	caller := userCtx("11111111-1111-1111-1111-111111111111", string(auth.RoleUser))
	// well-formed but non-existent id
	if _, err := mr.SetAgentStatus(caller, uuid.New().String(), model.AgentStatusStopped); err == nil {
		t.Fatal("set status on non-existent agent must error (not-found)")
	}
	// malformed id
	if _, err := mr.SetAgentStatus(caller, "not-a-uuid", model.AgentStatusStopped); err == nil {
		t.Fatal("set status on malformed id must error")
	}
}
