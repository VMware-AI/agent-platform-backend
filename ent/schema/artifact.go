package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Artifact is a Content-lib item: script/config/package with version + metadata
// (LLD-06). Source for agent default_config and offline install packages.
type Artifact struct {
	ent.Schema
}

func (Artifact) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Artifact) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty(),
		field.Enum("kind").Values("script", "config", "package"),
		field.String("version").NotEmpty(),
		field.String("uri").NotEmpty(),
		// Inline content for small text artifacts (config/script, ≤64K). Air-gap
		// path: the backend embeds it into cloud-init at deploy so the VM never
		// fetches it (LLD-09). Large packages keep content empty and use uri.
		field.String("content").Optional().MaxLen(65536),
		field.String("sha256").Optional(),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("environment_id", uuid.UUID{}).Optional().Nillable(), // LLD-10 env_scope (default off)
	}
}

func (Artifact) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("name", "version").Unique(),
	}
}
