package graph

// 0.1.x: provider health probe (按 model_name 分组聚合 4 档 status + per-spec additionalProp1)
//
// Worker 周期:5 分钟一次(`PROVIDER_PROBE_INTERVAL_SECONDS=300` 默认值),
// 每个 ProviderModel 跑 probeOneProviderModel → 分组聚合 status → 写回 ent。
//
// 实现要点:
//   - per-spec probe (probeOneSpec) → ModelHealth 枚举 + 可选 message
//   - per-model 分组聚合 → 4 档 status 之一
//   - ProviderModel 多组合并 → worst-of 规则
//   - in-memory replace 每个 spec 的 additionalProp1
//   - GIN 索引 (provider_models_model_specs_gin jsonb_path_ops) 加速 spec 反查
//
// 性能触发 v2 演进的阈值(详见 plan §Y):行数 > 20K 且 probe tick 单轮 P95 > 30s;
// 当前 GIN + JSON 是够用的轻量方案。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/providermodel"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

// probeStaleThreshold — last_checked_at 超过此值 → row.status = unknown。
// 0.1.x: 默认 10 分钟(= 5 分钟 probe 周期 + 5 分钟安全余量)。
// 可通过 PROVIDER_PROBE_STALE_THRESHOLD_SECONDS 环境变量覆盖。
const probeStaleThreshold = 10 * time.Minute

// StartProviderModelHealthProbe periodically probes every enabled ProviderModel
// against its upstream APIs and writes the resulting 4-tier status + per-spec
// additionalProp1 back to the row.
//
// Disabled when interval <= 0. Each ProviderModel row gets its own 5s budget
// for per-spec probes; a slow upstream does NOT block the rest of the batch.
func (r *Resolver) StartProviderModelHealthProbe(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		log.Printf("provider health probe: disabled (PROVIDER_PROBE_INTERVAL_SECONDS=0)")
		return
	}
	log.Printf("provider health probe: every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.probeAllProviderModels(ctx)
		}
	}
}

// probeAllProviderModels fans out one probe pass over every ProviderModel.
// Rows run sequentially — the dataset is small (operator-curated) and
// parallelism would just hammer upstream APIs.
func (r *Resolver) probeAllProviderModels(ctx context.Context) {
	rows, err := r.Ent.ProviderModel.Query().All(ctx)
	if err != nil {
		log.Printf("provider health probe: query: %v", err)
		return
	}
	for _, pm := range rows {
		newStatus, updatedSpecs, lastChecked := r.probeOneProviderModel(ctx, pm)
		if newStatus == "" {
			continue
		}
		// Skip DB write if status hasn't changed AND specs unchanged.
		statusChanged := string(pm.Status) != newStatus
		specsChanged := updatedSpecs != nil
		if !statusChanged && !specsChanged {
			continue
		}
		upd := r.Ent.ProviderModel.UpdateOneID(pm.ID).
			SetStatus(providermodel.Status(newStatus)).
			SetLastCheckedAt(lastChecked)
		if specsChanged {
			upd = upd.SetModelSpecs(updatedSpecs)
		}
		if _, err := upd.Save(ctx); err != nil {
			log.Printf("provider health probe: persist %s: %v", pm.Name, err)
		}
	}
}

// probeProviderModelInBackground kicks off one probe pass for a single
// ProviderModel after a write (e.g. createProviderModel). Mirrors the lifecycle
// of syncGatewayInBackground — detach context with a 30s ceiling. Returns a
// chan for tests to wait on; production call sites discard it.
func (r *Resolver) probeProviderModelInBackground(id uuid.UUID) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pm, err := r.Ent.ProviderModel.Get(ctx, id)
		if err != nil {
			log.Printf("provider-model auto-probe: load failed: id=%s err=%v", id, err)
			return
		}
		newStatus, updatedSpecs, lastChecked := r.probeOneProviderModel(ctx, pm)
		if newStatus == "" {
			return
		}
		statusChanged := string(pm.Status) != newStatus
		specsChanged := updatedSpecs != nil
		if !statusChanged && !specsChanged {
			return
		}
		upd := r.Ent.ProviderModel.UpdateOneID(pm.ID).
			SetStatus(providermodel.Status(newStatus)).
			SetLastCheckedAt(lastChecked)
		if specsChanged {
			upd = upd.SetModelSpecs(updatedSpecs)
		}
		if _, err := upd.Save(ctx); err != nil {
			log.Printf("provider-model auto-probe: persist failed: id=%s err=%v", id, err)
		}
	}()
	return done
}

