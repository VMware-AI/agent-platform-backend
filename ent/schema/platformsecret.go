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
//
// key_id records which encryption key sealed the password/api_key columns —
// it enables key rotation: a row can be re-encrypted with a newer active key
// without stranding older ciphertexts (which still decrypt under their
// original key, looked up by this id). Empty / "default" = legacy ciphertext
// sealed by the single SECRETS_ENCRYPTION_KEY pre-rotation feature.
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
		// Which encryption key sealed the columns above. "default" = the
		// pre-rotation single-key era. New rows written under a rotation
		// carry the active key's id; the rotation worker migrates older
		// rows in place.
		field.String("key_id").Optional().Default("default"),
	}
}

func (PlatformSecret) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("ref"),
	}
}
