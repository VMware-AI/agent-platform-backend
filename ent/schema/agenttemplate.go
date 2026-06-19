package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// AgentTemplate is a catalog entry (智能体市场) — an installable agent kind
// (goose/xiaoguai/qoder/...). Mirrors the C21 runcmd registry concept (doc 42).
type AgentTemplate struct {
	ent.Schema
}

func (AgentTemplate) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (AgentTemplate) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("kind").NotEmpty().Unique(), // registry key: goose/xiaoguai/qoder
		field.String("display").NotEmpty(),
		field.String("description").Optional(),
		field.Enum("install_method").Values("offline_tar", "curl", "unset").Default("unset"),
		field.String("install_command").Optional(),
		field.Enum("status").Values("active", "deferred").Default("deferred"),
		field.String("version").Optional(),
	}
}
