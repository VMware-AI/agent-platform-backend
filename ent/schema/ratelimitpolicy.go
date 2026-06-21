package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// RateLimitPolicy is a named rate-limit policy (限流策略, 0619). Enforcement is
// per-key/team at the gateway (research §2.3) — global keys are silently
// ignored, so policies are applied by binding them to keys/teams, never globally.
type RateLimitPolicy struct {
	ent.Schema
}

func (RateLimitPolicy) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (RateLimitPolicy) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty().Unique(),
		field.Int("rpm").Optional().Nillable(), // requests per minute
		field.Int("tpm").Optional().Nillable(), // tokens per minute
		field.Bool("enabled").Default(false),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("environment_id", uuid.UUID{}).Optional().Nillable(), // LLD-10 env_scope (default off)
	}
}
