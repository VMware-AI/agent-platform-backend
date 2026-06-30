package catalog

import (
	"context"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/agenttemplate"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

func TestSeed_PopulatesActiveCatalog(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, "", true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	if err := Seed(ctx, db); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	all, err := db.AgentTemplate.Query().All(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 seeded active templates (goose/xiaoguai/qoder), got %d", len(all))
	}
	for _, kind := range []string{"goose", "xiaoguai", "qoder"} {
		got, err := db.AgentTemplate.Query().Where(agenttemplate.Kind(kind)).Only(ctx)
		if err != nil {
			t.Fatalf("missing seeded kind %q: %v", kind, err)
		}
		if got.Status != agenttemplate.StatusActive {
			t.Errorf("%s should be active, got %s", kind, got.Status)
		}
		if got.Display == "" {
			t.Errorf("%s missing display", kind)
		}
	}
	// qoder is the online (curl) one; goose/xiaoguai are offline_tar.
	q, _ := db.AgentTemplate.Query().Where(agenttemplate.Kind("qoder")).Only(ctx)
	if q.InstallMethod != agenttemplate.InstallMethodCurl {
		t.Errorf("qoder install_method = %s, want curl", q.InstallMethod)
	}
	g, _ := db.AgentTemplate.Query().Where(agenttemplate.Kind("goose")).Only(ctx)
	if g.InstallMethod != agenttemplate.InstallMethodOfflineTar {
		t.Errorf("goose install_method = %s, want offline_tar", g.InstallMethod)
	}

	// LLD-11 K4: each active kind seeds its OKF grounding convention — an unpack
	// root and a "consult local knowledge index.md first" (非 RAG) prompt.
	for _, kind := range []string{"goose", "xiaoguai", "qoder"} {
		got, _ := db.AgentTemplate.Query().Where(agenttemplate.Kind(kind)).Only(ctx)
		if got.KnowledgeRoot != DefaultKnowledgeRoot {
			t.Errorf("%s knowledge_root = %q, want %q", kind, got.KnowledgeRoot, DefaultKnowledgeRoot)
		}
		if !strings.Contains(got.KnowledgePrompt, "index.md") {
			t.Errorf("%s knowledge_prompt should point at index.md, got %q", kind, got.KnowledgePrompt)
		}
	}
}

func TestSeed_Idempotent(t *testing.T) {
	ctx := context.Background()
	db, _ := store.Open(ctx, "", true)
	defer db.Close()

	if err := Seed(ctx, db); err != nil {
		t.Fatalf("Seed 1: %v", err)
	}
	if err := Seed(ctx, db); err != nil {
		t.Fatalf("Seed 2: %v", err)
	}
	n, _ := db.AgentTemplate.Query().Count(ctx)
	if n != 3 {
		t.Fatalf("re-seeding must not duplicate; want 3, got %d", n)
	}
}

// Seeding must not clobber an operator's edits to an existing entry.
func TestSeed_PreservesOperatorEdits(t *testing.T) {
	ctx := context.Background()
	db, _ := store.Open(ctx, "", true)
	defer db.Close()

	// Operator already customized goose before first seed.
	_, err := db.AgentTemplate.Create().
		SetKind("goose").SetDisplay("Goose (custom)").
		SetInstallMethod(agenttemplate.InstallMethodCurl).
		SetStatus(agenttemplate.StatusDeferred).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed operator goose: %v", err)
	}

	if err := Seed(ctx, db); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	got, _ := db.AgentTemplate.Query().Where(agenttemplate.Kind("goose")).Only(ctx)
	if got.Display != "Goose (custom)" || got.Status != agenttemplate.StatusDeferred {
		t.Errorf("seed clobbered operator edits: %+v", got)
	}
}

// Air-gap (#75): no built-in install command may hardcode an external host. Every
// package/script fetch must go through the {{AGENT_PKG_BASE_URL}} mirror so an
// offline deployment points at an internal mirror with one knob. goose/xiaoguai
// already complied; this pins qoder (the former online-only holdout) and guards
// any future entry from re-introducing a hardcoded URL.
func TestBuiltins_FetchOnlyViaMirrorPlaceholder(t *testing.T) {
	for _, e := range builtins {
		if strings.Contains(e.installCommand, "http://") || strings.Contains(e.installCommand, "https://") {
			t.Errorf("%s install_command hardcodes an external URL (air-gap regression): %q", e.kind, e.installCommand)
		}
		// A scheme-less host (e.g. `curl example.com/x`) would slip past the check
		// above, so also require the mirror placeholder whenever the command fetches.
		if strings.Contains(e.installCommand, "curl") && !strings.Contains(e.installCommand, "{{AGENT_PKG_BASE_URL}}") {
			t.Errorf("%s fetches via curl but not through the {{AGENT_PKG_BASE_URL}} mirror: %q", e.kind, e.installCommand)
		}
	}
}
