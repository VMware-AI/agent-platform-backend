package graph

import (
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// 模块④ 密钥: an agent may hold only ONE non-revoked virtual key (会议 0622 一对一
// 计费). A second issue for the same agent is rejected; revoking frees re-issue.
func TestIssueVirtualKey_OnePerAgent(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	ctx := adminCtx()
	mr := &mutationResolver{r}

	owner := uuid.New()
	agentID := uuid.New()
	aidStr := agentID.String()

	first, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{UserID: owner.String(), AgentID: &aidStr})
	if err != nil {
		t.Fatalf("first issue: %v", err)
	}
	// second issue for the same agent → rejected
	if _, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{UserID: owner.String(), AgentID: &aidStr}); err == nil {
		t.Fatal("second key for the same agent should be rejected")
	}
	// revoke the first → agent is free again
	if _, err := mr.RevokeVirtualKey(ctx, first.VirtualKey.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{UserID: owner.String(), AgentID: &aidStr}); err != nil {
		t.Fatalf("re-issue after revoke should succeed: %v", err)
	}
	// a keyless (no agent) issue is always allowed
	if _, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{UserID: owner.String()}); err != nil {
		t.Fatalf("agent-less issue: %v", err)
	}
}
