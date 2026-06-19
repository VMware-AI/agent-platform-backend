package graph

// This file will be automatically regenerated based on the schema, any resolver
// implementations will be copied through when generating.

import (
	"context"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/deploy"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// DeployAgent provisions the agent's VM: resolve pool credentials → connect
// vCenter → issue a gateway key + inject cloud-init via guestinfo → mark running.
func (r *mutationResolver) DeployAgent(ctx context.Context, input model.DeployAgentInput) (*model.DeployedAgent, error) {
	cu := auth.FromContext(ctx)
	if cu == nil {
		return nil, gqlerror.Errorf("unauthenticated")
	}
	if r.Secrets == nil || r.VCenterConnect == nil || r.Gateway == nil {
		return nil, gqlerror.Errorf("deploy is not configured (gateway/secrets/vcenter required)")
	}

	agentID, err := uuid.Parse(input.AgentID)
	if err != nil {
		return nil, gqlerror.Errorf("invalid agentId")
	}
	ag, err := r.Ent.Agent.Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if ag.OwnerUserID.String() != cu.ID && cu.Role != auth.RoleAdmin {
		return nil, gqlerror.Errorf("forbidden: not your agent")
	}

	poolID, err := uuid.Parse(input.ResourcePoolID)
	if err != nil {
		return nil, gqlerror.Errorf("invalid resourcePoolId")
	}
	pool, err := r.Ent.ResourcePool.Get(ctx, poolID)
	if err != nil {
		return nil, err
	}
	if pool.SecretRef == "" {
		return nil, gqlerror.Errorf("resource pool has no secret_ref")
	}

	cred, err := r.Secrets.Resolve(ctx, pool.SecretRef)
	if err != nil {
		return nil, gqlerror.Errorf("resolve pool credentials: %s", err.Error())
	}
	conn, err := r.VCenterConnect(ctx, pool.Endpoint, cred.Username, cred.Password, true)
	if err != nil {
		r.audit(ctx, "agent.deploy", "agent", ag.ID.String(), false, cu.ID)
		return nil, gqlerror.Errorf("connect vcenter: %s", err.Error())
	}

	svc := &deploy.Service{Gateway: r.Gateway, VCenter: conn, GatewayURL: r.GatewayURL}
	res, err := svc.Provision(ctx, deploy.Request{
		AgentName: ag.Name,
		UserID:    ag.OwnerUserID.String(),
		VMName:    input.VMName,
		Hostname:  derefString(input.Hostname),
		MaxBudget: input.MaxBudget,
	})
	if err != nil {
		r.audit(ctx, "agent.deploy", "agent", ag.ID.String(), false, cu.ID)
		return nil, gqlerror.Errorf("provision: %s", err.Error())
	}

	// Persist the issued key (secret is Sensitive) and mark the agent running.
	vk, err := r.Ent.VirtualKey.Create().
		SetLitellmKey(res.VirtualKey).
		SetUserID(ag.OwnerUserID).
		SetModels([]string{"smart"}).
		SetAlias(ag.Name).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	ag, err = r.Ent.Agent.UpdateOne(ag).
		SetStatus(agent.StatusRunning).
		SetVMRef(input.VMName).
		SetVirtualKeyID(vk.ID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	r.audit(ctx, "agent.deploy", "agent", ag.ID.String(), true, cu.ID)

	return &model.DeployedAgent{
		Agent:            toModelAgent(ag),
		VirtualKeySecret: res.VirtualKey,
	}, nil
}
