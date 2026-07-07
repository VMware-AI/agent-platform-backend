package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// GatewayConnection registers a LiteLLM proxy the backend governs (0619 模型网关接入).
// master_key is a secret-store reference, never stored in plaintext.
//
// A model route is bound to a gateway via ModelRoute.gateway_connection_id
// (required, see LLD-13 §3.3 / LLD-14). Each route's router-settings push
// targets the gateway of that route — there is no platform default.
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
		// last_synced_at: when the gateway last successfully connected (set on a
		// successful connection test). Nil = never synced. Distinct from updated_at
		// so an unrelated edit does not move the apparent sync time.
		field.Time("last_synced_at").Optional().Nillable(),
		// backend_model_count: number of models the gateway reports (len of GET
		// /models' data array). Set on a successful sync, preserved on failure
		// (so a transient outage doesn't zero the displayed count). Nil until the
		// first successful sync — projected as 0 in that case.
		field.Int("backend_model_count").Optional().Nillable(),
		field.Enum("status").Values("connected", "disconnected", "error").Default("disconnected"),
		field.Enum("load_balance_strategy").
			Values("SIMPLE_SHUFFLE", "LEAST_BUSY", "LATENCY_BASED_ROUTING", "USAGE_BASED_ROUTING_V2", "COST_BASED_ROUTING").
			Default("SIMPLE_SHUFFLE"),
	}
}
