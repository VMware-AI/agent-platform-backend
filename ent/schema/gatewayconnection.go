package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// GatewayConnection registers a LiteLLM proxy the backend governs (0619 模型网关接入).
// master_key is a secret-store reference, never stored in plaintext.
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
		// public_url: the gateway URL provisioned VMs/agents actually call (LLD-13
		// §3.3, replaces the GATEWAY_PUBLIC_URL env). Optional — falls back to
		// endpoint when unset (the backend's own API base may differ from a public ingress).
		field.String("public_url").Optional(),
		// is_default: the fallback gateway for ops with no department context
		// (e.g. upstream/router-tier sync, a virtual key issued without a team).
		// At most one row is true; the resolver enforces the singleton on set.
		field.Bool("is_default").Default(false),
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

func (GatewayConnection) Indexes() []ent.Index {
	return []ent.Index{
		// At most ONE default gateway, enforced by the DB (a partial unique index
		// over the rows where is_default is true). The resolver still clears the
		// previous default on set, but this closes the concurrent-set race that
		// could otherwise leave two defaults. Writers must clear-then-set within a
		// txn so a single request never transiently holds two true rows.
		index.Fields("is_default").Unique().
			Annotations(entsql.IndexWhere("is_default")),
	}
}
