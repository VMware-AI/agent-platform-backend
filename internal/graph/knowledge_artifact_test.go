package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// LLD-11 K0 / AC-1: knowledge is a first-class Artifact kind. An OKF bundle that
// fits inline (single-file knowledge ≤64K) stores its content and recomputes
// sha256 from the bytes, exactly like config/script. Larger bundles keep content
// empty and reference a uri (object/Harbor backend, §6).
func TestUpsertArtifact_Knowledge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	body := "# Orders\nOne row per completed order. See [customers](customers.md)."
	a, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "sales-kb", Kind: model.ArtifactKindKnowledge, Version: "1.0.0",
		URI: "inline://sales-kb", Content: &body,
		Metadata: map[string]any{"type": "knowledge-pack", "pages": float64(3)},
	})
	if err != nil {
		t.Fatalf("UpsertArtifact(knowledge): %v", err)
	}
	if a.Kind != model.ArtifactKindKnowledge {
		t.Fatalf("kind = %q, want knowledge", a.Kind)
	}
	if a.Content == nil || *a.Content != body {
		t.Fatalf("knowledge content not stored: %v", a.Content)
	}
	// sha256 recomputed from content (AC-1: digest matches stored bytes).
	sum := sha256.Sum256([]byte(body))
	if a.Sha256 == nil || *a.Sha256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 not recomputed: %v", a.Sha256)
	}
	if a.Metadata == nil {
		t.Fatal("pack-level metadata not preserved")
	}

	// A larger bundle references a uri with no inline content (object backend).
	big, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "big-kb", Kind: model.ArtifactKindKnowledge, Version: "1.0.0",
		URI: "harbor://kb/big-kb:1.0.0",
	})
	if err != nil {
		t.Fatalf("UpsertArtifact(knowledge, uri-only): %v", err)
	}
	if big.Content != nil && *big.Content != "" {
		t.Fatalf("uri-only knowledge should have empty content, got %v", big.Content)
	}

	// AC-1: knowledge shows up in the content-lib listing and version list.
	qr := &queryResolver{r}
	all, err := qr.Artifacts(ctx)
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	var seen bool
	for _, art := range all {
		if art.Name == "sales-kb" && art.Kind == model.ArtifactKindKnowledge {
			seen = true
		}
	}
	if !seen {
		t.Fatal("knowledge artifact missing from artifacts listing")
	}
	vers, err := qr.ArtifactVersions(ctx, "sales-kb")
	if err != nil || len(vers) != 1 {
		t.Fatalf("ArtifactVersions = %v (err %v), want 1", len(vers), err)
	}
}
