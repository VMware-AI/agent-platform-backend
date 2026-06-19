package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

func TestAgentConfigCRUD_DefaultToggle(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	yes := true
	c1, err := mr.CreateAgentConfig(ctx, model.CreateAgentConfigInput{Name: "goose-base", AgentType: "goose", IsDefault: &yes})
	if err != nil || !c1.IsDefault {
		t.Fatalf("create c1: %v default=%v", err, c1)
	}
	// second default for the same type must unset the first
	if _, err := mr.CreateAgentConfig(ctx, model.CreateAgentConfigInput{Name: "goose-v2", AgentType: "goose", IsDefault: &yes}); err != nil {
		t.Fatalf("create c2: %v", err)
	}

	gooseType := "goose"
	configs, _ := qr.AgentConfigs(ctx, &gooseType)
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}
	defaults := 0
	for _, c := range configs {
		if c.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("expected exactly 1 default, got %d", defaults)
	}

	// update name
	newName := "goose-base-renamed"
	upd, err := mr.UpdateAgentConfig(ctx, c1.ID, model.UpdateAgentConfigInput{Name: &newName})
	if err != nil || upd.Name != newName {
		t.Fatalf("update: %v name=%s", err, upd.Name)
	}

	// re-set default to c1
	def, err := mr.SetDefaultAgentConfig(ctx, c1.ID)
	if err != nil || !def.IsDefault {
		t.Fatalf("setDefault: %v", err)
	}

	// delete and verify
	if ok, err := mr.DeleteAgentConfig(ctx, upd.ID); err != nil || !ok {
		t.Fatalf("delete: %v", err)
	}
	left, _ := qr.AgentConfigs(ctx, &gooseType)
	if len(left) != 1 {
		t.Fatalf("after delete expected 1, got %d", len(left))
	}
}
