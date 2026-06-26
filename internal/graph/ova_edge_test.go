package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// This file holds OVA template family/version resolver EDGE-CASE coverage,
// complementing the happy-path TestOvaTemplateCatalog in ova_test.go. Every
// helper/var here carries an `Edge`/`edge` suffix to avoid colliding with the
// shared test tree. It reuses the existing harness (newTestResolver / adminCtx /
// ptr) rather than introducing new seed plumbing.

// mkFamilyEdge creates a family with a single initial version through the
// mutation resolver and returns the created family model. Centralizes the
// verbose input so the edge cases below stay focused on assertions.
func mkFamilyEdge(t *testing.T, mr *mutationResolver, name, typ, initialVersion string) *model.OvaTemplateFamily {
	t.Helper()
	created, err := mr.CreateOvaTemplateFamily(adminCtx(), model.CreateOvaTemplateFamilyInput{
		Name:        name,
		Type:        typ,
		Description: name + " desc",
		Tools:       []string{"shell"},
		Skills:      []string{},
		Scenarios:   []string{"ops"},
		IconShape:   "circle",
		IconColor:   model.OvaTemplateColorGreen,
		InitialVersion: &model.CreateOvaTemplateVersionInput{
			Version:       initialVersion,
			OvaIdentifier: name + "-" + initialVersion + ".ova",
		},
	})
	if err != nil {
		t.Fatalf("mkFamilyEdge(%s): %v", name, err)
	}
	return created.Family
}

// TestOvaFamiliesEmptyEdge: an empty catalog returns a well-formed, non-nil
// connection (no panic, total 0, sane PageInfo) rather than nil.
func TestOvaFamiliesEmptyEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	ctx := adminCtx()

	conn, err := qr.OvaTemplateFamilies(ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("OvaTemplateFamilies(empty): %v", err)
	}
	if conn == nil {
		t.Fatal("connection is nil on empty catalog")
	}
	if conn.TotalCount != 0 {
		t.Fatalf("empty totalCount = %d, want 0", conn.TotalCount)
	}
	if len(conn.Nodes) != 0 {
		t.Fatalf("empty nodes len = %d, want 0", len(conn.Nodes))
	}
	if conn.PageInfo == nil {
		t.Fatal("PageInfo is nil on empty catalog")
	}
	// Defaults: page 1, default page size, 0 total pages (no rows).
	if conn.PageInfo.Page != 1 || conn.PageInfo.PageSize != 50 {
		t.Fatalf("empty PageInfo = %+v, want page=1 pageSize=50", conn.PageInfo)
	}
	if conn.PageInfo.TotalPages != 0 {
		t.Fatalf("empty totalPages = %d, want 0", conn.PageInfo.TotalPages)
	}
}

// TestOvaVersionsEmptyEdge: empty version list returns a non-nil empty page.
func TestOvaVersionsEmptyEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	ctx := adminCtx()

	conn, err := qr.OvaTemplateVersions(ctx, nil, nil)
	if err != nil {
		t.Fatalf("OvaTemplateVersions(empty): %v", err)
	}
	if conn == nil || conn.PageInfo == nil {
		t.Fatal("nil connection/PageInfo on empty versions")
	}
	if conn.TotalCount != 0 || len(conn.Nodes) != 0 {
		t.Fatalf("empty versions: total=%d nodes=%d, want 0/0", conn.TotalCount, len(conn.Nodes))
	}
	if conn.PageInfo.TotalPages != 0 {
		t.Fatalf("empty versions totalPages = %d, want 0", conn.PageInfo.TotalPages)
	}
}

