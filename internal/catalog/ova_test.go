package catalog

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/ovatemplatefamily"
	"github.com/VMware-AI/agent-platform-backend/ent/ovatemplateversion"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

func TestRepairOvaTemplates_FixesOpenCodeTemplateIdentifier(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, "", true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	fam, err := db.OvaTemplateFamily.Create().
		SetName("opencode").
		SetType("OPENCODE").
		SetDescription("old opencode template").
		SetIconShape("pop-out").
		Save(ctx)
	if err != nil {
		t.Fatalf("create family: %v", err)
	}
	if _, err := db.OvaTemplateVersion.Create().
		SetFamily(fam).
		SetVersion("v1.0.0").
		SetOvaIdentifier("builder-hermes-temp-v7").
		Save(ctx); err != nil {
		t.Fatalf("create version: %v", err)
	}

	if err := RepairOvaTemplates(ctx, db); err != nil {
		t.Fatalf("RepairOvaTemplates: %v", err)
	}

	got, err := db.OvaTemplateVersion.Query().
		Where(ovatemplateversion.HasFamilyWith(ovatemplatefamily.Type("OPENCODE"))).
		Only(ctx)
	if err != nil {
		t.Fatalf("query repaired version: %v", err)
	}
	if got.OvaIdentifier != DefaultOpenCodeOvaIdentifier {
		t.Fatalf("ova_identifier = %q, want %q", got.OvaIdentifier, DefaultOpenCodeOvaIdentifier)
	}
}

func TestRepairOvaTemplates_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, "", true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	fam, err := db.OvaTemplateFamily.Create().
		SetName("opencode").
		SetType("OPENCODE").
		SetDescription("correct opencode template").
		SetIconShape("pop-out").
		Save(ctx)
	if err != nil {
		t.Fatalf("create family: %v", err)
	}
	if _, err := db.OvaTemplateVersion.Create().
		SetFamily(fam).
		SetVersion("v1.0.0").
		SetOvaIdentifier(DefaultOpenCodeOvaIdentifier).
		Save(ctx); err != nil {
		t.Fatalf("create version: %v", err)
	}

	if err := RepairOvaTemplates(ctx, db); err != nil {
		t.Fatalf("RepairOvaTemplates 1: %v", err)
	}
	if err := RepairOvaTemplates(ctx, db); err != nil {
		t.Fatalf("RepairOvaTemplates 2: %v", err)
	}

	got, err := db.OvaTemplateVersion.Query().
		Where(ovatemplateversion.HasFamilyWith(ovatemplatefamily.Type("OPENCODE"))).
		Only(ctx)
	if err != nil {
		t.Fatalf("query repaired version: %v", err)
	}
	if got.OvaIdentifier != DefaultOpenCodeOvaIdentifier {
		t.Fatalf("ova_identifier = %q, want %q", got.OvaIdentifier, DefaultOpenCodeOvaIdentifier)
	}
}

func TestRepairOvaTemplates_NoOpWhenOpenCodeFamilyMissing(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, "", true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	if err := RepairOvaTemplates(ctx, db); err != nil {
		t.Fatalf("RepairOvaTemplates: %v", err)
	}
}
