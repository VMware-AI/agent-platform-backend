package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// TestUpstreamLifecycle_SyncsAndDeletesGatewayModel pins the orphan-model fix
// (#29): an upstream's litellm deployment must track its lifecycle —
//
//	(1) enabled → pushed, pinned by model_info.id = the Upstream row id;
//	(2) disabled → removed from the pool (not left serving live);
//	(3) deleted → its gateway model is deleted (no orphan deployment).
//
// The litellm contract (custom model_info.id honored + delete-by-that-id works)
// was verified against a real local litellm; this test pins the backend wiring.
func TestUpstreamLifecycle_SyncsAndDeletesGatewayModel(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fm := &fakeModelManager{}
	r.GatewayModels = fm
	mr := &mutationResolver{r}
	ctx := adminCtx()

	// (1) an enabled upstream is pushed, pinned by model_info.id = the upstream row id.
	up, err := mr.UpsertUpstream(ctx, model.UpsertUpstreamInput{
		Name: "tier-fast", Provider: model.UpstreamProviderVllm, Model: "openai/qwen-7b",
	})
	if err != nil {
		t.Fatalf("UpsertUpstream(enabled): %v", err)
	}
	if len(fm.models) != 1 || fm.models[0].ModelID != up.ID {
		t.Fatalf("enabled upstream must be pushed with model_info.id=%q, models=%+v", up.ID, fm.models)
	}

	// (2) disabling it removes it from the gateway pool and does NOT re-push it.
	disabled := false
	if _, err := mr.UpsertUpstream(ctx, model.UpsertUpstreamInput{
		Name: "tier-fast", Provider: model.UpstreamProviderVllm, Model: "openai/qwen-7b", Enabled: &disabled,
	}); err != nil {
		t.Fatalf("UpsertUpstream(disable): %v", err)
	}
	if len(fm.models) != 1 {
		t.Fatalf("a disabled upstream must NOT be (re-)pushed live, models=%+v", fm.models)
	}
	if len(fm.deleted) == 0 || fm.deleted[len(fm.deleted)-1] != up.ID {
		t.Fatalf("a disabled upstream must be removed from the gateway, deleted=%v", fm.deleted)
	}

	// (3) deleting the upstream deletes its gateway model (no orphan deployment).
	before := len(fm.deleted)
	if _, err := mr.DeleteUpstream(ctx, up.ID); err != nil {
		t.Fatalf("DeleteUpstream: %v", err)
	}
	if len(fm.deleted) <= before || fm.deleted[len(fm.deleted)-1] != up.ID {
		t.Fatalf("DeleteUpstream must delete the gateway model %q, deleted=%v", up.ID, fm.deleted)
	}
}