// probeOneProviderModel is the per-row probe. It returns:
//   - newStatus: 4-tier status string ("" = no probe possible, caller skips write)
//   - updatedSpecs: new JSON spec slice with per-spec additionalProp1 patched
//     (nil = unchanged)
//   - lastChecked: time.Time of the probe
//
// Groups specs by spec.litellm_params.model, runs errgroup probes per
// endpoint, aggregates each group, then takes worst-of across groups.
func (r *Resolver) probeOneProviderModel(ctx context.Context, pm *ent.ProviderModel) (string, []map[string]any, time.Time) {
	specs, err := parseModelSpecsJSON(pm.ModelSpecs)
	if err != nil {
		log.Printf("provider health probe: parse %s: %v", pm.Name, err)
		return "", nil, time.Time{}
	}
	if len(specs) == 0 {
		// No specs yet (just-created, no model_specs) → unknown.
		return string(providermodel.StatusUnknown), nil, time.Now().UTC()
	}

	now := time.Now().UTC()

	// Probe every spec independently (errgroup parallel; bounded by 5s each).
	type specResult struct {
		idx  int
		res  specProbeResult
	}
	results := make([]specResult, len(specs))
	g, gctx := errgroup.WithContext(ctx)
	for i := range specs {
		i := i
		s := specs[i]
		g.Go(func() error {
			results[i] = specResult{idx: i, res: r.probeOneSpec(gctx, s)}
			return nil
		})
	}
	_ = g.Wait() // errors already captured per-spec

	// Group spec indices by model (for aggregation).
	groupIndices := map[string][]int{}
	for i, s := range specs {
		if s.LitellmParams.Model == "" {
			continue
		}
		key := s.LitellmParams.Model
		groupIndices[key] = append(groupIndices[key], i)
	}

	perGroupStatus := map[string]providermodel.Status{}
	for model, idxs := range groupIndices {
		var grp []specProbeResult
		for _, i := range idxs {
			grp = append(grp, results[i].res)
		}
		perGroupStatus[model] = r.aggregateGroupHealth(grp)
	}

	if len(perGroupStatus) == 0 {
		return string(providermodel.StatusUnknown), nil, now
	}

	// Patch each spec's additionalProp1 with its individual probe result.
	updated := make([]map[string]any, len(specs))
	copy(updated, pm.ModelSpecs)
	for i := range specs {
		ap := buildAdditionalProp1JSON(results[i].res)
		updated[i] = patchSpecAdditionalProp1(updated[i], ap)
	}

	overall := aggregateProviderModelStatus(perGroupStatus)
	return string(overall), updated, now
}

// aggregateGroupHealth collapses per-endpoint health into the group-level
// 4-tier status. Precedence: full_outage > partial_outage > unknown > full_healthy.
func (r *Resolver) aggregateGroupHealth(results []specProbeResult) providermodel.Status {
	if len(results) == 0 {
		return providermodel.StatusUnknown
	}
	healthy, unhealthy := 0, 0
	for _, h := range results {
		switch h.Status {
		case "healthy":
			healthy++
		case "unhealthy", "unknown":
			unhealthy++
		}
	}
	switch {
	case healthy == 0 && unhealthy > 0:
		return providermodel.StatusFullOutage
	case healthy >= 1 && unhealthy >= 1:
		return providermodel.StatusPartialOutage
	case healthy > 0 && unhealthy == 0:
		return providermodel.StatusFullHealthy
	default:
		return providermodel.StatusUnknown
	}
}

// aggregateProviderModelStatus takes worst-of across groups.
// Precedence: full_outage > partial_outage > unknown > full_healthy.
func aggregateProviderModelStatus(perGroup map[string]providermodel.Status) providermodel.Status {
	priority := map[providermodel.Status]int{
		providermodel.StatusFullOutage:    4,
		providermodel.StatusPartialOutage: 3,
		providermodel.StatusUnknown:       2,
		providermodel.StatusFullHealthy:   1,
	}
	worst := providermodel.StatusFullHealthy
	for _, s := range perGroup {
		if priority[s] > priority[worst] {
			worst = s
		}
	}
	return worst
}

// specProbeResult captures one endpoint probe outcome.
type specProbeResult struct {
	Status  string  // "healthy" | "unhealthy" | "unknown"
	Message *string // nil when healthy; non-nil otherwise
}

// probeOneSpec probes a single spec's upstream API and returns its health.
//
// 5s timeout per endpoint. Error classification:
//   - context.DeadlineExceeded → "unknown" + message "list models: deadline exceeded (5s)"
//   - other errors → "unhealthy" + error message
//   - empty api_base or api_key_ref unresolvable → "unknown" + descriptive message
func (r *Resolver) probeOneSpec(ctx context.Context, s specJSON) specProbeResult {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if s.LitellmParams.APIBase == nil || *s.LitellmParams.APIBase == "" {
		msg := "spec missing apiBase"
		return specProbeResult{Status: "unknown", Message: &msg}
	}

	apiKey := ""
	if r.Secrets != nil && s.LitellmParams.APIKeyRef != nil && *s.LitellmParams.APIKeyRef != "" {
		if cred, err := r.resolveSecret(probeCtx, *s.LitellmParams.APIKeyRef, secretPurposeProviderModelProbe); err == nil {
			apiKey = cred.APIKey
		}
	}
	if apiKey == "" {
		apiKey = "probe-placeholder"
	}

	c, err := gateway.NewHTTPClient(*s.LitellmParams.APIBase, apiKey)
	if err != nil {
		msg := fmt.Sprintf("build client: %v", err)
		return specProbeResult{Status: "unknown", Message: &msg}
	}

	if _, err := c.ListModels(probeCtx); err != nil {
		msg := fmt.Sprintf("list models: %v", err)
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "list models: deadline exceeded (5s)"
			return specProbeResult{Status: "unknown", Message: &msg}
		}
		return specProbeResult{Status: "unhealthy", Message: &msg}
	}
	return specProbeResult{Status: "healthy"} // Message nil
}

// buildAdditionalProp1JSON converts a specProbeResult into the JSON shape
// stored in spec.modelInfo.additionalProp1.
func buildAdditionalProp1JSON(r specProbeResult) additionalPropJSON {
	return additionalPropJSON{Status: r.Status, Message: r.Message}
}

// patchSpecAdditionalProp1 mutates a single spec's JSON map in place, setting
// modelInfo.additionalProp1 to ap. Returns the same map for chaining.
func patchSpecAdditionalProp1(specMap map[string]any, ap additionalPropJSON) map[string]any {
	if specMap == nil {
		return specMap
	}
	mi, ok := specMap["modelInfo"].(map[string]any)
	if !ok {
		mi = map[string]any{}
		specMap["modelInfo"] = mi
	}
	mi["additionalProp1"] = ap
	return specMap
}