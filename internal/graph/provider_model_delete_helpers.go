package graph

import (
	"context"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/google/uuid"
)

func (r *mutationResolver) deleteProviderModelCascade(ctx context.Context, pmID uuid.UUID) error {
	err := r.Ent.ProviderModel.DeleteOneID(pmID).Exec(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	return nil
}
