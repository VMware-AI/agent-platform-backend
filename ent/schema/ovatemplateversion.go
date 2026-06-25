package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
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
	}
}

// Edges: each version belongs to exactly one family (the FK side of the
// `versions` edge). Required + Unique → a NOT NULL family_id column.
func (OvaTemplateVersion) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("family", OvaTemplateFamily.Type).
			Ref("versions").
			Unique().
			Required(),
	}
}
