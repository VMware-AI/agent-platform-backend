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
		field.Strings("upstreams").Optional(),  // upstream names in the group
		field.Enum("strategy").
			Values("simple_shuffle", "latency", "usage_v2", "least_busy", "cost").
			Default("simple_shuffle"),
		field.Bool("enabled").Default(true),
	}
}
