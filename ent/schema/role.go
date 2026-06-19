package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Role is a fine-grained role (LLD-01 §1.2). M1.0 enforces via User.role enum;
// these tables back the 0619 "角色与权限" page (custom roles + permission matrix).
type Role struct {
	ent.Schema
}

func (Role) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (Role) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty().Unique(),
		field.Bool("is_system").Default(false),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
	}
}

func (Role) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("users", User.Type).Ref("roles"),
		edge.To("permissions", Permission.Type),
	}
}
