package graph

import (
	"entgo.io/ent/dialect/sql"
	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/resourcepool"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// orderNewest / orderByKey give list queries a stable TOTAL sort so Limit/Offset
// pagination is deterministic (no duplicate/dropped rows across pages) and logs
// read newest-first. The id tiebreaker matters: created_at alone is not unique
// (rapid inserts share a timestamp), so without it pages could overlap. orderByKey
// is for entities without created_at (Permission); key is unique.
var (
	orderNewest = ent.Desc("created_at", "id")
	orderByKey  = ent.Asc("key")
)

// pageBounds normalizes a PageInput into a safe limit/offset.
func pageBounds(page *model.PageInput) (limit, offset int) {
	limit, offset = 50, 0
	if page != nil {
		if page.Limit != nil {
			limit = *page.Limit
		}
		if page.Offset != nil {
			offset = *page.Offset
		}
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// clampLimit normalizes an optional list-limit into [1, max], defaulting to def.
func clampLimit(p *int, def, max int) int {
	n := def
	if p != nil {
		n = *p
	}
	if n < 1 {
		n = def
	}
	if n > max {
		n = max
	}
	return n
}

// applyResourcePoolSort orders a pool query by the console's sort field, with a
// stable id tiebreak. SYNC_STATUS sorts on the ent status column (proxy: never-
// synced rows have status=disconnected, errored sync rows have status=error;
// the console derives SYNCED/SYNCING/FAILED/NEVER from status+last_synced_at).
// The default (CREATED_AT) and any unmapped field fall back to created_at.
func applyResourcePoolSort(q *ent.ResourcePoolQuery, sort *model.ResourcePoolSort) *ent.ResourcePoolQuery {
	if sort == nil {
		return q.Order(ent.Desc(resourcepool.FieldCreatedAt))
	}
	desc := sort.Direction == model.SortDirectionDesc
	col := ""
	switch sort.Field {
	case model.ResourcePoolSortFieldName:
		col = resourcepool.FieldName
	case model.ResourcePoolSortFieldEndpoint:
		col = resourcepool.FieldEndpoint
	case model.ResourcePoolSortFieldSyncStatus:
		col = resourcepool.FieldStatus
	case model.ResourcePoolSortFieldUpdatedAt:
		col = resourcepool.FieldUpdatedAt
	default: // CREATED_AT
		col = resourcepool.FieldCreatedAt
	}
	if desc {
		return q.Order(ent.Desc(col), ent.Desc(resourcepool.FieldID))
	}
	return q.Order(ent.Asc(col), ent.Asc(resourcepool.FieldID))
}

// applyAgentSort orders the agent query per the requested field (前后端整合契约),
// with id as a stable tiebreaker for deterministic pagination. Native columns sort
// in SQL; OWNER / API_KEY_NAME sort via a LEFT JOIN on the related display column.
func applyAgentSort(q *ent.AgentQuery, sort *model.AgentSort) *ent.AgentQuery {
	if sort == nil {
		return q.Order(orderNewest)
	}
	desc := sort.Direction == model.SortDirectionDesc
	col := ""
	switch sort.Field {
	case model.AgentSortFieldName:
		col = agent.FieldName
	case model.AgentSortFieldType:
		col = agent.FieldAgentType
	case model.AgentSortFieldStatus:
		col = agent.FieldStatus
	case model.AgentSortFieldCreatedAt:
		col = agent.FieldCreatedAt
	case model.AgentSortFieldUpdatedAt:
		col = agent.FieldUpdatedAt
	case model.AgentSortFieldOwner:
		return q.Order(joinOrder(user.Table, agent.FieldOwnerUserID, user.FieldUsername, desc))
	case model.AgentSortFieldAPIKeyName:
		return q.Order(joinOrder(virtualkey.Table, agent.FieldVirtualKeyID, virtualkey.FieldName, desc))
	default:
		return q.Order(orderNewest)
	}
	if desc {
		return q.Order(ent.Desc(col), ent.Desc(agent.FieldID))
	}
	return q.Order(ent.Asc(col), ent.Asc(agent.FieldID))
}

// applyUserSort orders the user query per the requested field (模块① 用户与权限),
// id as a stable tiebreaker. All columns are native to users — no joins needed.
func applyUserSort(q *ent.UserQuery, sort *model.UserSort) *ent.UserQuery {
	if sort == nil {
		return q.Order(orderNewest)
	}
	desc := sort.Direction == model.SortDirectionDesc
	col := ""
	switch sort.Field {
	case model.UserSortFieldUsername:
		col = user.FieldUsername
	case model.UserSortFieldEmail:
		col = user.FieldEmail
	case model.UserSortFieldRole:
		col = user.FieldRole
	case model.UserSortFieldCreatedAt:
		col = user.FieldCreatedAt
	case model.UserSortFieldUpdatedAt:
		col = user.FieldUpdatedAt
	case model.UserSortFieldLastLogin:
		col = user.FieldLastLoginAt
	default:
		// CONNECTION (online status) is derived, not a column — fall back to newest.
		return q.Order(orderNewest)
	}
	if desc {
		return q.Order(ent.Desc(col), ent.Desc(user.FieldID))
	}
	return q.Order(ent.Asc(col), ent.Asc(user.FieldID))
}

// joinOrder orders agents by a column on a related table (owner username / key
// alias). LEFT JOIN keeps agents with no owner/key; id is the stable tiebreaker.
func joinOrder(table, fk, col string, desc bool) func(*sql.Selector) {
	return func(s *sql.Selector) {
		t := sql.Table(table)
		s.LeftJoin(t).On(s.C(fk), t.C("id"))
		if desc {
			s.OrderBy(sql.Desc(t.C(col)), sql.Desc(s.C(agent.FieldID)))
		} else {
			s.OrderBy(sql.Asc(t.C(col)), sql.Asc(s.C(agent.FieldID)))
		}
	}
}
