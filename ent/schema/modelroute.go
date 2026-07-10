package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// ModelRoute maps an outward model alias to a set of upstreams (a litellm model
// group, load-balanced) — the 模型路由配置 page.
type ModelRoute struct {
	ent.Schema
}

func (ModelRoute) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ModelRoute) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty().Unique(),
		// 后端模型网关 (0619 第6页): which GatewayConnection serves this route.
		// Required — the router-settings push targets exactly this gateway (LLD-13
		// §3.3). A route with no gateway would have no place to push, and the old
		// "platform default" fallback has been retired. Default required/non-null;
		// column will be NOT NULL in the DB.
		//
		// 2026-07 rename: gateway_connection_id → model_gateway_id, mirroring
		// VirtualKey's rename. The GraphQL field that reads this column is
		// `modelGateway` (a nested ModelGateway object); see VirtualKey
		// schema for the same wire shape.
		field.UUID("model_gateway_id", uuid.UUID{}),
		// The litellm model group served by this route. Mapped 1:1 to
		// routing_groups[i].models on the wire. The first element is also
		// the implicit key under which the route's three fallback chains
		// are surfaced (see AggregateRouterSettings).
		field.Strings("supported_models").Optional(),
		field.Enum("strategy").
			Values("SIMPLE_SHUFFLE", "LEAST_BUSY", "LATENCY_BASED_ROUTING", "USAGE_BASED_ROUTING_V2", "COST_BASED_ROUTING").
			Default("SIMPLE_SHUFFLE"),
		// Fallback chains surfaced to litellm via POST /config/update. Three
		// independent lists map 1:1 to the doc's three fallback kinds (general /
		// context-window / content-policy). Each entry is a litellm model name
		// on the wire (keyed by supported_models[0]).
		field.Strings("fallbacks").Optional(),
		field.Strings("context_window_fallbacks").Optional(),
		field.Strings("content_policy_fallbacks").Optional(),
	}
}
