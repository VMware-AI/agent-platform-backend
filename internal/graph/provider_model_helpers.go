package graph

// 0.1.x: ProviderModel resolver helpers — moved out of provider-model.resolvers.go
// to keep gqlgen's `// !!! WARNING !!!` deprecation block from gobbling them up
// on every regenerate. Helper functions that aren't directly the resolver bodies
// for GraphQL fields are treated as "to be deleted" by gqlgen when colocated
// with a resolver file — splitting them into a separate file defies that.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// minSpecsLen is the minimum number of specs in a ProviderModel.
// Schema validation enforces this; we also guard here so /model/new isn't
// called for an empty provider (litellm rejects it).
const minSpecsLen = 1

// requireGatewayMasterKey returns an error if the gateway has no resolvable
// masterKey. Callers use this to fail-fast before pushing /model/new.
func (r *Resolver) requireGatewayMasterKey(ctx context.Context, gw *ent.GatewayConnection) error {
	if gw.MasterKeyRef == "" {
		return gqlerror.Errorf("model gateway %s (%s) has no master key configured", gw.ID, gw.Name)
	}
	mk := r.gatewayMasterKey(ctx, gw)
	if mk == "" {
		return gqlerror.Errorf("model gateway %s (%s) master key cannot be resolved from secret store", gw.ID, gw.Name)
	}
	return nil
}

// litellmClient builds a gateway.HTTPClient pointing at the given gateway.
func (r *Resolver) litellmClient(ctx context.Context, gw *ent.GatewayConnection) (*gateway.HTTPClient, error) {
	mk := r.gatewayMasterKey(ctx, gw)
	if mk == "" {
		return nil, fmt.Errorf("gateway %s has no master key", gw.ID)
	}
	return gateway.NewHTTPClient(gw.Endpoint, mk)
}

// pushSpecToLitellm pushes a single spec to litellm via /model/new.
// On error the caller decides policy (rollback / leave row).
func (r *Resolver) pushSpecToLitellm(ctx context.Context, pm *ent.ProviderModel, gw *ent.GatewayConnection, s specJSON) error {
	c, err := r.litellmClient(ctx, gw)
	if err != nil {
		slog.WarnContext(ctx, "pushSpecToLitellm: litellm client build failed",
			"provider_model", pm.ID.String(), "gateway", gw.ID.String(), "err", err)
		return err
	}
	apiKey := ""
	if r.Secrets != nil && s.LitellmParams.APIKeyRef != nil && *s.LitellmParams.APIKeyRef != "" {
		if cred, err := r.resolveSecret(ctx, *s.LitellmParams.APIKeyRef, secretPurposeProviderModelProbe); err == nil {
			apiKey = cred.APIKey
		}
	}
	wireSpec, err := wireSpecToLitellmModelSpec(pm.Name, s, apiKey)
	if err != nil {
		slog.WarnContext(ctx, "pushSpecToLitellm: wire spec build failed",
			"provider_model", pm.ID.String(), "gateway", gw.ID.String(), "spec_id", s.ModelInfo.ID, "err", err)
		return err
	}
	slog.InfoContext(ctx, "pushSpecToLitellm: POST /model/new",
		"provider_model", pm.ID.String(), "provider_model_name", pm.Name,
		"gateway", gw.ID.String(), "model_name", wireSpec.ModelName,
		"model", wireSpec.Model, "spec_id", wireSpec.ModelID)
	if err := c.NewModel(ctx, wireSpec); err != nil {
		slog.WarnContext(ctx, "pushSpecToLitellm: /model/new failed",
			"provider_model", pm.ID.String(), "provider_model_name", pm.Name,
			"gateway", gw.ID.String(), "model_name", wireSpec.ModelName,
			"spec_id", wireSpec.ModelID, "err", err)
		return err
	}
	slog.InfoContext(ctx, "pushSpecToLitellm: /model/new ok",
		"provider_model", pm.ID.String(), "provider_model_name", pm.Name,
		"gateway", gw.ID.String(), "model_name", wireSpec.ModelName,
		"spec_id", wireSpec.ModelID)
	return nil
}

// deleteSpecFromLitellm best-effort: drops a spec from litellm via /model/delete.
// Errors are logged and swallowed (the row is being deleted anyway).
func (r *Resolver) deleteSpecFromLitellm(ctx context.Context, pm *ent.ProviderModel, gw *ent.GatewayConnection, specID string) {
	c, err := r.litellmClient(ctx, gw)
	if err != nil {
		return
	}
	_ = c.DeleteModel(ctx, specID)
}

// resolveModelGateway parses the gateway id and loads the GatewayConnection.
func (r *Resolver) resolveModelGateway(ctx context.Context, id string) (*ent.GatewayConnection, error) {
	gid, err := uuid.Parse(id)
	if err != nil {
		return nil, gqlerror.Errorf("invalid modelGateway %q", id)
	}
	gw, err := r.Ent.GatewayConnection.Get(ctx, gid)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, gqlerror.Errorf("model gateway %q not found", id)
		}
		return nil, err
	}
	return gw, nil
}

// cleanupMintedSecrets best-effort removes secret refs that were minted in the
// current resolver call. Called when ent Create fails after secrets were minted.
func (r *Resolver) cleanupMintedSecrets(ctx context.Context, refs []string) {
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		r.deleteSecretRef(ctx, ref)
	}
}

