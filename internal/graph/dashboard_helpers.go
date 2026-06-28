package graph

// Helpers for DashboardOverview. These live OUTSIDE *.resolvers.go on purpose:
// gqlgen rewrites resolver files and comments out any non-resolver method it finds
// there, which would break the build on the next `make generate`.

import (
	"context"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/tokenusage"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

const (
	defaultDashboardLimit = 5
	maxDashboardLimit     = 50
)

// dashboardStats counts the platform entities the overview cards show + the
// current calendar month's usage totals.
func (r *Resolver) dashboardStats(ctx context.Context) (*model.DashboardStats, error) {
	s := &model.DashboardStats{}
	var err error
	if s.TotalAgents, err = r.Ent.Agent.Query().Count(ctx); err != nil {
		return nil, err
	}
	if s.RunningAgents, err = r.Ent.Agent.Query().Where(agent.StatusEQ(agent.StatusRunning)).Count(ctx); err != nil {
		return nil, err
	}
	if s.StoppedAgents, err = r.Ent.Agent.Query().Where(agent.StatusEQ(agent.StatusStopped)).Count(ctx); err != nil {
		return nil, err
	}
	if s.ExceptionAgents, err = r.Ent.Agent.Query().Where(agent.StatusEQ(agent.StatusException)).Count(ctx); err != nil {
		return nil, err
	}
	if s.TotalVirtualKeys, err = r.Ent.VirtualKey.Query().Count(ctx); err != nil {
		return nil, err
	}
	if s.TotalGateways, err = r.Ent.GatewayConnection.Query().Count(ctx); err != nil {
		return nil, err
	}
	if s.TotalResourcePools, err = r.Ent.ResourcePool.Query().Count(ctx); err != nil {
		return nil, err
	}
	if s.TotalUsers, err = r.Ent.User.Query().Count(ctx); err != nil {
		return nil, err
	}

	// Current-month usage (本月): row count = calls, summed tokens + cost.
	monthStart := startOfMonth(time.Now())
	monthQ := r.Ent.TokenUsage.Query().Where(tokenusage.CreatedAtGTE(monthStart))
	if s.MonthlyCalls, err = monthQ.Clone().Count(ctx); err != nil {
		return nil, err
	}
	in, out, cost, err := r.monthlyUsageTotals(ctx, monthQ)
	if err != nil {
		return nil, err
	}
	s.MonthlyTokens = in + out
	s.MonthlyCost = cost
	return s, nil
}

// dashboardRecentAgents returns the newest agents for the 最近创建的实例 table,
// projecting Agent.status onto the console's 3-state badge (provisioning shows as
// stopped — it is not yet running).
func (r *Resolver) dashboardRecentAgents(ctx context.Context, limit int) ([]model.DashboardRecentAgent, error) {
	ags, err := r.Ent.Agent.Query().Order(orderNewest).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.DashboardRecentAgent, 0, len(ags))
	for _, a := range ags {
		out = append(out, model.DashboardRecentAgent{
			ID:        a.ID.String(),
			Name:      a.Name,
			AgentName: a.AgentType,
			Status:    dashboardAgentStatus(a.Status),
			CreatedAt: a.CreatedAt,
		})
	}
	return out, nil
}

// dashboardNotices derives the 系统通知 list from the most recent audit logs (the
// most real notice source available): each notice's severity comes from the log's
// success/fail result, and the text from a friendly rendering of the action +
// resource. When there is no audit history yet, the list is empty (not faked).
func (r *Resolver) dashboardNotices(ctx context.Context, limit int) ([]model.DashboardNotice, error) {
	logs, err := r.Ent.AuditLog.Query().Order(orderNewest).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.DashboardNotice, 0, len(logs))
	for _, l := range logs {
		status := model.DashboardNoticeStatusSuccess
		if string(l.Result) == "fail" {
			status = model.DashboardNoticeStatusDanger
		}
		out = append(out, model.DashboardNotice{
			ID:         l.ID.String(),
			Text:       noticeText(l.Action, l.ResourceType, status),
			Status:     status,
			OccurredAt: l.CreatedAt,
		})
	}
	return out, nil
}
