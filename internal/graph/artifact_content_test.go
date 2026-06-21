package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/artifact"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

func TestUpsertArtifact_InlineContent(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	content := "model: smart\nlog: debug"
	a, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "goose-cfg", Kind: model.ArtifactKindConfig, Version: "1.0.0",
		URI: "inline://goose-cfg", Content: &content,
	})
	if err != nil {
		t.Fatalf("UpsertArtifact: %v", err)
	}
	if a.Content == nil || *a.Content != content {
		t.Fatalf("content not stored: %v", a.Content)
	}
	// sha256 is recomputed from content (digest always matches stored bytes)
	sum := sha256.Sum256([]byte(content))
	if a.Sha256 == nil || *a.Sha256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 not recomputed from content: %v", a.Sha256)
	}

	// content on a package artifact is rejected (packages use uri)
	if _, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "pkg", Kind: model.ArtifactKindPackage, Version: "1", URI: "u", Content: &content,
	}); err == nil {
		t.Fatal("inline content on package should be rejected")
	}

	// oversized content is rejected
	big := strings.Repeat("x", maxArtifactContent+1)
	if _, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "big", Kind: model.ArtifactKindConfig, Version: "1", URI: "u", Content: &big,
	}); err == nil {
		t.Fatal("oversized content should be rejected")
	}
}

func TestResolveAgentConfig_Chain(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()

	// agent → config → artifact(content) with a metadata path override
	art := r.Ent.Artifact.Create().SetName("c").SetKind(artifact.KindConfig).
		SetVersion("1").SetURI("inline://c").SetContent("hello: world").
		SetMetadata(map[string]any{"config_path": "/opt/agent/cfg.yaml"}).SaveX(ctx)
	cfg := r.Ent.AgentConfig.Create().SetName("c1").SetAgentType("goose").
		SetArtifactID(art.ID).SaveX(ctx)
	ag := r.Ent.Agent.Create().SetName("a").SetAgentType("goose").
		SetOwnerUserID(uuid.New()).SetConfigID(cfg.ID).SaveX(ctx)

	content, path := r.resolveAgentConfig(ctx, ag)
	if content != "hello: world" {
		t.Fatalf("content = %q", content)
	}
	if path != "/opt/agent/cfg.yaml" {
		t.Fatalf("metadata config_path override not applied: %q", path)
	}

	// artifact without a path override falls back to the default path
	art2 := r.Ent.Artifact.Create().SetName("d").SetKind(artifact.KindConfig).
		SetVersion("1").SetURI("inline://d").SetContent("x: 1").SaveX(ctx)
	cfg2 := r.Ent.AgentConfig.Create().SetName("c2").SetAgentType("goose").
		SetArtifactID(art2.ID).SaveX(ctx)
	ag2 := r.Ent.Agent.Create().SetName("b").SetAgentType("goose").
		SetOwnerUserID(uuid.New()).SetConfigID(cfg2.ID).SaveX(ctx)
	if _, path := r.resolveAgentConfig(ctx, ag2); path != defaultAgentConfigPath {
		t.Fatalf("default path = %q, want %q", path, defaultAgentConfigPath)
	}

	// agent with no config → no content
	ag3 := r.Ent.Agent.Create().SetName("c3").SetAgentType("goose").
		SetOwnerUserID(uuid.New()).SaveX(ctx)
	if c, _ := r.resolveAgentConfig(ctx, ag3); c != "" {
		t.Fatalf("expected no config for config-less agent, got %q", c)
	}
}
