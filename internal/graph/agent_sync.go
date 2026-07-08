package graph

import (
	"context"
	"log"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/google/uuid"
)

// StartAgentVMStatusSync periodically checks vCenter for the actual power
// state of each agent's VM and updates the agent status accordingly.
// Disabled when interval is 0.
func (r *Resolver) StartAgentVMStatusSync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	log.Printf("agent-vm-status sync: every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.syncAgentVMStatuses(ctx)
		}
	}
}

// syncAgentVMStatuses iterates all agents with a VMRef, groups them by
// resource pool, connects to each pool's vCenter, and updates the agent
// status to match the VM's actual power state.
func (r *Resolver) syncAgentVMStatuses(ctx context.Context) {
	ags, err := r.Ent.Agent.Query().
		Where(agent.VMRefNEQ("")).
		Where(agent.StatusIn(agent.StatusRunning, agent.StatusProvisioning)).
		All(ctx)
	if err != nil {
		log.Printf("agent-vm-status sync: query agents: %v", err)
		return
	}
	if len(ags) == 0 {
		return
	}

	// Group by resource pool so we only dial each vCenter once.
	type vmRec struct {
		vmRef string
		id    string
	}
	pools := make(map[string][]vmRec)
	for _, a := range ags {
		if a.ResourcePoolID == nil || a.VMRef == "" {
			continue
		}
		pid := a.ResourcePoolID.String()
		pools[pid] = append(pools[pid], vmRec{vmRef: a.VMRef, id: a.ID.String()})
	}

	for pid, vms := range pools {
		poolID, err := uuid.Parse(pid)
		if err != nil {
			continue
		}
		pool, err := r.Ent.ResourcePool.Get(ctx, poolID)
		if err != nil {
			continue
		}
		conn, err := r.connectPool(ctx, pool)
		if err != nil {
			log.Printf("agent-vm-status sync: connect pool %s: %v", pool.Name, err)
			continue
		}
		vmList, err := conn.ListVMs(ctx)
		_ = conn.Logout(ctx)
		if err != nil {
			log.Printf("agent-vm-status sync: list VMs pool %s: %v", pool.Name, err)
			continue
		}

		vmStates := make(map[string]string, len(vmList))
		for _, v := range vmList {
			vmStates[v.Name] = v.PowerState
		}

		for _, vm := range vms {
			state, found := vmStates[vm.vmRef]
			if !found || state == "poweredOff" || state == "suspended" {
				r.updateAgentStatus(ctx, vm.id, agent.StatusStopped)
			}
		}
	}
}

func (r *Resolver) updateAgentStatus(ctx context.Context, agentID string, status agent.Status) {
	id, err := uuid.Parse(agentID)
	if err != nil {
		return
	}
	_, err = r.Ent.Agent.UpdateOneID(id).SetStatus(status).Save(ctx)
	if err != nil {
		log.Printf("agent-vm-status sync: update agent %s: %v", agentID, err)
	}
}
