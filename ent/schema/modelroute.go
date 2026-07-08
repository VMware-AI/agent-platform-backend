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
		field.String("model_alias").NotEmpty(), // outward model name
		// 后端模型网关 (0619 第6页): which GatewayConnection serves this route.
		// Required — the router-settings push targets exactly this gateway (LLD-13
		// §3.3). A route with no gateway would have no place to push, and the old
		// "platform default" fallback has been retired. Default required/non-null;
		// column will be NOT NULL in the DB.
		field.UUID("gateway_connection_id", uuid.UUID{}),
		// Display name of the serving gateway, denormalized for the console 模型路由
		// list (the view shows gatewayName next to each route).
		field.String("gateway_name").Optional().Default(""),
		field.Strings("upstreams").Optional(), // upstream names in the group
		field.Enum("strategy").
			Values("SIMPLE_SHUFFLE", "LEAST_BUSY", "LATENCY_BASED_ROUTING", "USAGE_BASED_ROUTING_V2", "COST_BASED_ROUTING").
			Default("SIMPLE_SHUFFLE"),
		// Console-facing load-balancing strategy (模型路由 page): a friendly,
		// gateway-agnostic enum distinct from `strategy` above (the console
		// exposes it as a small choice list separate from the litellm routing
		// strategies). Persisted so the console round-trips exactly what the
		// operator picked.
		field.Enum("ui_strategy").
			Values("ROUND_ROBIN", "WEIGHTED_ROUND_ROBIN", "RANDOM").
			Default("ROUND_ROBIN"),
		field.Bool("enabled").Default(true),
		// Fallback chains surfaced to litellm via POST /config/update. Three
		// independent lists map 1:1 to the doc's three fallback kinds (general /
		// context-window / content-policy). Each entry is an alias name
		// referencing another route's model_alias.
		field.Strings("fallbacks").Optional(),
		field.Strings("context_window_fallbacks").Optional(),
		field.Strings("content_policy_fallbacks").Optional(),
	}
}
