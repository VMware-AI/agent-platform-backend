package graph

import (
	"context"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/google/uuid"
)

// resolveActorNames batch-loads usernames for the distinct actor ids on a page
// of audit rows (actor_user_id is a soft ref with no FK edge). Missing/deleted
// users don't appear, so the caller leaves ActorName nil (console falls back to
// the short id). Lives here, not in schema.resolvers.go, so gqlgen doesn't sweep
// it into its "helpers should move out" warning block on regeneration.
func (r *queryResolver) resolveActorNames(ctx context.Context, logs []*ent.AuditLog) map[uuid.UUID]string {
	ids := make([]uuid.UUID, 0, len(logs))
	seen := map[uuid.UUID]struct{}{}
	for _, l := range logs {
		if l.ActorUserID == nil {
			continue
		}
		if _, ok := seen[*l.ActorUserID]; ok {
			continue
		}
		seen[*l.ActorUserID] = struct{}{}
		ids = append(ids, *l.ActorUserID)
	}
	if len(ids) == 0 {
		return nil
	}
	users, err := r.Ent.User.Query().Where(user.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil
	}
	names := make(map[uuid.UUID]string, len(users))
	for _, u := range users {
		names[u.ID] = u.Username
	}
	return names
}
