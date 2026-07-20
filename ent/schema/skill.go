package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Skill is a Skill-hub item: a distributable agent skill package (LLD-06).
type Skill struct {
	ent.Schema
}

func (Skill) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Skill) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty(),
		field.String("version").NotEmpty(),
		field.String("description").Optional(),
		field.String("uri").NotEmpty(),
		field.String("install_method").Default("pip"),
		field.JSON("mcp_config", map[string]interface{}{}).Optional(),
		field.String("package_url").Optional(),
		field.String("category").Optional(),
	}
}