// TestOvaFamiliesPaginationEdge: page splitting, second page, out-of-range page,
// and totalPages math across more rows than one page holds.
func TestOvaFamiliesPaginationEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	ctx := adminCtx()

	// 5 families, sorted by name asc so paging is deterministic.
	names := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for _, n := range names {
		mkFamilyEdge(t, mr, n, "goose", "1.0.0")
	}
	asc := &model.OvaTemplateFamilySort{Field: model.OvaTemplateFamilySortFieldOvaName, Direction: model.SortDirectionAsc}

	// page 1, pageSize 2 → first two, total 5, totalPages 3.
	p1, err := qr.OvaTemplateFamilies(ctx, nil, &model.Pagination{Page: 1, PageSize: 2}, asc)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if p1.TotalCount != 5 {
		t.Fatalf("totalCount = %d, want 5", p1.TotalCount)
	}
	if len(p1.Nodes) != 2 || p1.Nodes[0].Name != "alpha" || p1.Nodes[1].Name != "bravo" {
		t.Fatalf("page1 nodes wrong: %v", namesOfEdge(p1.Nodes))
	}
	if p1.PageInfo.TotalPages != 3 {
		t.Fatalf("totalPages = %d, want 3 (ceil(5/2))", p1.PageInfo.TotalPages)
	}

	// page 2 → next two, no overlap with page 1.
	p2, err := qr.OvaTemplateFamilies(ctx, nil, &model.Pagination{Page: 2, PageSize: 2}, asc)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(p2.Nodes) != 2 || p2.Nodes[0].Name != "charlie" || p2.Nodes[1].Name != "delta" {
		t.Fatalf("page2 nodes wrong: %v", namesOfEdge(p2.Nodes))
	}

	// last page is partial (5 rows, size 2 → page 3 has 1).
	p3, err := qr.OvaTemplateFamilies(ctx, nil, &model.Pagination{Page: 3, PageSize: 2}, asc)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(p3.Nodes) != 1 || p3.Nodes[0].Name != "echo" {
		t.Fatalf("page3 (partial) wrong: %v", namesOfEdge(p3.Nodes))
	}

	// page beyond the last yields an empty slice but the true total (not a panic).
	pOver, err := qr.OvaTemplateFamilies(ctx, nil, &model.Pagination{Page: 99, PageSize: 2}, asc)
	if err != nil {
		t.Fatalf("page over-range: %v", err)
	}
	if pOver.TotalCount != 5 {
		t.Fatalf("over-range totalCount = %d, want 5", pOver.TotalCount)
	}
	if len(pOver.Nodes) != 0 {
		t.Fatalf("over-range nodes = %d, want 0", len(pOver.Nodes))
	}
	if pOver.PageInfo.Page != 99 {
		t.Fatalf("over-range echoes page = %d, want 99", pOver.PageInfo.Page)
	}

	// Non-positive page/pageSize are ignored → defaults (page 1, size 50): all 5
	// fit on one page.
	pDefault, err := qr.OvaTemplateFamilies(ctx, nil, &model.Pagination{Page: 0, PageSize: -3}, asc)
	if err != nil {
		t.Fatalf("default page: %v", err)
	}
	if pDefault.PageInfo.Page != 1 || pDefault.PageInfo.PageSize != 50 {
		t.Fatalf("non-positive pagination not defaulted: %+v", pDefault.PageInfo)
	}
	if len(pDefault.Nodes) != 5 {
		t.Fatalf("default page nodes = %d, want 5", len(pDefault.Nodes))
	}
}

// TestOvaVersionsPaginationEdge: versions paginate newest-first within one
// family and the page window slides correctly.
func TestOvaVersionsPaginationEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	ctx := adminCtx()

	fam := mkFamilyEdge(t, mr, "multi", "goose", "1.0.0")
	// Append v2, v3 → ordering by created_at desc means [v3, v2, v1].
	for _, v := range []string{"2.0.0", "3.0.0"} {
		if _, err := mr.AddOvaTemplateVersion(ctx, model.AddOvaTemplateVersionInput{
			FamilyID:      fam.ID,
			Version:       v,
			OvaIdentifier: "multi-" + v + ".ova",
		}); err != nil {
			t.Fatalf("AddOvaTemplateVersion(%s): %v", v, err)
		}
	}

	page1, err := qr.OvaTemplateVersions(ctx, ptr(fam.ID), &model.Pagination{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("versions page1: %v", err)
	}
	if page1.TotalCount != 3 || page1.PageInfo.TotalPages != 2 {
		t.Fatalf("versions paging meta wrong: total=%d totalPages=%d", page1.TotalCount, page1.PageInfo.TotalPages)
	}
	if len(page1.Nodes) != 2 || page1.Nodes[0].Version != "3.0.0" || page1.Nodes[1].Version != "2.0.0" {
		t.Fatalf("versions page1 not newest-first: %v", versionsOfEdge(page1.Nodes))
	}
	// scoped page nodes carry the known familyId (no lazy edge load needed).
	if page1.Nodes[0].FamilyID != fam.ID {
		t.Fatalf("scoped version familyId = %q, want %q", page1.Nodes[0].FamilyID, fam.ID)
	}

	page2, err := qr.OvaTemplateVersions(ctx, ptr(fam.ID), &model.Pagination{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("versions page2: %v", err)
	}
	if len(page2.Nodes) != 1 || page2.Nodes[0].Version != "1.0.0" {
		t.Fatalf("versions page2 (oldest) wrong: %v", versionsOfEdge(page2.Nodes))
	}
}

// TestOvaVersionsBadFamilyIDEdge: invalid UUID errors; a syntactically valid but
// nonexistent family id yields an empty page (the "no oracle" contract), not an
// error or panic.
func TestOvaVersionsBadFamilyIDEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	ctx := adminCtx()

	// Seed one family so the global table is non-empty: a nonexistent-id scope
	// must still return 0 (proves it actually filtered, not just "empty table").
	mkFamilyEdge(t, mr, "present", "goose", "1.0.0")

	// invalid UUID → error.
	if _, err := qr.OvaTemplateVersions(ctx, ptr("not-a-uuid"), nil); err == nil {
		t.Fatal("expected error for invalid familyId")
	}

	// valid-but-missing UUID → empty page, no error.
	missing := "00000000-0000-0000-0000-0000000000aa"
	conn, err := qr.OvaTemplateVersions(ctx, ptr(missing), nil)
	if err != nil {
		t.Fatalf("OvaTemplateVersions(missing family): %v", err)
	}
	if conn.TotalCount != 0 || len(conn.Nodes) != 0 {
		t.Fatalf("missing-family scope should be empty, got total=%d nodes=%d", conn.TotalCount, len(conn.Nodes))
	}

	// empty-string familyId is treated as "unscoped" → sees the seeded version.
	all, err := qr.OvaTemplateVersions(ctx, ptr(""), nil)
	if err != nil {
		t.Fatalf("OvaTemplateVersions(empty familyId): %v", err)
	}
	if all.TotalCount != 1 {
		t.Fatalf("empty familyId should be unscoped (total=1), got %d", all.TotalCount)
	}
}

