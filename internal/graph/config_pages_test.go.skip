package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/artifact"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// 平台页核对: artifacts(kind) drives the 智能体配置 知识包选择器 (LLD-11 K2), and
// AgentConfig.artifactId lets the edit form preselect its default_config artifact.
func TestArtifacts_KindFilter_AndConfigArtifactId(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	bg := context.Background()
	qr := &queryResolver{r}

	r.Ent.Artifact.Create().SetName("cfg-a").SetKind(artifact.KindConfig).
		SetVersion("1").SetURI("inline://cfg-a").SetContent("x: 1").SaveX(bg)
	kb := r.Ent.Artifact.Create().SetName("kb-a").SetKind(artifact.KindKnowledge).
		SetVersion("1").SetURI("inline://kb-a").SetContent("# kb").SaveX(bg)
	r.Ent.Artifact.Create().SetName("kb-b").SetKind(artifact.KindKnowledge).
		SetVersion("1").SetURI("inline://kb-b").SetContent("# kb2").SaveX(bg)

	// kind=knowledge → only the two knowledge packs (picker source)
	knowledgeKind := model.ArtifactKindKnowledge
	picks, err := qr.Artifacts(ctx, &knowledgeKind)
	if err != nil {
		t.Fatalf("Artifacts(knowledge): %v", err)
	}
	if len(picks) != 2 {
		t.Fatalf("kind=knowledge should return 2, got %d", len(picks))
	}
	for _, a := range picks {
		if a.Kind != model.ArtifactKindKnowledge {
			t.Fatalf("non-knowledge leaked into filter: %s", a.Kind)
		}
	}
	// no filter → all three kinds
	if all, _ := qr.Artifacts(ctx, nil); len(all) != 3 {
		t.Fatalf("unfiltered should return 3, got %d", len(all))
	}

	// AgentConfig.artifactId reflects the bound default_config artifact
	cfg := r.Ent.AgentConfig.Create().SetName("c1").SetAgentType("goose").
		SetArtifactID(kb.ID).SaveX(bg)
	m := toModelAgentConfig(cfg)
	if m.ArtifactID == nil || *m.ArtifactID != kb.ID.String() {
		t.Fatalf("artifactId not exposed: %v", m.ArtifactID)
	}
	// config without an artifact → nil
	cfg2 := r.Ent.AgentConfig.Create().SetName("c2").SetAgentType("goose").SaveX(bg)
	if toModelAgentConfig(cfg2).ArtifactID != nil {
		t.Fatal("artifact-less config should have nil artifactId")
	}
}
