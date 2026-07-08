package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// 前后端整合契约: the agents query is a filtered/sorted/paged connection, and
// Agent resolves owner/apiKey/typeLabel/endpoint lazily from FK columns.
func TestAgents_ConnectionContract(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	bg := context.Background()
	qr := &queryResolver{r}

	// catalog template gives typeLabel; two owners; two keys.
	r.Ent.AgentTemplate.Create().SetKind("goose").SetDisplay("Goose").SaveX(bg)
	alice := r.Ent.User.Create().SetUsername("alice").SetEmail("alice@corp.com").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	bob := r.Ent.User.Create().SetUsername("bob").SetEmail("bob@corp.com").
		SetPasswordHash("x").SetRole("user").SaveX(bg)
	keyA := r.Ent.VirtualKey.Create().SetLitellmKey("sk-a").
		SetMaskedKey("sk-***").SetOrganizationID("org-conn").SetModelGatewayID(uuid.New()).
		SetModels([]string{"smart"}).SetName("alice-key").SaveX(bg)

	mk := func(name, kind string, status agent.Status, owner uuid.UUID, key *uuid.UUID) {
		c := r.Ent.Agent.Create().SetName(name).SetAgentType(kind).
			SetStatus(status).SetOwnerUserID(owner)
		if key != nil {
			c.SetVirtualKeyID(*key)
		}
		c.SaveX(bg)
	}
	mk("zeta", "goose", agent.StatusRunning, alice.ID, &keyA.ID)
	mk("alpha", "goose", agent.StatusStopped, bob.ID, nil)
	mk("mid", "xiaoguai", agent.StatusRunning, bob.ID, nil)

	// --- pagination: pageSize 2 → page1=2 nodes, totalCount=3, totalPages=2 ---
	p1, err := qr.Agents(ctx, nil, &model.Pagination{Page: 1, PageSize: 2}, nil)
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	if p1.TotalCount != 3 || len(p1.Nodes) != 2 || p1.PageInfo.TotalPages != 2 {
		t.Fatalf("page1: total=%d nodes=%d pages=%d", p1.TotalCount, len(p1.Nodes), p1.PageInfo.TotalPages)
	}
	p2, _ := qr.Agents(ctx, nil, &model.Pagination{Page: 2, PageSize: 2}, nil)
	if len(p2.Nodes) != 1 {
		t.Fatalf("page2 should have 1 node, got %d", len(p2.Nodes))
	}

	// --- filter: status ---
	run := mustAgents(t, qr, ctx, &model.AgentFilter{Status: ptr(model.AgentStatusRunning)}, nil)
	if run.TotalCount != 2 {
		t.Fatalf("status filter: got %d running, want 2", run.TotalCount)
	}
	// --- filter: type (catalog kind) ---
	xg := mustAgents(t, qr, ctx, &model.AgentFilter{Type: ptr("xiaoguai")}, nil)
	if xg.TotalCount != 1 || xg.Nodes[0].Name != "mid" {
		t.Fatalf("type filter: %+v", xg.Nodes)
	}
	// --- filter: nameKeyword ---
	nk := mustAgents(t, qr, ctx, &model.AgentFilter{NameKeyword: ptr("alph")}, nil)
	if nk.TotalCount != 1 || nk.Nodes[0].Name != "alpha" {
		t.Fatalf("nameKeyword: %+v", nk.Nodes)
	}
	// --- filter: ownerKeyword (substring on username/email) ---
	ok := mustAgents(t, qr, ctx, &model.AgentFilter{OwnerKeyword: ptr("bob")}, nil)
	if ok.TotalCount != 2 {
		t.Fatalf("ownerKeyword bob: got %d, want 2", ok.TotalCount)
	}
	// --- filter: keyKeyword (substring on virtual_key alias) ---
	kk := mustAgents(t, qr, ctx, &model.AgentFilter{KeyKeyword: ptr("alice-key")}, nil)
	if kk.TotalCount != 1 || kk.Nodes[0].Name != "zeta" {
		t.Fatalf("keyKeyword: %+v", kk.Nodes)
	}

	// --- sort: NAME asc / desc ---
	asc := mustAgents(t, qr, ctx, nil, &model.AgentSort{Field: model.AgentSortFieldName, Direction: model.SortDirectionAsc})
	if asc.Nodes[0].Name != "alpha" || asc.Nodes[2].Name != "zeta" {
		t.Fatalf("NAME asc order wrong: %s..%s", asc.Nodes[0].Name, asc.Nodes[2].Name)
	}
	desc := mustAgents(t, qr, ctx, nil, &model.AgentSort{Field: model.AgentSortFieldName, Direction: model.SortDirectionDesc})
	if desc.Nodes[0].Name != "zeta" {
		t.Fatalf("NAME desc: first = %s, want zeta", desc.Nodes[0].Name)
	}
	// --- sort: OWNER (LEFT JOIN on users.username) ---
	byOwner := mustAgents(t, qr, ctx, nil, &model.AgentSort{Field: model.AgentSortFieldOwner, Direction: model.SortDirectionAsc})
	// alice owns zeta; bob owns alpha+mid → alice's agent sorts first.
	if byOwner.Nodes[0].Name != "zeta" {
		t.Fatalf("OWNER asc: first owner should be alice's agent (zeta), got %s", byOwner.Nodes[0].Name)
	}

	// --- field resolvers: owner / apiKey / typeLabel / endpoint ---
	ar := &agentResolver{r}
	zeta := findNode(run.Nodes, "zeta")
	owner, err := ar.Owner(ctx, zeta)
	if err != nil || owner == nil {
		t.Fatalf("Owner: %v / %v", owner, err)
	}
	ur := &userResolver{r}
	if dn, _ := ur.DisplayName(ctx, owner); dn != "alice" {
		t.Fatalf("owner displayName = %q, want alice", dn)
	}
	apiKey, err := ar.APIKey(ctx, zeta)
	if err != nil || apiKey == nil || apiKey.Name != "alice-key" {
		t.Fatalf("apiKey: %+v / %v", apiKey, err)
	}
	if label, _ := ar.TypeLabel(ctx, zeta); label != "Goose" {
		t.Fatalf("typeLabel = %q, want Goose", label)
	}
	// an agent with no key → apiKey nil (not an error)
	alpha := findNode(asc.Nodes, "alpha")
	if k, err := ar.APIKey(ctx, alpha); err != nil || k != nil {
		t.Fatalf("keyless agent apiKey should be nil: %+v / %v", k, err)
	}
	// unknown kind → typeLabel falls back to the kind string
	if label, _ := ar.TypeLabel(ctx, findNode(xg.Nodes, "mid")); label != "xiaoguai" {
		t.Fatalf("typeLabel fallback = %q, want xiaoguai", label)
	}
}

func mustAgents(t *testing.T, qr *queryResolver, ctx context.Context, f *model.AgentFilter, s *model.AgentSort) *model.AgentConnection {
	t.Helper()
	c, err := qr.Agents(ctx, f, nil, s)
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	return c
}

func findNode(nodes []model.Agent, name string) *model.Agent {
	for i := range nodes {
		if nodes[i].Name == name {
			return &nodes[i]
		}
	}
	return nil
}
