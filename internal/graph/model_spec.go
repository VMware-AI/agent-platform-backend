package graph

// 0.1.x: model_specs JSON ↔ ent ↔ gateway 三向转换 helpers。
//
// 数据库侧:ent `provider_models.model_specs` 是 `[]map[string]any`(每 spec 含
// litellm_params + model_info);modelSpecs JSON 是 source of truth。
// GraphQL 侧:`*model.ModelSpec` 是 wire 类型。
// Gateway 侧:`gateway.ModelSpec` 是 litellm AdminClient 的 wire struct。

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/google/uuid"
)

// specJSON mirrors the on-disk JSON shape for one model_spec.
// Field names match GraphQL wire camelCase, as gqlgen generates.
type specJSON struct {
	LitellmParams litellmParamsJSON `json:"litellmParams"`
	ModelInfo     modelInfoJSON     `json:"modelInfo"`
}

type litellmParamsJSON struct {
	APIKey                         *string  `json:"apiKey"`
	APIKeyRef                      *string  `json:"apiKeyRef"`
	APIBase                        *string  `json:"apiBase"`
	Model                          string   `json:"model"`
	CustomLlmProvider              *string  `json:"customLlmProvider"`
	Organization                   *string  `json:"organization"`
	Tpm                            *int     `json:"tpm"`
	Rpm                            *int     `json:"rpm"`
	DefaultAPIKeyTpmLimit          *int     `json:"defaultApiKeyTpmLimit"`
	DefaultAPIKeyRpmLimit          *int     `json:"defaultApiKeyRpmLimit"`
	MaxBudget                      *float64 `json:"maxBudget"`
	BudgetDuration                 *string  `json:"budgetDuration"`
	UseInPassThrough               *bool    `json:"useInPassThrough"`
	UseChatCompletionsAPI          *bool    `json:"useChatCompletionsApi"`
	MergeReasoningContentInChoices *bool    `json:"mergeReasoningContentInChoices"`
	Tags                           []string `json:"tags"`
	InputCostPerToken              *float64 `json:"inputCostPerToken"`
	OutputCostPerToken             *float64 `json:"outputCostPerToken"`
	CacheReadInputTokenCost        *float64 `json:"cacheReadInputTokenCost"`
	CacheCreationInputTokenCost    *float64 `json:"cacheCreationInputTokenCost"`
}

type modelInfoJSON struct {
	ID              string              `json:"id"`
	Mode            *string             `json:"mode"`
	Blocked         bool                `json:"blocked"`
	AdditionalProp1 *additionalPropJSON `json:"additionalProp1"`
}

type additionalPropJSON struct {
	Status  string  `json:"status"`
	Message *string `json:"message"`
}

func stringPtr(s string) *string { return &s }

