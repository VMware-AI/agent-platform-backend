package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Tenant is the top-level org boundary (LLD-01 §1.4 / doc43).
type Tenant struct {
	ent.Schema
}

func (Tenant) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (Tenant) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty(),
	}
}

func (Tenant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("departments", Department.Type),
	}
}
