package graph

import (
	"context"
	"log"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agenttemplate"
	"github.com/VMware-AI/agent-platform-backend/internal/catalog"
)

// maxArtifactContent caps inline artifact content (matches the ent MaxLen).
const maxArtifactContent = 65536

// defaultAgentConfigPath is where an agent's inline default_config lands in the
// VM when the artifact doesn't override it via metadata["config_path"].
const defaultAgentConfigPath = "/etc/agent/config"

// resolveAgentConfig loads the agent's inline default_config via
// agent.config_id → AgentConfig.artifact_id → Artifact.content, returning the
// content and the VM path to write it to (LLD-09). Empty content means "no
// config" — deploy then degrades to gateway-env only. Best-effort: a missing or
// unreadable config never fails deploy.
func (r *Resolver) resolveAgentConfig(ctx context.Context, ag *ent.Agent) (content, path string) {
	if ag.ConfigID == nil {
		return "", ""
	}
	cfg, err := r.Ent.AgentConfig.Get(ctx, *ag.ConfigID)
	if err != nil || cfg.ArtifactID == nil {
		return "", ""
	}
	art, err := r.Ent.Artifact.Get(ctx, *cfg.ArtifactID)
	if err != nil || art.Content == "" {
		return "", ""
	}
	path = defaultAgentConfigPath
	if p, ok := art.Metadata["config_path"].(string); ok && p != "" {
		path = p
	}
	return art.Content, path
}

// resolveAgentKnowledge returns the ids of the OKF knowledge packs mounted on an
// agent's config (LLD-11 K2), for 下发 via guestinfo at deploy — the daemon pulls
// each over the control-plane channel (§6). Best-effort: a config-less agent or a
// load error yields no packs (knowledge never blocks deploy).
func (r *Resolver) resolveAgentKnowledge(ctx context.Context, ag *ent.Agent) []string {
	if ag.ConfigID == nil {
		return nil
	}
	cfg, err := r.Ent.AgentConfig.Get(ctx, *ag.ConfigID)
	if err != nil {
		return nil
	}
	arts, err := cfg.QueryKnowledge().Order(orderNewest).All(ctx)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(arts))
	for _, a := range arts {
		ids = append(ids, a.ID.String())
	}
	return ids
}

// resolveKnowledgeRoot returns the VM path the daemon should unpack the agent's
// knowledge packs to (LLD-11 K4). It is the agent kind's AgentTemplate
// knowledge_root, or the platform default when unset/unknown.
func (r *Resolver) resolveKnowledgeRoot(ctx context.Context, ag *ent.Agent) string {
	t, err := r.Ent.AgentTemplate.Query().Where(agenttemplate.Kind(ag.AgentType)).Only(ctx)
	if err == nil && t.KnowledgeRoot != "" {
		return t.KnowledgeRoot
	}
	// A genuine DB fault (not just a missing template) silently downgrades a custom
	// root to the default — packs would unpack where the prompt doesn't look. Surface
	// it so the degradation isn't invisible; deploy still proceeds with the default.
	if err != nil && !ent.IsNotFound(err) {
		log.Printf("resolveKnowledgeRoot: kind %q template lookup failed, using default: %v", ag.AgentType, err)
	}
	return catalog.DefaultKnowledgeRoot
}
