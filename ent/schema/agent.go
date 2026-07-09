package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Agent is a deployed agent instance (智能体列表). The id is the unique handle;
// the UI shows name/type/status/key/owner (0619 mockup).
type Agent struct {
	ent.Schema
}

func (Agent) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Agent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty(),
		field.String("agent_type").NotEmpty(), // = AgentTemplate.kind
		field.Enum("status").
			Values("provisioning", "running", "stopped", "exception").
			Default("provisioning"),
		field.UUID("owner_user_id", uuid.UUID{}),
		field.String("vm_ref").Optional(), // vCenter VM moRef/name
		field.UUID("config_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("virtual_key_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("resource_pool_id", uuid.UUID{}).Optional().Nillable(),
		// Catalog provenance when deployed from an OVA version (智能体市场 deploy).
		field.UUID("template_family_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("template_version_id", uuid.UUID{}).Optional().Nillable(),
		field.String("run_as_user").Optional().Default(""),
	field.String("static_ip").Optional().Default(""),
	field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("environment_id", uuid.UUID{}).Optional().Nillable(), // LLD-10 env_scope (default off)
	}
}

func (Agent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("owner_user_id"),
		index.Fields("status"),
	}
}
