package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Upstream is a model-provider deployment behind the gateway (多上游接入).
// One of these maps to a litellm model_list entry. api_key is a Vaultwarden ref.
type Upstream struct {
	ent.Schema
}

func (Upstream) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Upstream) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty().Unique(), // alias, e.g. tier-fast
		field.Enum("provider").Values("vllm", "openai", "anthropic", "minimax", "codex"),
		field.String("api_base").Optional(),
		field.String("api_key_ref").Optional(), // vault://item-id
		field.String("model").NotEmpty(),       // real upstream model name
		field.Bool("enabled").Default(true),
	}
}
