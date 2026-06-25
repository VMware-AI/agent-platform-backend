package graph

import (
	"fmt"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/ovatemplatefamily"
	"github.com/VMware-AI/agent-platform-backend/ent/ovatemplateversion"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// rollback aborts the transaction and wraps the originating error, attaching the
// rollback failure (if any) so neither is silently swallowed.
func rollback(tx *ent.Tx, err error) error {
	if rerr := tx.Rollback(); rerr != nil {
		return fmt.Errorf("%w (rollback: %v)", err, rerr)
	}
	return err
}

// ovaDefaultPageSize is the catalog connection default when the console omits
// pagination (mirrors the contract's default page size).
const ovaDefaultPageSize = 50

// ovaPage normalizes an optional Pagination into (page, pageSize), defaulting to
// page 1 / ovaDefaultPageSize and ignoring non-positive values (前后端整合契约).
func ovaPage(p *model.Pagination) (page, pageSize int) {
	page, pageSize = 1, ovaDefaultPageSize
	if p != nil {
		if p.Page > 0 {
			page = p.Page
		}
		if p.PageSize > 0 {
			pageSize = p.PageSize
		}
	}
	return page, pageSize
}

// toModelOvaFamily maps an ent.OvaTemplateFamily to the GraphQL model. Edge-backed
// fields (versions/latestVersion) are left zero — they resolve lazily via field
// resolvers, so callers never need the versions eager-loaded here.
func toModelOvaFamily(f *ent.OvaTemplateFamily) *model.OvaTemplateFamily {
	return &model.OvaTemplateFamily{
		ID:          f.ID.String(),
		Name:        f.Name,
		Type:        f.Type,
		Description: f.Description,
		Tools:       sliceOrEmpty(f.Tools),
		Skills:      sliceOrEmpty(f.Skills),
		Scenarios:   sliceOrEmpty(f.Scenarios),
		IconShape:   f.IconShape,
		IconColor:   model.OvaTemplateColor(f.IconColor),
		CreatedAt:   f.CreatedAt,
		UpdatedAt:   f.UpdatedAt,
	}
}

// toModelOvaVersion maps an ent.OvaTemplateVersion to the GraphQL model. familyID
// is supplied by the caller (the version's owning family), avoiding a second edge
// load when it is already known; the field resolver fills it otherwise.
func toModelOvaVersion(v *ent.OvaTemplateVersion, familyID string) *model.OvaTemplateVersion {
	return &model.OvaTemplateVersion{
		ID:            v.ID.String(),
		FamilyID:      familyID,
		Version:       v.Version,
		OvaIdentifier: v.OvaIdentifier,
		Notes:         v.Notes,
		CreatedAt:     v.CreatedAt,
	}
}

// sliceOrEmpty returns a non-nil slice so the GraphQL non-null list fields
// (tools/skills/scenarios) never serialize as null when the column is empty.
func sliceOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// applyOvaFamilySort orders a family query by the console's sort field with a
// stable id tiebreak. OVA_NAME maps to the name column; the default (CREATED_AT)
// and any unmapped field fall back to created_at.
func applyOvaFamilySort(q *ent.OvaTemplateFamilyQuery, sort *model.OvaTemplateFamilySort) *ent.OvaTemplateFamilyQuery {
	if sort == nil {
		return q.Order(ent.Desc(ovatemplatefamily.FieldCreatedAt), ent.Desc(ovatemplatefamily.FieldID))
	}
	col := ovatemplatefamily.FieldCreatedAt
	switch sort.Field {
	case model.OvaTemplateFamilySortFieldOvaName:
		col = ovatemplatefamily.FieldName
	case model.OvaTemplateFamilySortFieldType:
		col = ovatemplatefamily.FieldType
	case model.OvaTemplateFamilySortFieldUpdatedAt:
		col = ovatemplatefamily.FieldUpdatedAt
	default: // CREATED_AT
		col = ovatemplatefamily.FieldCreatedAt
	}
	if sort.Direction == model.SortDirectionDesc {
		return q.Order(ent.Desc(col), ent.Desc(ovatemplatefamily.FieldID))
	}
	return q.Order(ent.Asc(col), ent.Asc(ovatemplatefamily.FieldID))
}

// orderVersionsNewest orders versions newest-first (created_at desc, id desc) —
// the order both the versions edge resolver and the versions list query use.
func orderVersionsNewest(q *ent.OvaTemplateVersionQuery) *ent.OvaTemplateVersionQuery {
	return q.Order(ent.Desc(ovatemplateversion.FieldCreatedAt), ent.Desc(ovatemplateversion.FieldID))
}
