package graph

import (
	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// applyUpstreamOptionals sets nullable fields on an upstream mutation, shared by
// the create and update arms of UpsertUpstream (mirrors applyTemplateOptionals).
func applyUpstreamOptionals(m *ent.UpstreamMutation, input model.UpsertUpstreamInput) {
	if input.APIBase != nil {
		m.SetAPIBase(*input.APIBase)
	}
	if input.APIKeyRef != nil {
		m.SetAPIKeyRef(*input.APIKeyRef)
	}
}
