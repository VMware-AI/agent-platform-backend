package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// User is an authenticated principal (LLD-01 §1.1).
type User struct {
	ent.Schema
}

func (User) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("username").NotEmpty().Unique(),
		field.String("email").NotEmpty().Unique(),
		// Sensitive: never serialized into GraphQL output or logs.
		field.String("password_hash").NotEmpty().Sensitive(),
		field.Enum("role").
			Values("admin", "user", "read_only").
			Default("user"),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.Bool("must_change_password").Default(true),
		field.Bool("is_active").Default(true),
		field.Time("last_login_at").Optional().Nillable(),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("roles", Role.Type),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
	}
}
