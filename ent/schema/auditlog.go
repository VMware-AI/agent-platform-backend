package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// AuditLog records every write operation (LLD-01 §1.5).
type AuditLog struct {
	ent.Schema
}

func (AuditLog) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("actor_user_id", uuid.UUID{}).Optional().Nillable(),
		field.String("action").NotEmpty(), // user.create, password.reset, ...
		field.String("resource_type").Optional(),
		field.String("resource_id").Optional(),
		field.String("ip").Optional(),
		field.Enum("result").Values("success", "fail").Default("success"),
		field.JSON("detail", map[string]any{}).Optional(),
		field.Time("created_at").Immutable().Default(time.Now),
	}
}

// actor_user_id is a soft reference (no FK): audit is append-only and must
// survive user deletion; a write must never fail because the actor is unknown.

func (AuditLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("action"),
		index.Fields("created_at"),
	}
}
