package catalog

import (
	"context"
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