// specToJSON converts GraphQL ModelSpecInput + secret ref to on-disk JSON map.
// apiKey plaintext is NEVER persisted;apiKeyRef is the secret-store ref.
func specToJSON(in *model.ModelSpecInput, apiKeyRef string) (map[string]any, error) {
	if in == nil {
		return nil, fmt.Errorf("nil spec input")
	}
	specID := ""
	if in.ModelInfo != nil && in.ModelInfo.ID != nil && *in.ModelInfo.ID != "" {
		parsed, err := uuid.Parse(*in.ModelInfo.ID)
		if err != nil {
			return nil, fmt.Errorf("invalid modelInfo.id: %w", err)
		}
		specID = parsed.String()
	} else {
		specID = uuid.New().String()
	}
	blocked := false
	if in.ModelInfo != nil && in.ModelInfo.Blocked != nil {
		blocked = *in.ModelInfo.Blocked
	}
	var mode *string
	if in.ModelInfo != nil {
		mode = in.ModelInfo.Mode
	}

	spec := specJSON{
		LitellmParams: litellmParamsJSON{
			APIBase:                        in.LitellmParams.APIBase,
			Model:                          in.LitellmParams.Model,
			CustomLlmProvider:              in.LitellmParams.CustomLlmProvider,
			Organization:                   in.LitellmParams.Organization,
			Tpm:                            in.LitellmParams.Tpm,
			Rpm:                            in.LitellmParams.Rpm,
			DefaultAPIKeyTpmLimit:          in.LitellmParams.DefaultAPIKeyTpmLimit,
			DefaultAPIKeyRpmLimit:          in.LitellmParams.DefaultAPIKeyRpmLimit,
			MaxBudget:                      in.LitellmParams.MaxBudget,
			BudgetDuration:                 in.LitellmParams.BudgetDuration,
			UseInPassThrough:               in.LitellmParams.UseInPassThrough,
			UseChatCompletionsAPI:          in.LitellmParams.UseChatCompletionsAPI,
			MergeReasoningContentInChoices: in.LitellmParams.MergeReasoningContentInChoices,
			Tags:                           in.LitellmParams.Tags,
			InputCostPerToken:              in.LitellmParams.InputCostPerToken,
			OutputCostPerToken:             in.LitellmParams.OutputCostPerToken,
			CacheReadInputTokenCost:        in.LitellmParams.CacheReadInputTokenCost,
			CacheCreationInputTokenCost:    in.LitellmParams.CacheCreationInputTokenCost,
		},
		ModelInfo: modelInfoJSON{
			ID:      specID,
			Mode:    mode,
			Blocked: blocked,
		},
	}
	if apiKeyRef != "" {
		ref := apiKeyRef
		spec.LitellmParams.APIKeyRef = &ref
	}
	// Defaults for omitted booleans (GraphQL input nullable → DB stored as non-null).
	f := func(b *bool, def bool) *bool {
		if b == nil {
			return &def
		}
		return b
	}
	spec.LitellmParams.UseInPassThrough = f(spec.LitellmParams.UseInPassThrough, false)
	spec.LitellmParams.UseChatCompletionsAPI = f(spec.LitellmParams.UseChatCompletionsAPI, true)
	spec.LitellmParams.MergeReasoningContentInChoices = f(spec.LitellmParams.MergeReasoningContentInChoices, false)
	if spec.LitellmParams.Tags == nil {
		spec.LitellmParams.Tags = []string{}
	}
	// apiKey plaintext → NEVER persist.
	spec.LitellmParams.APIKey = nil

	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return nil, fmt.Errorf("unmarshal spec to map: %w", err)
	}
	return asMap, nil
}

// specInputToJSONWithID is like specToJSON but reuses an existing spec's id and
// additionalProp1 (worker-managed). Used by addProviderModelSpec /
// updateProviderModelSpec where the caller provides fresh wire input but the
// spec identity + worker state must persist.
func specInputToJSONWithID(existing specJSON, in *model.ModelSpecInput, apiKeyRef string) (map[string]any, error) {
	if in == nil {
		return nil, fmt.Errorf("nil spec input")
	}
	override := in.ModelInfo
	in.ModelInfo = &model.ModelInfoInput{
		ID:      stringPtr(existing.ModelInfo.ID),
		Mode:    nil,
		Blocked: nil,
	}
	if override != nil {
		in.ModelInfo.Mode = override.Mode
		in.ModelInfo.Blocked = override.Blocked
	}
	out, err := specToJSON(in, apiKeyRef)
	if err != nil {
		return nil, err
	}
	// Graft back additionalProp1 (worker-managed).
	jb, _ := json.Marshal(existing.ModelInfo.AdditionalProp1)
	var ap *additionalPropJSON
	if jb != nil && string(jb) != "null" {
		_ = json.Unmarshal(jb, &ap)
	}
	if ap != nil {
		if mi, ok := out["modelInfo"].(map[string]any); ok {
			mi["additionalProp1"] = *ap
		}
	}
	return out, nil
}

