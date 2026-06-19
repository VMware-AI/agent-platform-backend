package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// AgentConfig is a named configuration for an agent type (智能体配置). One can be
// marked default per type (system-provided). Pulls default_config from Content lib.
type AgentConfig struct {
	ent.Schema
}

func (AgentConfig) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (AgentConfig) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty(),
		field.String("agent_type").NotEmpty(), // = AgentTemplate.kind
		field.Bool("is_default").Default(false),
		field.UUID("artifact_id", uuid.UUID{}).Optional().Nillable(), // -> Artifact default_config
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
	}
}
