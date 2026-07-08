package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// Artifact metadata round-trips through GraphQL, and artifactVersions returns all
// versions of a name newest-first (LLD-06 §1/§3).
func TestArtifact_MetadataAndVersions(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	meta := map[string]any{"arch": "amd64", "signed": true}
	a, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "goose", Kind: model.ArtifactKindPackage, Version: "1.0.0", URI: "u1", Metadata: meta,
	})
	if err != nil {
		t.Fatalf("UpsertArtifact: %v", err)
	}
	if a.Metadata["arch"] != "amd64" || a.Metadata["signed"] != true {
		t.Fatalf("metadata not round-tripped: %+v", a.Metadata)
	}

	// second version of the same name + an unrelated artifact
	if _, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "goose", Kind: model.ArtifactKindPackage, Version: "1.1.0", URI: "u2",
	}); err != nil {
		t.Fatalf("upsert v2: %v", err)
	}
	if _, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "other", Kind: model.ArtifactKindConfig, Version: "1.0.0", URI: "u3",
	}); err != nil {
		t.Fatalf("upsert other: %v", err)
	}

	versions, err := qr.ArtifactVersions(ctx, "goose")
	if err != nil {
		t.Fatalf("ArtifactVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("want 2 goose versions, got %d", len(versions))
	}
	for _, v := range versions {
		if v.Name != "goose" {
			t.Errorf("artifactVersions leaked a different name: %s", v.Name)
		}
	}
}

func TestContentCRUD(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	// Artifact upsert + idempotency on (name, version)
	sha := "abc123"
	a, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "goose", Kind: model.ArtifactKindPackage, Version: "1.0.0", URI: "u1", Sha256: &sha,
	})
	if err != nil {
		t.Fatalf("UpsertArtifact: %v", err)
	}
	if a.Kind != model.ArtifactKindPackage || a.Sha256 == nil || *a.Sha256 != "abc123" {
		t.Fatalf("artifact wrong: %+v", a)
	}
	if _, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "goose", Kind: model.ArtifactKindPackage, Version: "1.0.0", URI: "u2",
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	arts, _ := qr.Artifacts(ctx, nil)
	if len(arts) != 1 || arts[0].URI != "u2" {
		t.Fatalf("artifact not updated/duplicated: %+v", arts)
	}

	// Skill
	if _, err := mr.UpsertSkill(ctx, model.UpsertSkillInput{Name: "vmware-ops", Version: "1.3.0", URI: "s"}); err != nil {
		t.Fatalf("UpsertSkill: %v", err)
	}
	if skills, _ := qr.Skills(ctx); len(skills) != 1 {
		t.Fatalf("skills = %d", len(skills))
	}

	// Image
	signed := true
	img, err := mr.UpsertImage(ctx, model.UpsertImageInput{Repository: "agent/vm", Tag: "1.0", Signed: &signed})
	if err != nil {
		t.Fatalf("UpsertImage: %v", err)
	}
	if !img.Signed {
		t.Fatal("image should be signed")
	}

	// delete artifact
	ok, err := mr.DeleteArtifact(ctx, a.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteArtifact: %v", err)
	}
	if arts2, _ := qr.Artifacts(ctx, nil); len(arts2) != 0 {
		t.Fatalf("artifact not deleted: %d", len(arts2))
	}
}
