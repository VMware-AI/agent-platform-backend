package graph

import (
	"strings"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// TestPlatformSettings_AgentUser pins LLD-13 §3.2: agent_user is a DB platform
// setting (not a startup env). It defaults to "agent", is editable via
// updatePlatformSettings, rejects empty, and — the point of the migration — the
// {{AGENT_USER}} install-command placeholder renders from the DB value.
func TestPlatformSettings_AgentUser(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

	ctx := adminCtx()
	qr := &queryResolver{r}
	mr := &mutationResolver{r}

	// 1) default when unset.
	ps, err := qr.PlatformSettings(ctx)
	if err != nil {
		t.Fatalf("platformSettings: %v", err)
	}
	if ps.AgentUser != "agent" {
		t.Fatalf("default agentUser = %q, want \"agent\"", ps.AgentUser)
	}

	// 2) update sets it; 3) query reflects it.
	want := "appuser"
	updated, err := mr.UpdatePlatformSettings(ctx, model.UpdatePlatformSettingsInput{AgentUser: &want})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.AgentUser != want {
		t.Fatalf("update returned %q, want %q", updated.AgentUser, want)
	}
	if again, _ := qr.PlatformSettings(ctx); again.AgentUser != want {
		t.Fatalf("re-query agentUser = %q, want %q", again.AgentUser, want)
	}

	// 4) blank is rejected (fail fast, no clobber).
	blank := "   "
	if _, err := mr.UpdatePlatformSettings(ctx, model.UpdatePlatformSettingsInput{AgentUser: &blank}); err == nil {
		t.Fatal("blank agentUser must be rejected")
	}
	if still, _ := qr.PlatformSettings(ctx); still.AgentUser != want {
		t.Fatalf("rejected update must not clobber: got %q", still.AgentUser)
	}

	// 5) the {{AGENT_USER}} install-command placeholder renders from the DB value.
	if _, err := r.Ent.AgentTemplate.Create().
		SetKind("test-tpl").
		SetDisplay("Test Template").
		SetInstallCommand("su {{AGENT_USER}} -c /opt/agent/install.sh").
		Save(ctx); err != nil {
		t.Fatalf("create template: %v", err)
	}
	tpls, err := qr.AgentTemplates(ctx)
	if err != nil {
		t.Fatalf("agentTemplates: %v", err)
	}
	var got *model.AgentTemplate
	for i := range tpls {
		if tpls[i].Kind == "test-tpl" {
			got = &tpls[i]
			break
		}
	}
	if got == nil || got.InstallCommand == nil {
		t.Fatal("test template / its install command not found")
	}
	if strings.Contains(*got.InstallCommand, "{{AGENT_USER}}") {
		t.Fatalf("placeholder not rendered: %q", *got.InstallCommand)
	}
	if !strings.Contains(*got.InstallCommand, want) {
		t.Fatalf("install command must use the DB agent_user %q: %q", want, *got.InstallCommand)
	}
}
