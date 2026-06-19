package graph

// This file will be automatically regenerated based on the schema, any resolver
// implementations will be copied through when generating.

import (
	"context"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/ratelimitpolicy"
	"github.com/VMware-AI/agent-platform-backend/ent/requestlog"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

func intOrZero(p *int) int {
	if p != nil {
		return *p
	}
	return 0
}

// RecordRequestLog ingests a gateway request-log record.
func (r *mutationResolver) RecordRequestLog(ctx context.Context, input model.RecordRequestLogInput) (*model.RequestLog, error) {
	c := r.Ent.RequestLog.Create().
		SetRequestID(input.RequestID).
		SetInputTokens(intOrZero(input.InputTokens)).
		SetOutputTokens(intOrZero(input.OutputTokens)).
		SetLatencyMs(intOrZero(input.LatencyMs)).
		SetStatusCode(input.StatusCode)
	if input.Model != nil {
		c.SetModel(*input.Model)
	}
	if input.UserID != nil {
		if uid, err := uuid.Parse(*input.UserID); err == nil {
			c.SetUserID(uid)
		}
	}
	if input.AgentID != nil {
		if aid, err := uuid.Parse(*input.AgentID); err == nil {
			c.SetAgentID(aid)
		}
	}
	l, err := c.Save(ctx)
	if err != nil {
		return nil, err
	}
	return toModelRequestLog(l), nil
}

// UpsertRateLimitPolicy creates or updates a policy keyed by name.
func (r *mutationResolver) UpsertRateLimitPolicy(ctx context.Context, input model.UpsertRateLimitPolicyInput) (*model.RateLimitPolicy, error) {
	existing, err := r.Ent.RateLimitPolicy.Query().Where(ratelimitpolicy.Name(input.Name)).Only(ctx)
	enabled := false
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	switch {
	case ent.IsNotFound(err):
		c := r.Ent.RateLimitPolicy.Create().SetName(input.Name).SetEnabled(enabled)
		if input.Rpm != nil {
			c.SetRpm(*input.Rpm)
		}
		if input.Tpm != nil {
			c.SetTpm(*input.Tpm)
		}
		p, err := c.Save(ctx)
		if err != nil {
			return nil, err
		}
		r.audit(ctx, "rate_limit.upsert", "rate_limit_policy", p.ID.String(), true, actorID(auth.FromContext(ctx)))
		return toModelRateLimitPolicy(p), nil
	case err != nil:
		return nil, err
	default:
		u := r.Ent.RateLimitPolicy.UpdateOne(existing).SetEnabled(enabled)
		if input.Rpm != nil {
			u.SetRpm(*input.Rpm)
		}
		if input.Tpm != nil {
			u.SetTpm(*input.Tpm)
		}
		p, err := u.Save(ctx)
		if err != nil {
			return nil, err
		}
		r.audit(ctx, "rate_limit.upsert", "rate_limit_policy", p.ID.String(), true, actorID(auth.FromContext(ctx)))
		return toModelRateLimitPolicy(p), nil
	}
}

// SetRateLimitPolicyEnabled toggles a policy.
func (r *mutationResolver) SetRateLimitPolicyEnabled(ctx context.Context, id string, enabled bool) (*model.RateLimitPolicy, error) {
	pid, err := uuid.Parse(id)
	if err != nil {
		return nil, gqlerror.Errorf("invalid id")
	}
	p, err := r.Ent.RateLimitPolicy.UpdateOneID(pid).SetEnabled(enabled).Save(ctx)
	if err != nil {
		return nil, err
	}
	r.audit(ctx, "rate_limit.set_enabled", "rate_limit_policy", id, true, actorID(auth.FromContext(ctx)))
	return toModelRateLimitPolicy(p), nil
}

// RequestLogs lists gateway request logs (optionally by status code).
func (r *queryResolver) RequestLogs(ctx context.Context, statusCode *int, page *model.PageInput) ([]model.RequestLog, error) {
	q := r.Ent.RequestLog.Query()
	if statusCode != nil {
		q = q.Where(requestlog.StatusCode(*statusCode))
	}
	limit, offset := pageBounds(page)
	rows, err := q.Limit(limit).Offset(offset).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.RequestLog, 0, len(rows))
	for _, l := range rows {
		out = append(out, *toModelRequestLog(l))
	}
	return out, nil
}

// RateLimitPolicies lists all policies.
func (r *queryResolver) RateLimitPolicies(ctx context.Context) ([]model.RateLimitPolicy, error) {
	ps, err := r.Ent.RateLimitPolicy.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.RateLimitPolicy, 0, len(ps))
	for _, p := range ps {
		out = append(out, *toModelRateLimitPolicy(p))
	}
	return out, nil
}
