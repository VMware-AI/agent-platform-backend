package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// PlatformSecret is the platform's single persistent credential store, used in
// every environment. The password and api_key columns hold AES-256-GCM ciphertext
// (sealed by internal/secrets.DBStore under SECRETS_ENCRYPTION_KEY), never
// plaintext; username is stored in clear for operability. Credentials survive
// backend restarts (this replaced the in-memory StaticResolver and the external
// Vaultwarden dependency).
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