// TestOvaAddVersionNotFoundEdge: appending to a missing/invalid family must error
// (no silent create) and must not leave an orphan version behind.
func TestOvaAddVersionNotFoundEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	ctx := adminCtx()

	// invalid UUID → "invalid familyId".
	if _, err := mr.AddOvaTemplateVersion(ctx, model.AddOvaTemplateVersionInput{
		FamilyID:      "bogus",
		Version:       "1.0.0",
		OvaIdentifier: "x.ova",
	}); err == nil {
		t.Fatal("expected error for invalid familyId on add")
	}

	// valid-but-nonexistent family → "not found".
	if _, err := mr.AddOvaTemplateVersion(ctx, model.AddOvaTemplateVersionInput{
		FamilyID:      "00000000-0000-0000-0000-0000000000bb",
		Version:       "1.0.0",
		OvaIdentifier: "x.ova",
	}); err == nil {
		t.Fatal("expected error adding to nonexistent family")
	}

	// No version row was created by either failed add.
	conn, err := qr.OvaTemplateVersions(ctx, nil, nil)
	if err != nil {
		t.Fatalf("OvaTemplateVersions: %v", err)
	}
	if conn.TotalCount != 0 {
		t.Fatalf("failed adds left %d orphan version(s)", conn.TotalCount)
	}
}

// TestOvaAddVersionHappyAndDuplicateEdge: a successful add returns the new
// version and advances latestVersion; the catalog is append-only with NO
// dedupe, so re-adding the same version string creates a second distinct row
// (this pins the documented append-only behavior — it would fail if a dedupe
// constraint were silently introduced without updating the contract).
func TestOvaAddVersionHappyAndDuplicateEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	famR := &ovaTemplateFamilyResolver{r}
	ctx := adminCtx()

	fam := mkFamilyEdge(t, mr, "dup", "goose", "1.0.0")

	// happy: append 2.0.0 → returned payload + latestVersion both reflect it.
	added, err := mr.AddOvaTemplateVersion(ctx, model.AddOvaTemplateVersionInput{
		FamilyID:      fam.ID,
		Version:       "2.0.0",
		OvaIdentifier: "dup-2.0.0.ova",
		Notes:         ptr("second"),
	})
	if err != nil {
		t.Fatalf("AddOvaTemplateVersion(happy): %v", err)
	}
	if added.Version.Version != "2.0.0" || added.Version.FamilyID != fam.ID {
		t.Fatalf("added payload wrong: %+v", added.Version)
	}
	if added.Version.Notes == nil || *added.Version.Notes != "second" {
		t.Fatalf("added notes = %v, want \"second\"", added.Version.Notes)
	}
	lv, err := famR.LatestVersion(ctx, fam)
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if lv == nil || *lv != "2.0.0" {
		t.Fatalf("latestVersion after happy add = %v, want 2.0.0", lv)
	}

	// duplicate version string: append-only → succeeds, creates a 2nd row with a
	// distinct id, and total versions becomes 3 (1.0.0, 2.0.0, 2.0.0).
	dup, err := mr.AddOvaTemplateVersion(ctx, model.AddOvaTemplateVersionInput{
		FamilyID:      fam.ID,
		Version:       "2.0.0",
		OvaIdentifier: "dup-2.0.0-rebuild.ova",
	})
	if err != nil {
		t.Fatalf("AddOvaTemplateVersion(duplicate string): %v", err)
	}
	if dup.Version.ID == added.Version.ID {
		t.Fatal("duplicate add reused the existing version id (should be a new row)")
	}
	conn, err := qr.OvaTemplateVersions(ctx, ptr(fam.ID), nil)
	if err != nil {
		t.Fatalf("OvaTemplateVersions(scoped): %v", err)
	}
	if conn.TotalCount != 3 {
		t.Fatalf("append-only: expected 3 versions after duplicate add, got %d", conn.TotalCount)
	}
}

