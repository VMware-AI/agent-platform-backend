package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// GatewayConnection registers a LiteLLM proxy the backend governs (0619 模型网关接入).
// master_key is a Vaultwarden reference, never stored in plaintext.
type GatewayConnection struct {
	ent.Schema
}

func (GatewayConnection) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (GatewayConnection) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty().Unique(),
		field.String("endpoint").NotEmpty(),
		field.String("master_key_ref").Optional(), // vault://item-id
		field.Enum("status").Values("connected", "disconnected", "error").Default("disconnected"),
		field.Enum("load_balance_strategy").
			Values("simple_shuffle", "latency", "usage_v2", "least_busy", "cost").
			Default("simple_shuffle"),
	}
}
