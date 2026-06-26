package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Department = a litellm team (doc43 决策1). Admin-facing grouping; users 无感.
// tenant_id is a soft reference (no ent edge) for M1 simplicity.
type Department struct {
	ent.Schema
}

func (Department) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Department) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.String("name").NotEmpty(),
		// Handle to the litellm team (typically == id). Sync via gateway client.
		field.String("litellm_team_id").Optional(),
		// The gateway connection that hosts this department's litellm team
		// (LLD-13 §3.3): virtual-key / team / deploy ops route here. Nil → the
		// platform default gateway (GatewayConnection.is_default).
		field.UUID("gateway_connection_id", uuid.UUID{}).Optional().Nillable(),
	}
}
