package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// OvaTemplateFamily is a catalog entry in the Agent Marketplace (智能体市场 OVA
// 模板族). It groups the metadata an operator browses (name/type/description/
// tools/skills/scenarios + icon) and owns N versioned OVA images via the
// `versions` edge. The catalog is platform-global (no tenant_id) — mirrors
// AgentTemplate / GatewayConnection — and is admin-gated at the GraphQL layer.
type OvaTemplateFamily struct {
	ent.Schema
}

func (OvaTemplateFamily) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (OvaTemplateFamily) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty(),
		// Free-form agent kind label (= console AgentType, e.g. goose/xiaoguai).
		field.String("type").NotEmpty(),
		field.String("description").Default(""),
		// Display chips on the marketplace card — JSON string arrays.
		field.Strings("tools").Optional(),
		field.Strings("skills").Optional(),
		field.Strings("scenarios").Optional(),
		// Card icon shape key (console-defined) + a fixed palette color.
		field.String("icon_shape").Default(""),
		field.Enum("icon_color").
			Values("BLUE", "PURPLE", "ORANGE", "GREEN", "RED", "CYAN").
			Default("BLUE"),
	}
}

// Edges: a family owns its versions; deleting a family cascades to its versions.
func (OvaTemplateFamily) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("versions", OvaTemplateVersion.Type),
	}
}
