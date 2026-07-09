package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// VirtualKey is a per-agent-per-org LiteLLM virtual key issued by the
// gateway (LLD-04, refactored 2026-07). The secret (litellm_key) and its
// persistent preview (masked_key) are both Sensitive — neither is ever
// serialized to GraphQL or logs. litellm_key is delivered to the VM via
// guestinfo; masked_key is shown on the operator console. The DB row
// holds governance metadata and is owned by the organization, not a
// single user.
type VirtualKey struct {
	ent.Schema
}

func (VirtualKey) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (VirtualKey) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		// The LiteLLM virtual key secret — kept out of all output.
		field.String("litellm_key").Sensitive().NotEmpty(),
		// LiteLLM's hashed token (what GET /key/list returns). Stored at issue
		// time so reconciliation matches by it; empty for keys issued before
		// this field.
		field.String("litellm_token").Optional(),
		// Persistent, safe-to-display preview of the secret. Sensitive (we do
		// not want it in logs/JSON even though it is not itself a secret).
		// Format: "head6...tail4" via gateway.redactKey.
		field.String("masked_key").Sensitive().NotEmpty(),
		// Human-readable label, required since 2026-07 refactor (replaces
		// the prior optional alias column).
		field.String("name").NotEmpty(),
		field.UUID("agent_id", uuid.UUID{}).Optional().Nillable(),
		// The modelGateway that ISSUED this key. Its whole lifecycle
		// (revoke/regenerate/recycle/reconcile) routes by this, decoupled from
		// the organization's *current* gateway binding. The GraphQL field that
		// reads this column is `modelGateway` (a nested ModelGateway object);
		// see §2.4 in the spec.
		//
		// 2026-07 rename: gateway_connection_id → model_gateway_id. Single
		// column carries both the user-facing ModelGateway association and the
		// LLD-14 post-issue lifecycle pin. Required since per-agent-per-org
		// refactor: every VirtualKey MUST be bound to exactly one modelGateway
		// so that IssueVirtualKey can statically pin the secret-spending
		// target.
		field.UUID("model_gateway_id", uuid.UUID{}),
		field.Strings("models").Optional(),
		field.Float("max_budget").Optional(),
		field.Int("max_parallel_requests").Optional(),
		field.Int("tpm_limit").Optional(),
		field.Int("rpm_limit").Optional(),
		field.String("tpm_limit_type").Optional(), // "guaranteed_throughput" etc
		field.String("rpm_limit_type").Optional(),
		field.String("budget_duration").Optional(),
		field.Enum("status").Values("active", "disabled", "revoked").Default("active"),
		field.Time("expires_at").Optional().Nillable(),
		field.Strings("allowed_routes").Optional(),
		field.Strings("tags").Optional(),
		field.Bool("blocked").Default(false),
		field.String("key_type").Default("default"),
		field.Bool("auto_rotate").Default(false),
		field.String("rotation_interval").Optional(), // "30d" etc
		// 前端传入的 user_id;与 gateway.GenerateKeyRequest.UserID 直传。
		// NotEmpty(dev-only:历史行没这个列,prod ALTER 会失败 — 后续 prod
		// 数据迁移是另一个工作,见 spec out-of-scope)。
		field.String("user_id").NotEmpty(),
		field.Time("last_active_at").Optional().Nillable(),
		field.Int("spend").Optional().Default(0),
	}
}

func (VirtualKey) Edges() []ent.Edge {
	return []ent.Edge{
		// Soft FK on agent_id → Agent. The column stays as a plain UUID
		// field (no FK constraint) — see tenant_scope.go on the "soft
		// reference" rationale. The edge is what gives GraphQL the
		// `agent: Agent` nested-object field without a hand-rolled join.
		// "virtual_keys" is the canonical Ref name; the Agent side has no
		// matching edge (one-directional by design — see schema/agent.go
		// for the rationale on skipping the back-edge).
		edge.From("agent", Agent.Type).
			Ref("virtual_keys").
			Field("agent_id").
			Unique(),
	}
}

func (VirtualKey) Indexes() []ent.Index {
	return []ent.Index{
		// Enforce the 1:1 agent↔key invariant at the DB layer so two concurrent
		// IssueVirtualKey calls can't both mint a key for the same agent (the
		// read-then-create check in the resolver is racy). Partial: a revoked
		// key frees the agent to be re-issued; a NULL agent_id (org-level keys
		// not yet bound to an agent) is naturally unconstrained.
		index.Fields("agent_id").Unique().Annotations(entsql.IndexWhere("status <> 'revoked'")),
		// Per-tenant name uniqueness — the schema-level floor that catches
		// IssueVirtualKey's race window when the resolver-side read-then-
		// create check would otherwise let two concurrent calls mint keys
		// with the same display name. We use `model_gateway_id` as the
		// tenant proxy (every VirtualKey MUST be bound to exactly one
		// modelGateway since the 2026-07 refactor — see FieldModelGatewayID
		// above) because the platform-side `tenant_id` column is not on
		// this table; routing uniqueness by issuing gateway keeps it
		// semantically clean (keys issued by the same gateway share an
		// admin scope) while avoiding a migration just for this index.
		// Partial: a revoked key frees its name for re-use. NULL
		// model_gateway_id is naturally unconstrained (legacy rows only;
		// current issue path requires the field).
		index.Fields("model_gateway_id", "name").Unique().Annotations(entsql.IndexWhere("status <> 'revoked'")),
	}
}
