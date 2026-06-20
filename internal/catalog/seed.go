// Package catalog seeds the agent market (AgentTemplate) with the platform's
// built-in installable agents (LLD-05 §1). Seeding is idempotent and create-only:
// it fills in missing kinds without overwriting an operator's customizations made
// via upsertAgentTemplate.
package catalog

import (
	"context"
	"fmt"
	"log"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agenttemplate"
)

// entry is one built-in catalog definition.
type entry struct {
	kind           string
	display        string
	description    string
	installMethod  agenttemplate.InstallMethod
	installCommand string
	version        string
}

// builtins are the M1 active agents (LLD-05 §1: active = goose/xiaoguai/qoder).
// Deferred kinds (Hermès/openclaw) are intentionally NOT seeded — they are not
// selectable in M1 and were purged as unrelated third-party names.
//
// install_command is the display string shown on selection; its {{PLACEHOLDER}}
// tokens are resolved server-side at deploy time (offline mirror base + agent
// user). These are starting definitions; operators may refine via upsert.
var builtins = []entry{
	{
		kind:           "goose",
		display:        "Goose",
		description:    "开源 AI agent;离线 tar 安装(内网镜像,air-gap 可用)。",
		installMethod:  agenttemplate.InstallMethodOfflineTar,
		installCommand: "curl -fsSL {{AGENT_PKG_BASE_URL}}/goose.tar.gz | tar -xz -C /opt/agent && su {{AGENT_USER}} -c /opt/agent/goose/install.sh",
	},
	{
		kind:           "xiaoguai",
		display:        "小怪 (Xiaoguai)",
		description:    "Rust agent 平台;离线 tar 安装(内网镜像,air-gap 可用)。",
		installMethod:  agenttemplate.InstallMethodOfflineTar,
		installCommand: "curl -fsSL {{AGENT_PKG_BASE_URL}}/xiaoguai.tar.gz | tar -xz -C /opt/agent && su {{AGENT_USER}} -c /opt/agent/xiaoguai/install.sh",
	},
	{
		kind:           "qoder",
		display:        "Qoder",
		description:    "在线 agent;curl 安装,需外联(air-gap 不可用)。",
		installMethod:  agenttemplate.InstallMethodCurl,
		installCommand: "curl -fsSL https://qoder.sh/install.sh | sh",
	},
}

// Seed inserts any missing built-in catalog entries. Existing kinds are left
// untouched so operator edits survive restarts.
func Seed(ctx context.Context, client *ent.Client) error {
	// Fetch all existing built-in kinds in ONE query instead of one Exist per
	// entry (this runs on the startup critical path).
	kinds := make([]string, 0, len(builtins))
	for _, e := range builtins {
		kinds = append(kinds, e.kind)
	}
	existingRows, err := client.AgentTemplate.Query().
		Where(agenttemplate.KindIn(kinds...)).
		Select(agenttemplate.FieldKind).All(ctx)
	if err != nil {
		return fmt.Errorf("load existing catalog kinds: %w", err)
	}
	existing := make(map[string]struct{}, len(existingRows))
	for _, row := range existingRows {
		existing[row.Kind] = struct{}{}
	}

	created := 0
	for _, e := range builtins {
		if _, ok := existing[e.kind]; ok {
			continue
		}
		b := client.AgentTemplate.Create().
			SetKind(e.kind).
			SetDisplay(e.display).
			SetInstallMethod(e.installMethod).
			SetStatus(agenttemplate.StatusActive)
		if e.description != "" {
			b.SetDescription(e.description)
		}
		if e.installCommand != "" {
			b.SetInstallCommand(e.installCommand)
		}
		if e.version != "" {
			b.SetVersion(e.version)
		}
		if _, err := b.Save(ctx); err != nil {
			return fmt.Errorf("seed catalog kind %q: %w", e.kind, err)
		}
		created++
	}
	if created > 0 {
		log.Printf("catalog: seeded %d built-in agent template(s)", created)
	}
	return nil
}
