package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// RouterTier maps a difficulty tier to a model alias — the heart of the compute
// gateway (算力网关之魂): simple questions → cheap model, hard → strong model.
// The four tiers compose the litellm Complexity Router config (调研 §3.2).
type RouterTier struct {
	ent.Schema
}

func (RouterTier) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (RouterTier) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.Enum("tier").Values("SIMPLE", "MEDIUM", "COMPLEX", "REASONING"),
		field.String("model_alias").NotEmpty(), // -> ModelRoute.model_alias / Upstream.name
	}
}

func (RouterTier) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tier").Unique(),
	}
}
