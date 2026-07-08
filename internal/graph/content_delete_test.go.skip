package graph

import (
	"context"
	"testing"
)

// #36 coverage (low): DeleteSkill / DeleteImage destructive mutations had 0%
// coverage. Happy path (row gone) + invalid-id branch. (DeleteProviderModel
// was previously covered by TestDeleteUpstream here; that tested the since-
// removed `DeleteUpstream` alias, now exercised through TestDeleteProviderModel
// in gateway_routing_keys_test.go.)

func TestDeleteSkill(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	s, err := r.Ent.Skill.Create().SetName("sk").SetVersion("1.0").SetURI("oci://x/sk:1.0").Save(ctx)
	if err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if ok, err := mr.DeleteSkill(ctx, s.ID.String()); err != nil || !ok {
		t.Fatalf("DeleteSkill: ok=%v err=%v", ok, err)
	}
	if _, err := r.Ent.Skill.Get(ctx, s.ID); err == nil {
		t.Error("skill should be gone")
	}
	if _, err := mr.DeleteSkill(ctx, "bad"); err == nil {
		t.Error("invalid id must error")
	}
}

func TestDeleteImage(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	img, err := r.Ent.Image.Create().SetRepository("repo/app").SetTag("v1").Save(ctx)
	if err != nil {
		t.Fatalf("seed image: %v", err)
	}
	if ok, err := mr.DeleteImage(ctx, img.ID.String()); err != nil || !ok {
		t.Fatalf("DeleteImage: ok=%v err=%v", ok, err)
	}
	if _, err := r.Ent.Image.Get(ctx, img.ID); err == nil {
		t.Error("image should be gone")
	}
	if _, err := mr.DeleteImage(ctx, "bad"); err == nil {
		t.Error("invalid id must error")
	}
}
