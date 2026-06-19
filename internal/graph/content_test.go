package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

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
	arts, _ := qr.Artifacts(ctx)
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
	if arts2, _ := qr.Artifacts(ctx); len(arts2) != 0 {
		t.Fatalf("artifact not deleted: %d", len(arts2))
	}
}
