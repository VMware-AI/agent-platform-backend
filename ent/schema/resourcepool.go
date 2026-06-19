package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// ResourcePool is a compute resource pool (vCenter) the platform connects to
// (LLD-06). Credentials are NEVER stored in plaintext — only a secret_ref to
// Vaultwarden (C18); the backend resolves it at connect time (LLD-03).
type ResourcePool struct {
	ent.Schema
}

func (ResourcePool) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ResourcePool) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty(),
		field.Enum("kind").Values("vcenter").Default("vcenter"),
		field.String("endpoint").NotEmpty(),
		field.Enum("status").Values("connected", "disconnected", "error").Default("disconnected"),
		// Reference into the secret store; never the credential itself.
		field.String("secret_ref").Optional(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
	}
}
