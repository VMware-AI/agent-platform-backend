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
		// Inventory counts from the last sync (0619 第13页).
		field.Int("datacenter_count").NonNegative().Default(0),
		field.Int("cluster_count").NonNegative().Default(0),
		field.Int("host_count").NonNegative().Default(0),
		field.Int("vm_count").NonNegative().Default(0),
		// last_synced_at: when syncResourcePool last succeeded. Nil = never synced.
		// Drives the console's syncStatus (NEVER/SYNCED) + lastSyncedAt column.
		field.Time("last_synced_at").Optional().Nillable(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("environment_id", uuid.UUID{}).Optional().Nillable(), // LLD-10 env_scope (default off)
	}
}
