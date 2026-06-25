package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// OvaTemplateVersion is one versioned OVA image under an OvaTemplateFamily. The
// ova_identifier points at the deployable artifact (content-library item /
// template name) the deploy flow will clone from (deferred phase). Versions are
// append-only from the console; the family's latestVersion is derived from the
// most recently created version.
type OvaTemplateVersion struct {
	ent.Schema
}

func (OvaTemplateVersion) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (OvaTemplateVersion) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("version").NotEmpty(),
		field.String("ova_identifier").NotEmpty(),
		field.String("notes").Optional().Nillable(),
		// Explicit FK for the `family` edge so it can be indexed. Required + the
		// edge's Unique() keep it NOT NULL (every version belongs to one family).
		field.UUID("family_id", uuid.UUID{}),
	}
}

// Edges: each version belongs to exactly one family (the FK side of the
// `versions` edge). Required + Unique → a NOT NULL family_id column. The FK is
// surfaced as an explicit `family_id` field so it can be indexed (below).
func (OvaTemplateVersion) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("family", OvaTemplateFamily.Type).
			Ref("versions").
			Field("family_id").
			Unique().
			Required(),
	}
}

// Indexes: family_id backs every read path (versions/latestVersion edge walks and
// ovaTemplateVersions(familyId) filtering), so index the FK to avoid full scans.
func (OvaTemplateVersion) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("family_id"),
	}
}
