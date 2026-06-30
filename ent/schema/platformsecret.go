package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// PlatformSecret is a persistent key-value credential store for dev / air-gapped
// deployments where Vaultwarden is not available. It replaces the in-memory
// StaticResolver so credentials survive backend restarts.
//
// Production deployments with Vaultwarden configured bypass this table entirely.
type PlatformSecret struct {
	ent.Schema
}

func (PlatformSecret) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (PlatformSecret) Fields() []ent.Field {
	return []ent.Field{
		field.String("ref").Unique().NotEmpty(),
		field.String("username").Optional().Default(""),
		field.String("password").Optional().Default("").Sensitive(),
		field.String("api_key").Optional().Default("").Sensitive(),
	}
}

func (PlatformSecret) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("ref"),
	}
}
