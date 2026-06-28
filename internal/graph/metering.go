package graph

import (
	"context"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/tokenusage"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// monthlyUsageTotals sums input/output tokens and cost over the given TokenUsage
// query, returning zeros when there are no rows (the aggregate scan yields no row
// on an empty table for some drivers).
func (r *Resolver) monthlyUsageTotals(ctx context.Context, q *ent.TokenUsageQuery) (in, out int, cost float64, err error) {
	var agg []struct {
		In   int     `json:"in"`
		Out  int     `json:"out"`
		Cost float64 `json:"cost"`
	}
	if err = q.Clone().Aggregate(
		ent.As(ent.Sum(tokenusage.FieldInputTokens), "in"),
		ent.As(ent.Sum(tokenusage.FieldOutputTokens), "out"),
		ent.As(ent.Sum(tokenusage.FieldCost), "cost"),
	).Scan(ctx, &agg); err != nil {
		return 0, 0, 0, err
	}
	if len(agg) == 1 {
		return agg[0].In, agg[0].Out, agg[0].Cost, nil
	}
	return 0, 0, 0, nil
}

// scopedTokenUsageQuery builds a TokenUsage query confined to the caller's tenant
// (tenant-admin → own tenant; admin → all) and environment (when env_scope is on),
// optionally narrowed to one user. Shared by the metering aggregations so they all
// agree on visibility (LLD-10).
func (r *Resolver) scopedTokenUsageQuery(ctx context.Context, userID *string) (*ent.TokenUsageQuery, error) {
	q := r.Ent.TokenUsage.Query()
	if userID != nil {
		uid, err := uuid.Parse(*userID)
		if err != nil {
			return nil, gqlerror.Errorf("invalid userId")
		}
		q = q.Where(tokenusage.UserID(uid))
	}
	if d := tenantScopeFor(ctx); d.apply {
		if d.denyAll {
			q = q.Where(tokenusage.IDEQ(uuid.Nil))
		} else {
			q = q.Where(tokenusage.TenantID(d.tenant))
		}
	}
	if env, ok := r.envScopeFor(ctx); ok {
		q = q.Where(tokenusage.Or(tokenusage.EnvironmentID(env), tokenusage.EnvironmentIDIsNil()))
	}
	return q, nil
}

// meteringRangeStart maps a MeteringTimeRange to the inclusive lower bound of the
// window, relative to now. LAST_7_DAYS (default) and LAST_30_DAYS are rolling
// day windows; THIS_MONTH starts at the first of the current calendar month.
func meteringRangeStart(rng model.MeteringTimeRange, now time.Time) time.Time {
	switch rng {
	case model.MeteringTimeRangeLast30Days:
		return now.AddDate(0, 0, -30)
	case model.MeteringTimeRangeThisMonth:
		return startOfMonth(now)
	default: // LAST_7_DAYS
		return now.AddDate(0, 0, -7)
	}
}

// startOfMonth returns midnight on the first day of now's calendar month (UTC),
// matching the DB-side UTC day buckets.
func startOfMonth(now time.Time) time.Time {
	y, m, _ := now.UTC().Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}

// agentNamesFor resolves agent display names for a per-agent usage breakdown in one
// batched query (id → name). Rows whose agent no longer exists fall back to the id
// string so the metering table never shows a blank name. The argument is the
// anonymous per-agent aggregation slice; only its AgentID is read.
func (r *Resolver) agentNamesFor(ctx context.Context, rows []struct {
	AgentID string  `json:"agent_id"`
	In      int     `json:"in"`
	Out     int     `json:"out"`
	Cost    float64 `json:"cost"`
	Reqs    int     `json:"reqs"`
}) (map[string]string, error) {
	names := make(map[string]string, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if id, err := uuid.Parse(row.AgentID); err == nil {
			ids = append(ids, id)
		}
		names[row.AgentID] = row.AgentID // fallback to id
	}
	if len(ids) == 0 {
		return names, nil
	}
	ags, err := r.Ent.Agent.Query().Where(agent.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range ags {
		names[a.ID.String()] = a.Name
	}
	return names, nil
}
