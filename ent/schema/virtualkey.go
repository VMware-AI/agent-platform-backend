package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
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

func (VirtualKey) Indexes() []ent.Index {
	return []ent.Index{
		// Enforce the 1:1 agent↔key invariant at the DB layer so two concurrent
		// IssueVirtualKey calls can't both mint a key for the same agent (the
		// read-then-create check in the resolver is racy). Partial: a revoked
		// key frees the agent to be re-issued; a NULL agent_id (org-level keys
		// not yet bound to an agent) is naturally unconstrained.
		index.Fields("agent_id").Unique().Annotations(entsql.IndexWhere("status <> 'revoked'")),
	}
}
