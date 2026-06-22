package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/artifact"
)

// LLD-11 K2: an AgentConfig can mount N knowledge packs (N:M); only kind=knowledge
// artifacts are accepted; AgentConfig.knowledge lists them; deploy resolves them
// for 下发 (the daemon pulls each via the §6 endpoint).
func TestSetAgentConfigKnowledge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	cfg := r.Ent.AgentConfig.Create().SetName("c1").SetAgentType("goose").SaveX(context.Background())
	kb1 := r.Ent.Artifact.Create().SetName("kb1").SetKind(artifact.KindKnowledge).
		SetVersion("1").SetURI("inline://kb1").SetContent("# A").SaveX(context.Background())
	kb2 := r.Ent.Artifact.Create().SetName("kb2").SetKind(artifact.KindKnowledge).
		SetVersion("1").SetURI("inline://kb2").SetContent("# B").SaveX(context.Background())
	notKnowledge := r.Ent.Artifact.Create().SetName("cfg").SetKind(artifact.KindConfig).
		SetVersion("1").SetURI("inline://cfg").SetContent("x: 1").SaveX(context.Background())

	// associate two knowledge packs
	if _, err := mr.SetAgentConfigKnowledge(ctx, cfg.ID.String(),
		[]string{kb1.ID.String(), kb2.ID.String()}); err != nil {
		t.Fatalf("SetAgentConfigKnowledge: %v", err)
	}

	// AgentConfig.knowledge lists them
	acr := &agentConfigResolver{r}
	packs, err := acr.Knowledge(ctx, toModelAgentConfig(cfg))
	if err != nil {
		t.Fatalf("Knowledge field: %v", err)
	}
	if len(packs) != 2 {
		t.Fatalf("knowledge len = %d, want 2", len(packs))
	}

	// a non-knowledge artifact is rejected (kind guard)
	if _, err := mr.SetAgentConfigKnowledge(ctx, cfg.ID.String(),
		[]string{notKnowledge.ID.String()}); err == nil {
		t.Fatal("non-knowledge artifact should be rejected")
	}

	// setting replaces the set (idempotent): now just kb1
	if _, err := mr.SetAgentConfigKnowledge(ctx, cfg.ID.String(), []string{kb1.ID.String()}); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	packs, _ = acr.Knowledge(ctx, toModelAgentConfig(cfg))
	if len(packs) != 1 || packs[0].Name != "kb1" {
		t.Fatalf("replace failed: %+v", packs)
	}

	// deploy 下发: an agent whose config has knowledge resolves the pack ids
	ag := r.Ent.Agent.Create().SetName("a").SetAgentType("goose").
		SetOwnerUserID(uuid.New()).SetConfigID(cfg.ID).SaveX(context.Background())
	ids := r.resolveAgentKnowledge(ctx, ag)
	if len(ids) != 1 || ids[0] != kb1.ID.String() {
		t.Fatalf("resolveAgentKnowledge = %v, want [%s]", ids, kb1.ID)
	}

	// config-less agent → no packs
	ag2 := r.Ent.Agent.Create().SetName("b").SetAgentType("goose").
		SetOwnerUserID(uuid.New()).SaveX(context.Background())
	if ids := r.resolveAgentKnowledge(ctx, ag2); len(ids) != 0 {
		t.Fatalf("config-less agent should have no packs, got %v", ids)
	}

	// True N:M: the SAME pack (kb1) can be mounted on a second config without
	// being stolen from the first (a join table, not an FK). cfg still has kb1.
	cfg2 := r.Ent.AgentConfig.Create().SetName("c2").SetAgentType("goose").SaveX(context.Background())
	if _, err := mr.SetAgentConfigKnowledge(ctx, cfg2.ID.String(), []string{kb1.ID.String()}); err != nil {
		t.Fatalf("mount shared pack on cfg2: %v", err)
	}
	if p, _ := acr.Knowledge(ctx, toModelAgentConfig(cfg)); len(p) != 1 || p[0].Name != "kb1" {
		t.Fatalf("cfg lost its shared pack (not N:M): %+v", p)
	}
	if p, _ := acr.Knowledge(ctx, toModelAgentConfig(cfg2)); len(p) != 1 || p[0].Name != "kb1" {
		t.Fatalf("cfg2 missing shared pack: %+v", p)
	}
}
