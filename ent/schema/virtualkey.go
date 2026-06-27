package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// VirtualKey is a per-user LiteLLM virtual key issued by the gateway (LLD-04).
// The secret itself is Sensitive (never serialized to GraphQL/logs); it is
// delivered to the VM via guestinfo. The DB row holds governance metadata.
type VirtualKey struct {
	ent.Schema
}

func (VirtualKey) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (VirtualKey) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		// The LiteLLM virtual key secret — kept out of all output.
		field.String("litellm_key").Sensitive().NotEmpty(),
		// LiteLLM's hashed token (what GET /key/list returns). Stored at issue time
		// so reconciliation matches by it; empty for keys issued before this field.
		field.String("litellm_token").Optional(),
		field.String("alias").Optional(), // display label, e.g. "alice / coding"
		field.UUID("user_id", uuid.UUID{}),
		field.UUID("agent_id", uuid.UUID{}).Optional().Nillable(),             // 绑定的智能体ID (0619 第7页)
		field.UUID("rate_limit_policy_id", uuid.UUID{}).Optional().Nillable(), // 关联策略
		field.String("team_id").Optional(),                                    // = department / litellm team
		field.Strings("models").Optional(),
		field.Float("max_budget").Optional(),
		// active=启用, disabled=禁用(可切回), revoked=已撤销(终态)
		field.Enum("status").Values("active", "disabled", "revoked").Default("active"),
		field.Time("expires_at").Optional().Nillable(),
	}
}

func (VirtualKey) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id"),
		// Enforce the 1:1 agent↔key invariant at the DB layer so two concurrent
		// IssueVirtualKey calls can't both mint a key for the same agent (the
		// read-then-create check in the resolver is racy). Partial: a revoked key
		// frees the agent to be re-issued; a NULL agent_id (user-level keys) is
		// naturally unconstrained.
		index.Fields("agent_id").Unique().Annotations(entsql.IndexWhere("status <> 'revoked'")),
	}
}