// TestOvaLatestVersionNoneEdge: a family with no versions resolves latestVersion
// to nil and Versions to empty on BOTH the lazy single-entity path and the
// eager-loaded list path (the two must agree).
func TestOvaLatestVersionNoneEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	famR := &ovaTemplateFamilyResolver{r}
	ctx := adminCtx()

	// Create a bare family directly via ent (no initial version) so we can probe
	// the "no versions" branch the mutation path never produces.
	ent, err := r.Ent.OvaTemplateFamily.Create().
		SetName("bare").
		SetType("goose").
		SetDescription("no versions").
		SetIconShape("circle").
		Save(ctx)
	if err != nil {
		t.Fatalf("seed bare family: %v", err)
	}
	bare := toModelOvaFamily(ent) // Versions == nil → forces the lazy path

	// lazy path: nil latest, empty versions, no error.
	lv, err := famR.LatestVersion(ctx, bare)
	if err != nil {
		t.Fatalf("LatestVersion(lazy, none): %v", err)
	}
	if lv != nil {
		t.Fatalf("latestVersion of empty family = %v, want nil", *lv)
	}
	vers, err := famR.Versions(ctx, bare)
	if err != nil {
		t.Fatalf("Versions(lazy, none): %v", err)
	}
	if len(vers) != 0 {
		t.Fatalf("empty family Versions = %d, want 0", len(vers))
	}

	// eager path: a non-nil but empty Versions slice is the "loaded, none"
	// sentinel — latestVersion must still be nil without re-querying.
	eager := toModelOvaFamily(ent)
	eager.Versions = []model.OvaTemplateVersion{} // sentinel: loaded, zero rows
	elv, err := famR.LatestVersion(ctx, eager)
	if err != nil {
		t.Fatalf("LatestVersion(eager, none): %v", err)
	}
	if elv != nil {
		t.Fatalf("eager empty latestVersion = %v, want nil", *elv)
	}
}

// TestOvaEagerVsLazyAgreeEdge: the list resolver eager-loads versions; the per-row
// Versions/LatestVersion field resolvers must serve identical results to the lazy
// single-entity path (this is the N+1 fix's correctness invariant).
func TestOvaEagerVsLazyAgreeEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	famR := &ovaTemplateFamilyResolver{r}
	ctx := adminCtx()

	fam := mkFamilyEdge(t, mr, "eager", "goose", "1.0.0")
	for _, v := range []string{"1.1.0", "1.2.0"} {
		if _, err := mr.AddOvaTemplateVersion(ctx, model.AddOvaTemplateVersionInput{
			FamilyID:      fam.ID,
			Version:       v,
			OvaIdentifier: "eager-" + v + ".ova",
		}); err != nil {
			t.Fatalf("AddOvaTemplateVersion(%s): %v", v, err)
		}
	}

	// lazy: fam came from the mutation payload with Versions == nil.
	lazyVers, err := famR.Versions(ctx, fam)
	if err != nil {
		t.Fatalf("Versions(lazy): %v", err)
	}
	lazyLatest, err := famR.LatestVersion(ctx, fam)
	if err != nil {
		t.Fatalf("LatestVersion(lazy): %v", err)
	}

	// eager: pull the same family back through the list resolver, which sets the
	// Versions sentinel from WithVersions.
	list, err := qr.OvaTemplateFamilies(ctx, &model.OvaTemplateFamilyFilter{NameKeyword: ptr("eager")}, nil, nil)
	if err != nil {
		t.Fatalf("OvaTemplateFamilies: %v", err)
	}
	if len(list.Nodes) != 1 {
		t.Fatalf("expected 1 eager family, got %d", len(list.Nodes))
	}
	node := &list.Nodes[0]
	if node.Versions == nil {
		t.Fatal("list resolver did not eager-load Versions (nil sentinel)")
	}
	eagerVers, err := famR.Versions(ctx, node)
	if err != nil {
		t.Fatalf("Versions(eager): %v", err)
	}
	eagerLatest, err := famR.LatestVersion(ctx, node)
	if err != nil {
		t.Fatalf("LatestVersion(eager): %v", err)
	}

	// Both paths: newest-first [1.2.0, 1.1.0, 1.0.0] and latest 1.2.0.
	want := []string{"1.2.0", "1.1.0", "1.0.0"}
	if got := versionsOfEdge(lazyVers); !equalStringsEdge(got, want) {
		t.Fatalf("lazy versions = %v, want %v", got, want)
	}
	if got := versionsOfEdge(eagerVers); !equalStringsEdge(got, want) {
		t.Fatalf("eager versions = %v, want %v", got, want)
	}
	if lazyLatest == nil || eagerLatest == nil || *lazyLatest != *eagerLatest || *eagerLatest != "1.2.0" {
		t.Fatalf("latest mismatch: lazy=%v eager=%v want 1.2.0", lazyLatest, eagerLatest)
	}
	// The eager pre-population also surfaces LatestVersion directly on the node.
	if node.LatestVersion == nil || *node.LatestVersion != "1.2.0" {
		t.Fatalf("node.LatestVersion = %v, want 1.2.0", node.LatestVersion)
	}
	// FamilyID must be stamped on eager-loaded version nodes too.
	if eagerVers[0].FamilyID != fam.ID {
		t.Fatalf("eager version familyId = %q, want %q", eagerVers[0].FamilyID, fam.ID)
	}
}