// parseModelSpecsJSON unmarshals the ent JSON column into typed slice.
func parseModelSpecsJSON(raw []map[string]any) ([]specJSON, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	jb, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var out []specJSON
	if err := json.Unmarshal(jb, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// marshalModelSpecsJSON serializes a slice back to the ent column shape.
func marshalModelSpecsJSON(specs []specJSON) ([]map[string]any, error) {
	jb, err := json.Marshal(specs)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if err := json.Unmarshal(jb, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// wireModelSpecFromJSON projects DB spec → GraphQL wire ModelSpec.
// apiKey is always nil (filter on wire);apiKeyRef exposed.
func wireModelSpecFromJSON(s specJSON) *model.ModelSpec {
	return &model.ModelSpec{
		LitellmParams: &model.LitellmParams{
			APIKey:                         nil,
			APIKeyRef:                      s.LitellmParams.APIKeyRef,
			APIBase:                        s.LitellmParams.APIBase,
			Model:                          s.LitellmParams.Model,
			CustomLlmProvider:              s.LitellmParams.CustomLlmProvider,
			Organization:                   s.LitellmParams.Organization,
			Tpm:                            s.LitellmParams.Tpm,
			Rpm:                            s.LitellmParams.Rpm,
			DefaultAPIKeyTpmLimit:          s.LitellmParams.DefaultAPIKeyTpmLimit,
			DefaultAPIKeyRpmLimit:          s.LitellmParams.DefaultAPIKeyRpmLimit,
			MaxBudget:                      s.LitellmParams.MaxBudget,
			BudgetDuration:                 s.LitellmParams.BudgetDuration,
			UseInPassThrough:               derefBool(s.LitellmParams.UseInPassThrough),
			UseChatCompletionsAPI:          derefBool(s.LitellmParams.UseChatCompletionsAPI),
			MergeReasoningContentInChoices: derefBool(s.LitellmParams.MergeReasoningContentInChoices),
			Tags:                           s.LitellmParams.Tags,
			InputCostPerToken:              s.LitellmParams.InputCostPerToken,
			OutputCostPerToken:             s.LitellmParams.OutputCostPerToken,
			CacheReadInputTokenCost:        s.LitellmParams.CacheReadInputTokenCost,
			CacheCreationInputTokenCost:    s.LitellmParams.CacheCreationInputTokenCost,
		},
		ModelInfo: &model.ModelInfo{
			ID:              s.ModelInfo.ID,
			Mode:            s.ModelInfo.Mode,
			Blocked:         s.ModelInfo.Blocked,
			AdditionalProp1: wireAdditionalProp1FromJSON(s.ModelInfo.AdditionalProp1),
		},
	}
}

func wireAdditionalProp1FromJSON(a *additionalPropJSON) *model.AdditionalProp1 {
	if a == nil {
		return &model.AdditionalProp1{
			Status:  model.ModelHealthUnknown,
			Message: stringPtr("never probed"),
		}
	}
	health := model.ModelHealth(a.Status)
	switch health {
	case model.ModelHealthHealthy, model.ModelHealthUnhealthy, model.ModelHealthUnknown:
	default:
		health = model.ModelHealthUnknown
	}
	return &model.AdditionalProp1{Status: health, Message: a.Message}
}

func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

// litellmSpecDeleteID returns spec.modelInfo.id for use with /model/delete.
func litellmSpecDeleteID(s specJSON) (string, error) {
	if s.ModelInfo.ID == "" {
		return "", fmt.Errorf("spec missing modelInfo.id")
	}
	if _, err := uuid.Parse(s.ModelInfo.ID); err != nil {
		return "", fmt.Errorf("invalid spec modelInfo.id %q: %w", s.ModelInfo.ID, err)
	}
	return s.ModelInfo.ID, nil
}

// wireSpecToLitellmModelSpec converts a parsed DB spec → gateway wire ModelSpec
// used by /model/new and /model/delete calls.
func wireSpecToLitellmModelSpec(providerName string, s specJSON, secretPlain string) (gateway.ModelSpec, error) {
	apiBase := ""
	if s.LitellmParams.APIBase != nil {
		apiBase = *s.LitellmParams.APIBase
	}
	specID, err := litellmSpecDeleteID(s)
	if err != nil {
		return gateway.ModelSpec{}, err
	}
	return gateway.ModelSpec{
		ModelName:                      providerName,
		Model:                          s.LitellmParams.Model,
		APIBase:                        apiBase,
		APIKey:                         secretPlain,
		ModelID:                        specID,
		CustomLlmProvider:              strOrEmpty(s.LitellmParams.CustomLlmProvider),
		Organization:                   strOrEmpty(s.LitellmParams.Organization),
		Tpm:                            s.LitellmParams.Tpm,
		Rpm:                            s.LitellmParams.Rpm,
		DefaultAPIKeyTpmLimit:          s.LitellmParams.DefaultAPIKeyTpmLimit,
		DefaultAPIKeyRpmLimit:          s.LitellmParams.DefaultAPIKeyRpmLimit,
		MaxBudget:                      s.LitellmParams.MaxBudget,
		BudgetDuration:                 strOrEmpty(s.LitellmParams.BudgetDuration),
		UseInPassThrough:               s.LitellmParams.UseInPassThrough,
		UseChatCompletionsAPI:          s.LitellmParams.UseChatCompletionsAPI,
		MergeReasoningContentInChoices: s.LitellmParams.MergeReasoningContentInChoices,
		Tags:                           s.LitellmParams.Tags,
		InputCostPerToken:              s.LitellmParams.InputCostPerToken,
		OutputCostPerToken:             s.LitellmParams.OutputCostPerToken,
		CacheReadInputTokenCost:        s.LitellmParams.CacheReadInputTokenCost,
		CacheCreationInputTokenCost:    s.LitellmParams.CacheCreationInputTokenCost,
	}, nil
}

func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// toModelProviderModel projects an ent ProviderModel row into the wire shape,
// loading the bound ModelGateway by id and projecting model_specs JSON.
func (r *Resolver) toModelProviderModel(ctx context.Context, pm *ent.ProviderModel) (*model.ProviderModel, error) {
	specs, err := parseModelSpecsJSON(pm.ModelSpecs)
	if err != nil {
		return nil, fmt.Errorf("parse model_specs: %w", err)
	}
	wireSpecs := make([]model.ModelSpec, 0, len(specs))
	for _, s := range specs {
		if w := wireModelSpecFromJSON(s); w != nil {
			wireSpecs = append(wireSpecs, *w)
		}
	}
	mg, err := r.toModelGatewayFor(ctx, pm.ModelGatewayID)
	if err != nil {
		return nil, err
	}
	return &model.ProviderModel{
		ID:            pm.ID.String(),
		Name:          pm.Name,
		ModelGateway:  mg,
		Status:        pm.Status,
		CreatedAt:     pm.CreatedAt.UTC(),
		UpdatedAt:     pm.UpdatedAt.UTC(),
		LastCheckedAt: pm.LastCheckedAt,
		ModelSpecs:    wireSpecs,
	}, nil
}

// toModelGatewayFor loads a single GatewayConnection by id and projects it via
// the resolver's existing toModelGateway (same path as ModelGatewayByID).
func (r *Resolver) toModelGatewayFor(ctx context.Context, id uuid.UUID) (*model.ModelGateway, error) {
	g, err := r.Ent.GatewayConnection.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.toModelGateway(g), nil
}

// specByIDInJSON scans the JSON array in-memory for a spec with the given
// modelInfo.id (UUID string match). Returns the index + parsed spec, or
// (nil, false) if not found. Used by updateProviderModelSpec /
// deleteProviderModelSpec / blockProviderModelSpec to locate the spec by id.
func specByIDInJSON(specs []specJSON, specID string) (int, bool) {
	for i, s := range specs {
		if s.ModelInfo.ID == specID {
			return i, true
		}
	}
	return 0, false
}
