package graph

import (
	"context"
	"log"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/setting"
)

// Platform setting keys (LLD-13) — a small fixed vocabulary stored in the Setting
// key/value table, editable in the console rather than via startup env.
const (
	settingKeyAgentUser = "agent_user"
	defaultAgentUser    = "agent"
)

// getSetting returns the stored value for key, or def when the row is absent or
// its value is empty.
func (r *Resolver) getSetting(ctx context.Context, key, def string) string {
	s, err := r.Ent.Setting.Query().Where(setting.KeyEQ(key)).Only(ctx)
	if err != nil {
		// Absent row → default. A real DB error (not "not found") shouldn't be
		// silent — log it but still fall back so a settings read never hard-fails
		// install rendering.
		if !ent.IsNotFound(err) {
			log.Printf("getSetting %q: %v", key, err)
		}
		return def
	}
	if s.Value == "" {
		return def
	}
	return s.Value
}

// setSetting upserts a key/value (update-or-create; no reliance on a dialect
// UPSERT so it works the same on sqlite-dev and postgres-prod). If two writers
// race on the first write, both see n==0 and try Create; the loser hits the
// unique-key constraint, so it falls back to Update (last-writer-wins) rather
// than surfacing a raw DB error to the admin.
func (r *Resolver) setSetting(ctx context.Context, key, value string) error {
	n, err := r.Ent.Setting.Update().Where(setting.KeyEQ(key)).SetValue(value).Save(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := r.Ent.Setting.Create().SetKey(key).SetValue(value).Save(ctx); err != nil {
		// Only a lost create-race (unique-key violation) is recoverable: the row
		// now exists, so update it (last-writer-wins). Any other error is real.
		if !ent.IsConstraintError(err) {
			return err
		}
		if _, uerr := r.Ent.Setting.Update().Where(setting.KeyEQ(key)).SetValue(value).Save(ctx); uerr != nil {
			return uerr
		}
	}
	return nil
}

// renderInstallVars builds the {{PLACEHOLDER}} substitutions for catalog install
// commands: the static env-derived vars (AGENT_PKG_BASE_URL) PLUS AGENT_USER read
// from the DB platform setting (LLD-13), so AGENT_USER is operator-editable, not a
// startup env. Called once per template-returning resolver (lists fetch once).
func (r *Resolver) renderInstallVars(ctx context.Context) map[string]string {
	vars := make(map[string]string, len(r.InstallVars)+1)
	for k, v := range r.InstallVars {
		vars[k] = v
	}
	vars["AGENT_USER"] = r.getSetting(ctx, settingKeyAgentUser, defaultAgentUser)
	return vars
}
