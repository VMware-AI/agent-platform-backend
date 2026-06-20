package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

func TestIssueVirtualKey_AppliesPolicyAndBindsAgent(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	ctx := context.Background()
	mr := &mutationResolver{r}

	rpm, tpm := 60, 100000
	pol, err := mr.UpsertRateLimitPolicy(ctx, model.UpsertRateLimitPolicyInput{Name: "std", Rpm: &rpm, Tpm: &tpm})
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	u, _ := mr.CreateUser(ctx, model.CreateUserInput{Username: "ku", Email: "ku@x.io", Password: "KuPass1234567", Role: model.RoleUser})
	ownerCtx := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: u.ID, Role: auth.RoleUser})
	seedActiveTemplate(t, mr.Resolver, "goose")
	ag, _ := mr.CreateAgent(ownerCtx, model.CreateAgentInput{Name: "a", AgentType: "goose"})

	issued, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{
		UserID: u.ID, AgentID: &ag.ID, RateLimitPolicyID: &pol.ID, Models: []string{"smart"},
	})
	if err != nil {
		t.Fatalf("IssueVirtualKey: %v", err)
	}

	// the policy's rpm/tpm were applied to the litellm key request (真生效)
	last := fg.generated[len(fg.generated)-1]
	if last.RPMLimit == nil || *last.RPMLimit != 60 || last.TPMLimit == nil || *last.TPMLimit != 100000 {
		t.Fatalf("policy rpm/tpm not applied to gateway key: rpm=%v tpm=%v", last.RPMLimit, last.TPMLimit)
	}
	// the key is bound to the agent + policy
	if issued.VirtualKey.AgentID == nil || *issued.VirtualKey.AgentID != ag.ID {
		t.Fatalf("agent not bound: %+v", issued.VirtualKey.AgentID)
	}
	if issued.VirtualKey.RateLimitPolicyID == nil || *issued.VirtualKey.RateLimitPolicyID != pol.ID {
		t.Fatalf("policy not bound: %+v", issued.VirtualKey.RateLimitPolicyID)
	}

	// enable/disable toggle (distinct from revoke)
	vk, err := mr.SetVirtualKeyEnabled(ctx, issued.VirtualKey.ID, false)
	if err != nil || vk.Status != model.VirtualKeyStatusDisabled {
		t.Fatalf("disable failed: status=%v err=%v", vk.Status, err)
	}
	vk, err = mr.SetVirtualKeyEnabled(ctx, issued.VirtualKey.ID, true)
	if err != nil || vk.Status != model.VirtualKeyStatusActive {
		t.Fatalf("re-enable failed: status=%v err=%v", vk.Status, err)
	}

	// after revoke, cannot re-enable
	if _, err := mr.RevokeVirtualKey(ctx, issued.VirtualKey.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := mr.SetVirtualKeyEnabled(ctx, issued.VirtualKey.ID, true); err == nil {
		t.Fatal("revoked key must not be re-enabled")
	}
}
