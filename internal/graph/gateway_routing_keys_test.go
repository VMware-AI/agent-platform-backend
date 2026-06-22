package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

// 模块③ 路由 / 网关接入: gateway master key + upstream api key are submitted raw by
// the form; the backend writes them to the secret store and persists only the ref
// — plaintext never lands in the DB. An explicit ref remains the alternative.
func TestGatewayRouting_StoresRawKeys(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	store := secrets.NewStaticResolver(nil)
	r.Secrets = store
	ctx := adminCtx()
	bg := context.Background()
	mr := &mutationResolver{r}

	// gateway master key → master_key_ref
	mk := "sk-litellm-master"
	gc, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "gw1", Endpoint: "https://lite", MasterKey: &mk,
	})
	if err != nil {
		t.Fatalf("register gw: %v", err)
	}
	gcRow := r.Ent.GatewayConnection.GetX(bg, uuid.MustParse(gc.ID))
	if !strings.HasPrefix(gcRow.MasterKeyRef, "vault://") {
		t.Fatalf("master_key_ref not a store ref: %q", gcRow.MasterKeyRef)
	}
	if cred, _ := store.Resolve(bg, gcRow.MasterKeyRef); cred.APIKey != mk {
		t.Fatalf("master key not stored: %+v", cred)
	}

	// upstream api key → api_key_ref
	ak := "sk-openai-xxx"
	up, err := mr.UpsertUpstream(ctx, model.UpsertUpstreamInput{
		Name: "openai-up", Provider: model.UpstreamProviderOpenai, Model: "gpt-4", APIKey: &ak,
	})
	if err != nil {
		t.Fatalf("upsert upstream: %v", err)
	}
	upRow := r.Ent.Upstream.GetX(bg, uuid.MustParse(up.ID))
	if !strings.HasPrefix(upRow.APIKeyRef, "vault://") {
		t.Fatalf("api_key_ref not a store ref: %q", upRow.APIKeyRef)
	}
	if cred, _ := store.Resolve(bg, upRow.APIKeyRef); cred.APIKey != ak {
		t.Fatalf("api key not stored: %+v", cred)
	}

	// existing-ref path still works (no raw key) — verbatim, no Put
	ref := "vault://preset-1"
	up2, err := mr.UpsertUpstream(ctx, model.UpsertUpstreamInput{
		Name: "anthropic-up", Provider: model.UpstreamProviderAnthropic, Model: "claude", APIKeyRef: &ref,
	})
	if err != nil {
		t.Fatalf("upsert upstream ref: %v", err)
	}
	if got := r.Ent.Upstream.GetX(bg, uuid.MustParse(up2.ID)).APIKeyRef; got != ref {
		t.Fatalf("apiKeyRef path: %q", got)
	}
}
