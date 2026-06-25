package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// TestOvaTemplateCatalog exercises the Block 6a catalog end-to-end: create a
// family with its initial version, append a second version, then list/filter/sort
// the families and versions through the resolvers (console 智能体市场).
func TestOvaTemplateCatalog(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	famR := &ovaTemplateFamilyResolver{r}

	// --- createOvaTemplateFamily creates the family + its initial version in one tx.
	created, err := mr.CreateOvaTemplateFamily(ctx, model.CreateOvaTemplateFamilyInput{
		Name:        "Goose Agent",
		Type:        "goose",
		Description: "A general coding agent",
		Tools:       []string{"shell", "editor"},
		Scenarios:   []string{"coding"},
		Skills:      []string{"refactor"},
		IconShape:   "hexagon",
		IconColor:   model.OvaTemplateColorBlue,
		InitialVersion: &model.CreateOvaTemplateVersionInput{
			Version:       "1.0.0",
			OvaIdentifier: "goose-1.0.0.ova",
			Notes:         ptr("first cut"),
		},
	})
	if err != nil {
		t.Fatalf("CreateOvaTemplateFamily: %v", err)
	}
	fam := created.Family
	if fam.Name != "Goose Agent" || fam.Type != "goose" {
		t.Fatalf("family fields wrong: %+v", fam)
	}
	if fam.IconColor != model.OvaTemplateColorBlue || fam.IconShape != "hexagon" {
		t.Fatalf("icon fields wrong: %+v", fam)
	}
	if len(fam.Tools) != 2 || fam.Tools[0] != "shell" {
		t.Fatalf("tools wrong: %v", fam.Tools)
	}

	// latestVersion + versions field resolvers reflect the initial version.
	lv, err := famR.LatestVersion(ctx, fam)
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if lv == nil || *lv != "1.0.0" {
		t.Fatalf("latestVersion = %v, want 1.0.0", lv)
	}
	vers, err := famR.Versions(ctx, fam)
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(vers) != 1 || vers[0].Version != "1.0.0" {
		t.Fatalf("versions = %+v", vers)
	}
	if vers[0].FamilyID != fam.ID {
		t.Fatalf("version familyId = %q, want %q", vers[0].FamilyID, fam.ID)
	}
	if vers[0].Notes == nil || *vers[0].Notes != "first cut" {
		t.Fatalf("version notes = %v", vers[0].Notes)
	}

	// --- addOvaTemplateVersion appends a newer version (newest-first ordering).
	added, err := mr.AddOvaTemplateVersion(ctx, model.AddOvaTemplateVersionInput{
		FamilyID:      fam.ID,
		Version:       "1.1.0",
		OvaIdentifier: "goose-1.1.0.ova",
	})
	if err != nil {
		t.Fatalf("AddOvaTemplateVersion: %v", err)
	}
	if added.Version.Version != "1.1.0" || added.Version.FamilyID != fam.ID {
		t.Fatalf("added version wrong: %+v", added.Version)
	}

	lv, err = famR.LatestVersion(ctx, fam)
	if err != nil {
		t.Fatalf("LatestVersion after add: %v", err)
	}
	if lv == nil || *lv != "1.1.0" {
		t.Fatalf("latestVersion after add = %v, want 1.1.0", lv)
	}

	// adding to a missing family must error (no silent create).
	if _, err := mr.AddOvaTemplateVersion(ctx, model.AddOvaTemplateVersionInput{
		FamilyID:      "00000000-0000-0000-0000-000000000099",
		Version:       "9.9.9",
		OvaIdentifier: "ghost.ova",
	}); err == nil {
		t.Fatal("expected error adding version to missing family")
	}

	// --- a second family so filter/sort have something to discriminate.
	if _, err := mr.CreateOvaTemplateFamily(ctx, model.CreateOvaTemplateFamilyInput{
		Name:        "Xiaoguai Agent",
		Type:        "xiaoguai",
		Description: "MCP-first agent",
		Tools:       []string{"mcp"},
		Scenarios:   []string{"ops"},
		Skills:      []string{},
		IconShape:   "circle",
		IconColor:   model.OvaTemplateColorPurple,
		InitialVersion: &model.CreateOvaTemplateVersionInput{
			Version:       "0.1.0",
			OvaIdentifier: "xiaoguai-0.1.0.ova",
		},
	}); err != nil {
		t.Fatalf("CreateOvaTemplateFamily (2nd): %v", err)
	}

	// --- ovaTemplateFamilies: full list, sorted by name ascending.
	asc := &model.OvaTemplateFamilySort{Field: model.OvaTemplateFamilySortFieldOvaName, Direction: model.SortDirectionAsc}
	all, err := qr.OvaTemplateFamilies(ctx, nil, nil, asc)
	if err != nil {
		t.Fatalf("OvaTemplateFamilies: %v", err)
	}
	if all.TotalCount != 2 || len(all.Nodes) != 2 {
		t.Fatalf("expected 2 families, got total=%d nodes=%d", all.TotalCount, len(all.Nodes))
	}
	if all.Nodes[0].Name != "Goose Agent" || all.Nodes[1].Name != "Xiaoguai Agent" {
		t.Fatalf("name-asc order wrong: %q, %q", all.Nodes[0].Name, all.Nodes[1].Name)
	}
	if all.PageInfo.Page != 1 || all.PageInfo.TotalPages != 1 {
		t.Fatalf("pageInfo wrong: %+v", all.PageInfo)
	}

	// filter by nameKeyword.
	byName, err := qr.OvaTemplateFamilies(ctx, &model.OvaTemplateFamilyFilter{NameKeyword: ptr("xiao")}, nil, nil)
	if err != nil {
		t.Fatalf("OvaTemplateFamilies(name filter): %v", err)
	}
	if byName.TotalCount != 1 || byName.Nodes[0].Type != "xiaoguai" {
		t.Fatalf("name filter wrong: %+v", byName)
	}

	// filter by type.
	byType, err := qr.OvaTemplateFamilies(ctx, &model.OvaTemplateFamilyFilter{Type: ptr("goose")}, nil, nil)
	if err != nil {
		t.Fatalf("OvaTemplateFamilies(type filter): %v", err)
	}
	if byType.TotalCount != 1 || byType.Nodes[0].Name != "Goose Agent" {
		t.Fatalf("type filter wrong: %+v", byType)
	}

	// --- ovaTemplateVersions scoped to the goose family, newest-first.
	scoped, err := qr.OvaTemplateVersions(ctx, ptr(fam.ID), nil)
	if err != nil {
		t.Fatalf("OvaTemplateVersions(scoped): %v", err)
	}
	if scoped.TotalCount != 2 || len(scoped.Nodes) != 2 {
		t.Fatalf("expected 2 versions for goose, got %d", scoped.TotalCount)
	}
	if scoped.Nodes[0].Version != "1.1.0" || scoped.Nodes[1].Version != "1.0.0" {
		t.Fatalf("versions not newest-first: %q, %q", scoped.Nodes[0].Version, scoped.Nodes[1].Version)
	}
	if scoped.Nodes[0].FamilyID != fam.ID {
		t.Fatalf("scoped version familyId = %q, want %q", scoped.Nodes[0].FamilyID, fam.ID)
	}

	// unscoped versions span both families (1 goose initial + 1 added + 1 xiaoguai).
	unscoped, err := qr.OvaTemplateVersions(ctx, nil, nil)
	if err != nil {
		t.Fatalf("OvaTemplateVersions(unscoped): %v", err)
	}
	if unscoped.TotalCount != 3 {
		t.Fatalf("expected 3 versions total, got %d", unscoped.TotalCount)
	}
}
