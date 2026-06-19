package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// TokenUsage is an append-only metering record (HLD §12 / LLD-01 §5.1). Written
// by the gateway usage callback / telemetry ingest; sliced in the metering UI.
type TokenUsage struct {
	ent.Schema
}

func (TokenUsage) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("user_id", uuid.UUID{}),
		field.UUID("agent_id", uuid.UUID{}).Optional().Nillable(),
		field.String("model").NotEmpty(),
		field.Int("input_tokens").NonNegative().Default(0),
		field.Int("output_tokens").NonNegative().Default(0),
		field.Float("cost").Optional().Default(0),
		field.String("correlation_id").Optional(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("department_id", uuid.UUID{}).Optional().Nillable(),
		field.Time("created_at").Immutable().Default(time.Now),
	}
}

func (TokenUsage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id"),
		index.Fields("model"),
		index.Fields("created_at"),
	}
}
