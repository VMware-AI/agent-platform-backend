package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Environment is a logical partition WITHIN a tenant (dev/staging/prod or a
// project grouping) — a SOFT boundary for view filtering, NOT a security
// boundary (tenant_id is). It always belongs to a tenant (tenant_id is required,
// fail-safe). env_scope is built here but stays disabled by default until the
// frontend env switcher + X-Environment contract land (LLD-10 §2).
type Environment struct {
	ent.Schema
}

func (Environment) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Environment) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		// NOT optional: an environment must belong to a tenant (fail-safe).
		field.UUID("tenant_id", uuid.UUID{}),
		field.String("name").NotEmpty(), // dev / staging / prod / ...
		field.String("description").Optional(),
	}
}

func (Environment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "name").Unique(), // unique within a tenant
		index.Fields("tenant_id"),
	}
}