// TestOvaVersionFamilyIDLazyEdge: the unscoped versions query leaves FamilyID
// empty on each node; the FamilyID field resolver then fills it via an edge
// lookup. A bad obj id must error (not panic).
func TestOvaVersionFamilyIDLazyEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	verR := &ovaTemplateVersionResolver{r}
	ctx := adminCtx()

	fam := mkFamilyEdge(t, mr, "lazyfid", "goose", "1.0.0")

	// unscoped query → FamilyID left empty on the node (avoids an N+1 edge load).
	unscoped, err := qr.OvaTemplateVersions(ctx, nil, nil)
	if err != nil {
		t.Fatalf("OvaTemplateVersions(unscoped): %v", err)
	}
	if len(unscoped.Nodes) != 1 {
		t.Fatalf("expected 1 version, got %d", len(unscoped.Nodes))
	}
	node := unscoped.Nodes[0]
	if node.FamilyID != "" {
		t.Fatalf("unscoped node should have empty FamilyID, got %q", node.FamilyID)
	}

	// FamilyID field resolver fills it lazily from the edge.
	fid, err := verR.FamilyID(ctx, &node)
	if err != nil {
		t.Fatalf("FamilyID(lazy): %v", err)
	}
	if fid != fam.ID {
		t.Fatalf("resolved familyId = %q, want %q", fid, fam.ID)
	}

	// already-populated FamilyID short-circuits (returned verbatim).
	known := node
	known.FamilyID = "preset"
	if got, err := verR.FamilyID(ctx, &known); err != nil || got != "preset" {
		t.Fatalf("FamilyID(preset) = %q,%v want preset,nil", got, err)
	}

	// bad obj id → error, no panic.
	bad := &model.OvaTemplateVersion{ID: "not-a-uuid"}
	if _, err := verR.FamilyID(ctx, bad); err == nil {
		t.Fatal("expected error resolving FamilyID for bad obj id")
	}
}

// TestOvaFamilyResolversBadObjIDEdge: the family field resolvers must not panic
// when handed an object with an unparseable id on the lazy path.
func TestOvaFamilyResolversBadObjIDEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	famR := &ovaTemplateFamilyResolver{r}
	ctx := adminCtx()

	bad := &model.OvaTemplateFamily{ID: "not-a-uuid"} // Versions == nil → lazy path
	if _, err := famR.LatestVersion(ctx, bad); err == nil {
		t.Fatal("expected error from LatestVersion on bad obj id")
	}
	if _, err := famR.Versions(ctx, bad); err == nil {
		t.Fatal("expected error from Versions on bad obj id")
	}
}

// --- local assertion helpers (Edge-suffixed to avoid tree-wide collisions) ---

func namesOfEdge(fams []model.OvaTemplateFamily) []string {
	out := make([]string, 0, len(fams))
	for _, f := range fams {
		out = append(out, f.Name)
	}
	return out
}

func versionsOfEdge(vers []model.OvaTemplateVersion) []string {
	out := make([]string, 0, len(vers))
	for _, v := range vers {
		out = append(out, v.Version)
	}
	return out
}

func equalStringsEdge(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
