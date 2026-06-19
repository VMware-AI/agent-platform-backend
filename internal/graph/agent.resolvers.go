package graph

// This file will be automatically regenerated based on the schema, any resolver
// implementations will be copied through when generating.

import (
	"context"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/agentconfig"
	"github.com/VMware-AI/agent-platform-backend/ent/agenttemplate"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// UpsertAgentTemplate creates or updates a catalog entry keyed by kind.
func (r *mutationResolver) UpsertAgentTemplate(ctx context.Context, input model.UpsertAgentTemplateInput) (*model.AgentTemplate, error) {
	set := func(b interface {
		SetDisplay(string)
		SetInstallMethod(agenttemplate.InstallMethod)
		SetStatus(agenttemplate.Status)
	}) {
		b.SetDisplay(input.Display)
		b.SetInstallMethod(agenttemplate.InstallMethod(input.InstallMethod))
		b.SetStatus(agenttemplate.Status(input.Status))
	}

	existing, err := r.Ent.AgentTemplate.Query().Where(agenttemplate.Kind(input.Kind)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		c := r.Ent.AgentTemplate.Create().SetKind(input.Kind)
		set(c.Mutation())
		applyTemplateOptionals(c.Mutation(), input)
		t, err := c.Save(ctx)
		if err != nil {
			return nil, err
		}
		r.audit(ctx, "agent_template.create", "agent_template", t.ID.String(), true, actorID(auth.FromContext(ctx)))
		return toModelAgentTemplate(t), nil
	case err != nil:
		return nil, err
	default:
		u := r.Ent.AgentTemplate.UpdateOne(existing)
		set(u.Mutation())
		applyTemplateOptionals(u.Mutation(), input)
		t, err := u.Save(ctx)
		if err != nil {
			return nil, err
		}
		r.audit(ctx, "agent_template.update", "agent_template", t.ID.String(), true, actorID(auth.FromContext(ctx)))
		return toModelAgentTemplate(t), nil
	}
}

// applyTemplateOptionals sets the nullable string fields on a template mutation.
func applyTemplateOptionals(m *ent.AgentTemplateMutation, input model.UpsertAgentTemplateInput) {
	if input.Description != nil {
		m.SetDescription(*input.Description)
	}
	if input.InstallCommand != nil {
		m.SetInstallCommand(*input.InstallCommand)
	}
	if input.Version != nil {
		m.SetVersion(*input.Version)
	}
}

// CreateAgent provisions a new agent instance owned by the caller.
func (r *mutationResolver) CreateAgent(ctx context.Context, input model.CreateAgentInput) (*model.Agent, error) {
	cu := auth.FromContext(ctx)
	if cu == nil {
		return nil, gqlerror.Errorf("unauthenticated")
	}
	ownerID, err := uuid.Parse(cu.ID)
	if err != nil {
		return nil, err
	}
	create := r.Ent.Agent.Create().
		SetName(input.Name).
		SetAgentType(input.AgentType).
		SetOwnerUserID(ownerID)
	if input.ConfigID != nil {
		if cid, err := uuid.Parse(*input.ConfigID); err == nil {
			create.SetConfigID(cid)
		}
	}
	if input.ResourcePoolID != nil {
		if pid, err := uuid.Parse(*input.ResourcePoolID); err == nil {
			create.SetResourcePoolID(pid)
		}
	}
	a, err := create.Save(ctx)
	if err != nil {
		return nil, err
	}
	r.audit(ctx, "agent.create", "agent", a.ID.String(), true, cu.ID)
	return toModelAgent(a), nil
}

// SetAgentStatus updates an agent's status (owner or admin only).
func (r *mutationResolver) SetAgentStatus(ctx context.Context, id string, status model.AgentStatus) (*model.Agent, error) {
	cu := auth.FromContext(ctx)
	if cu == nil {
		return nil, gqlerror.Errorf("unauthenticated")
	}
	aid, err := uuid.Parse(id)
	if err != nil {
		return nil, gqlerror.Errorf("invalid id")
	}
	a, err := r.Ent.Agent.Get(ctx, aid)
	if err != nil {
		return nil, err
	}
	if a.OwnerUserID.String() != cu.ID && cu.Role != auth.RoleAdmin {
		return nil, gqlerror.Errorf("forbidden: not your agent")
	}
	a, err = r.Ent.Agent.UpdateOne(a).SetStatus(agent.Status(status)).Save(ctx)
	if err != nil {
		return nil, err
	}
	r.audit(ctx, "agent.set_status", "agent", a.ID.String(), true, cu.ID)
	return toModelAgent(a), nil
}

// AgentTemplates lists the catalog.
func (r *queryResolver) AgentTemplates(ctx context.Context) ([]model.AgentTemplate, error) {
	ts, err := r.Ent.AgentTemplate.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.AgentTemplate, 0, len(ts))
	for _, t := range ts {
		out = append(out, *toModelAgentTemplate(t))
	}
	return out, nil
}

// AgentConfigs lists configs, optionally filtered by agent type.
func (r *queryResolver) AgentConfigs(ctx context.Context, agentType *string) ([]model.AgentConfig, error) {
	q := r.Ent.AgentConfig.Query()
	if agentType != nil {
		q = q.Where(agentconfig.AgentType(*agentType))
	}
	cs, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.AgentConfig, 0, len(cs))
	for _, c := range cs {
		out = append(out, *toModelAgentConfig(c))
	}
	return out, nil
}

// Agents lists agents (admin: all; user: own — owner scope, LLD-01 §4.1).
func (r *queryResolver) Agents(ctx context.Context) ([]model.Agent, error) {
	cu := auth.FromContext(ctx)
	if cu == nil {
		return nil, gqlerror.Errorf("unauthenticated")
	}
	q := r.Ent.Agent.Query()
	if cu.Role != auth.RoleAdmin {
		if uid, err := uuid.Parse(cu.ID); err == nil {
			q = q.Where(agent.OwnerUserID(uid))
		}
	}
	as, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Agent, 0, len(as))
	for _, a := range as {
		out = append(out, *toModelAgent(a))
	}
	return out, nil
}
