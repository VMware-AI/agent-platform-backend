package schema

import (
	"entgo.io/ent"
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
		field.String("alias").Optional(), // display label, e.g. "alice / coding"
		field.UUID("user_id", uuid.UUID{}),
		field.String("team_id").Optional(), // = department / litellm team
		field.Strings("models").Optional(),
		field.Float("max_budget").Optional(),
		field.Enum("status").Values("active", "revoked").Default("active"),
		field.Time("expires_at").Optional().Nillable(),
	}
}

func (VirtualKey) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id"),
	}
}
