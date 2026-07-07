package graph

import (
	"context"
)

// defaultAgentUser is the OS account installed agents run as when the operator
// hasn't overridden it via the AGENT_USER env var. Historical default — see
// LLD-13; moved out of the DB-backed Setting table as part of the
// PlatformSettings retirement.
const defaultAgentUser = "agent"

// renderInstallVars builds the {{PLACEHOLDER}} substitutions for catalog install
// commands: the static env-derived vars (currently AGENT_PKG_BASE_URL from
// InstallVars) PLUS AGENT_USER read from the resolved Resolver field
// (set in cmd/server from the AGENT_USER env, defaulting to "agent").
// Called once per template-returning resolver (lists fetch once).
func (r *Resolver) renderInstallVars(_ context.Context) map[string]string {
	vars := make(map[string]string, len(r.InstallVars)+1)
	for k, v := range r.InstallVars {
		vars[k] = v
	}
	if r.AgentUser == "" {
		vars["AGENT_USER"] = defaultAgentUser
	} else {
		vars["AGENT_USER"] = r.AgentUser
	}
	return vars
}
