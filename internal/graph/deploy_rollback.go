package graph

import (
	"context"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/google/uuid"
)

// deployProvisionTimeout bounds a VM clone + power-on, run on a context detached
// from the HTTP request so the 60s WriteTimeout can't cancel it mid-clone and
// orphan the VM. Generous — a cold OVA clone can take several minutes.
const deployProvisionTimeout = 15 * time.Minute

// vmNameInvalidChars matches characters that are not safe in a vSphere VM name;
// they are collapsed to a single dash. vCenter disallows the special chars
// %/\?*:|"<> among others, so we keep only word chars, dot and dash.
var vmNameInvalidChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// uniqueVMName derives a collision-free vCenter VM name from the agent's display
// name + the first 8 chars of its (unique) id. The display name alone can repeat
// across agents, which would collide on the VM clone; the id suffix disambiguates.
// The result is sanitized to a valid vSphere VM name.
func uniqueVMName(displayName string, id uuid.UUID) string {
	base := strings.Trim(vmNameInvalidChars.ReplaceAllString(displayName, "-"), "-")
	if base == "" {
		base = "agent"
	}
	return base + "-" + id.String()[:8]
}

// deleteAgentRow drops a freshly-created agent row when its deploy aborts before
// any VM/key exists (create-from-OVA flow). The row was created by DeployAgent
// itself, so removing it leaves no orphan. Detached ctx so cleanup runs even if
// the request ctx was canceled. Failures are logged (never swallowed).
//
// It also retires the agent's AgentEnrollment, if any. DeployAgent calls
// IssueEnrollment (when r.AgentMgr != nil) BEFORE provisioning, persisting a
// pending enrollment row whose agent_id is a soft reference (no FK), so deleting
// the agent row does NOT cascade it. Every deploy-fail compensation path funnels
// through here (Provision-fail directly; vk-persist/finalize-fail via
// rollbackDeployCreate), so cleaning the enrollment here keeps a failed deploy
// from leaking an orphan pending enrollment. Best-effort, on the SAME detached
// ctx; failures are logged (never swallowed).
func (r *Resolver) deleteAgentRow(ctx context.Context, ag *ent.Agent) {
	cctx := context.WithoutCancel(ctx)
	if err := r.Ent.Agent.DeleteOne(ag).Exec(cctx); err != nil {
		log.Printf("deploy rollback: delete agent row %s failed: %v", ag.ID, err)
	}
	if r.AgentMgr != nil {
		if err := r.AgentMgr.DeleteEnrollment(cctx, ag.ID); err != nil {
			log.Printf("deploy rollback: delete enrollment for agent %s failed: %v", ag.ID, err)
		}
	}
}

// rollbackDeployCreate compensates a failed create-from-OVA deploy AFTER the VM
// and gateway key already exist: destroy the VM, revoke the key, and delete the
// agent row (it was created by this same deploy and never went live, so we drop
// it rather than marking it exception).
func (r *Resolver) rollbackDeployCreate(ctx context.Context, conn VCenterClient, gw gateway.Client, ag *ent.Agent, vmName, key string) {
	cctx := context.WithoutCancel(ctx)
	if err := conn.Destroy(cctx, vmName); err != nil {
		log.Printf("deploy rollback: orphan VM %q, destroy failed: %v", vmName, err)
	}
	revokeDeployKey(cctx, gw, key, ag.ID.String())
	r.deleteAgentRow(cctx, ag)
}

// rollbackDeployVMOnly compensates a failed deploy that reused an existing key:
// the VM and just-created agent row belong to this attempt, but the key does not.
func (r *Resolver) rollbackDeployVMOnly(ctx context.Context, conn VCenterClient, ag *ent.Agent, vmName string) {
	cctx := context.WithoutCancel(ctx)
	if err := conn.Destroy(cctx, vmName); err != nil {
		log.Printf("deploy rollback: orphan VM %q, destroy failed: %v", vmName, err)
	}
	r.deleteAgentRow(cctx, ag)
}

// revokeDeployKey best-effort revokes a deploy's gateway key during rollback. The
// key was minted on the agent's department/default gateway (gw, LLD-13 §3.3); the
// rollback MUST revoke through that SAME client — not a process-wide default that
// no longer exists — or a failed deploy leaks a live, billable key. Never silent.
func revokeDeployKey(ctx context.Context, gw gateway.Client, key, agentID string) {
	if gw == nil {
		log.Printf("deploy rollback: no gateway to revoke key for agent %s (orphan key)", agentID)
		return
	}
	if err := gw.DeleteKey(ctx, key); err != nil {
		log.Printf("deploy rollback: orphan gateway key for agent %s, revoke failed: %v", agentID, err)
	}
}
