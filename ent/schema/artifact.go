package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
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

// Edges: the inverse of AgentConfig.knowledge. Declaring this (non-unique on both
// ends) makes the relation many-to-many — a knowledge pack may be mounted on
// several configs (LLD-11 K2, N:M) via a join table, not stolen by an FK column.
func (Artifact) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("configs", AgentConfig.Type).Ref("knowledge"),
	}
}

func (Artifact) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty(),
		// knowledge = OKF bundle (互链 markdown 知识包, LLD-11). Read-only knowledge
		// for agent grounding (non-RAG); small single-file packs go inline like
		// config, larger bundles reference a uri.
		field.Enum("kind").Values("script", "config", "package", "knowledge"),
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
	// Unique per tenant (LLD-10 §1.7): two tenants may hold the same name+version,
	// but within one tenant — and within the platform (NULL-tenant) namespace —
	// name+version stays unique. Two partial indexes handle NULL vs non-NULL
	// (Postgres/SQLite treat NULLs as distinct, so a plain composite would let
	// platform rows duplicate).
	return []ent.Index{
		index.Fields("tenant_id", "name", "version").Unique().
			Annotations(entsql.IndexWhere("tenant_id IS NOT NULL")),
		index.Fields("name", "version").Unique().
			Annotations(entsql.IndexWhere("tenant_id IS NULL")),
	}
}