// findProviderModelBySpecID scans every ProviderModel row for a spec whose
// modelInfo.id matches specID. Used by single-spec CRUD operations.
// 0.1.x: JSON-array scan;will be replaced by a reverse index table if/when
// row counts grow (see plan §Y.2 — architecture evolution v2).
func (r *Resolver) findProviderModelBySpecID(ctx context.Context, specID string) (*ent.ProviderModel, int, bool, error) {
	if _, err := uuid.Parse(specID); err != nil {
		return nil, 0, false, gqlerror.Errorf("invalid spec id %q", specID)
	}
	rows, err := r.Ent.ProviderModel.Query().All(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	for i := range rows {
		specs, err := parseModelSpecsJSON(rows[i].ModelSpecs)
		if err != nil {
			continue
		}
		if idx, ok := specByIDInJSON(specs, specID); ok {
			return rows[i], idx, true, nil
		}
	}
	return nil, 0, false, nil
}

// pushModelUpdateToLitellm POSTs the bulk /model/update endpoint.
// Implementation lives in gateway/admin.go (PushModelUpdate); on wire-shape
// ambiguity see plan §Y.2 / plan §R6.
func (r *Resolver) pushModelUpdateToLitellm(ctx context.Context, pm *ent.ProviderModel, gw *ent.GatewayConnection, specs []specJSON) error {
	c, err := r.litellmClient(ctx, gw)
	if err != nil {
		return err
	}
	ac := gateway.NewAdminClient(c)
	wireSpecs := make([]gateway.ModelSpec, 0, len(specs))
	for _, s := range specs {
		apiKey := ""
		if r.Secrets != nil && s.LitellmParams.APIKeyRef != nil && *s.LitellmParams.APIKeyRef != "" {
			if cred, err := r.resolveSecret(ctx, *s.LitellmParams.APIKeyRef, secretPurposeProviderModelProbe); err == nil {
				apiKey = cred.APIKey
			}
		}
		wire, err := wireSpecToLitellmModelSpec(pm.Name, s, apiKey)
		if err != nil {
			return err
		}
		wireSpecs = append(wireSpecs, wire)
	}
	return ac.PushModelUpdate(ctx, pm.Name, wireSpecs)
}

// patchSpecOnLitellm PATCHes a single spec via /model/{id}/update.
func (r *Resolver) patchSpecOnLitellm(ctx context.Context, pm *ent.ProviderModel, gw *ent.GatewayConnection, s specJSON) error {
	c, err := r.litellmClient(ctx, gw)
	if err != nil {
		return err
	}
	ac := gateway.NewAdminClient(c)
	apiKey := ""
	if r.Secrets != nil && s.LitellmParams.APIKeyRef != nil && *s.LitellmParams.APIKeyRef != "" {
		if cred, err := r.resolveSecret(ctx, *s.LitellmParams.APIKeyRef, secretPurposeProviderModelProbe); err == nil {
			apiKey = cred.APIKey
		}
	}
	wire, err := wireSpecToLitellmModelSpec(pm.Name, s, apiKey)
	if err != nil {
		return err
	}
	return ac.PatchModel(ctx, s.ModelInfo.ID, modelSpecToMap(wire))
}

// modelSpecToMap converts a gateway.ModelSpec into the partial-update body
// expected by /model/{id}/update.
func modelSpecToMap(s gateway.ModelSpec) map[string]any {
	body := map[string]any{
		"model_name":     s.ModelName,
		"litellm_params": gateway.LitellmParamsFromSpec(s),
	}
	if mi := gateway.ModelInfoFromSpec(s); mi != nil {
		body["model_info"] = mi
	}
	return body
}

// jsonMarshalToBytes / jsonUnmarshalBytes are tiny wrappers used by resolvers
// in this package for JSON round-trips of model_specs map values.
func jsonMarshalToBytes(v any) ([]byte, error) {
	if v == nil {
		return nil, errors.New("nil value")
	}
	return json.Marshal(v)
}

func jsonUnmarshalBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// ReconcileProviderModelDrift runs the per-gateway ProviderModel diff loop for
// the unified reconciler. For every ProviderModel owned by conn it compares
// model_specs[*].modelInfo.id against LiteLLM's /v2/model/info (the spec-IDs
// already persisted to LiteLLM at create time) and detects three drifts:
//
//   - Drift A (LiteLLM-only specs, ids not in DB): detect + log only. IGNORE.
//     LiteLLM is the source of creation; we never auto-import.
//   - Drift B (DB specs, ids missing at LiteLLM): re-push via
//     pushModelUpdateToLitellm so the gateway state matches DB state. Counts
//     in the returned `repushed` total.
//   - Drift C (whole LiteLLM group empty): same as Drift B; whole-group
//     re-push.
//
// Specs whose modelInfo.id is empty (newly created, push hasn't returned yet)
// are skipped — they are "in flight", not drift.
//
// Guard: if every spec on a ProviderModel has empty modelInfo.id we refuse to
// push, since that signature means "specs were never pushed" (mutator failed
// before litellm returned ids), not "specs were dropped".
//
// Exported so internal/reconcile.Reconciler can call it via the ResolverSource
// interface from the unified cycle's provider_models phase.
func (r *Resolver) ReconcileProviderModelDrift(ctx context.Context, conn *ent.GatewayConnection) (repushed int, driftA int, err error) {
	if conn == nil {
		return 0, 0, fmt.Errorf("nil gateway connection")
	}
	// Implementation note: PR #1 ships this as a stub that performs no diff.
	// PR #2 fills in the actual compare-and-repush logic. With Resolver != nil
	// but the real diff not yet wired, the unified cycle's provider_models
	// phase simply reports zero drift every cycle.
	return 0, 0, nil
}
