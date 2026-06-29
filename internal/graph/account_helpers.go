package graph

import (
	"context"
	"log"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
)

// revokeUserKeys best-effort revokes all of a user's non-revoked virtual keys at
// their (per-department) gateways and marks the rows revoked. A missing gateway or
// a gateway failure is logged + audited as an orphan rather than aborting — the
// caller (DeleteUser) must still proceed. Mirrors RecycleAgent's non-silent
// compensation so a leaked billable key is at least observable.
//
// Lives in a non-resolver file (not account.resolvers.go): gqlgen regen treats
// non-resolver methods in *.resolvers.go as "unknown code" and relocates them into
// a commented dead block (breaking the build). Shared helpers must stay out of
// *.resolvers.go files.
func (r *mutationResolver) revokeUserKeys(ctx context.Context, uid uuid.UUID) {
	keys, err := r.Ent.VirtualKey.Query().
		Where(virtualkey.UserID(uid), virtualkey.StatusNEQ(virtualkey.StatusRevoked)).
		All(ctx)
	if err != nil {
		log.Printf("delete user %s: list virtual keys failed: %v", uid, err)
		return
	}
	actor := actorID(auth.FromContext(ctx))
	for _, vk := range keys {
		gw := r.gatewayKeyClientForVK(ctx, vk)
		if gw == nil {
			log.Printf("delete user %s: no gateway to revoke key %s (orphan)", uid, vk.ID)
			r.audit(ctx, "key.revoke", "virtual_key", vk.ID.String(), false, actor)
			continue
		}
		if err := gw.DeleteKey(ctx, vk.LitellmKey); err != nil {
			log.Printf("delete user %s: orphan gateway key %s, revoke failed: %v", uid, vk.ID, err)
			r.audit(ctx, "key.revoke", "virtual_key", vk.ID.String(), false, actor)
			continue
		}
		_, _ = r.Ent.VirtualKey.UpdateOne(vk).SetStatus(virtualkey.StatusRevoked).Save(ctx)
	}
}
