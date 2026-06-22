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
		// OKF knowledge-grounding convention per agent kind (LLD-11 K4, OQ-3).
		// knowledge_root = where the daemon unpacks mounted packs in the VM;
		// knowledge_prompt = the system-prompt snippet telling the agent to consult
		// the local knowledge index.md first (非 RAG: file navigation, not retrieval).
		field.String("knowledge_root").Optional(),
		field.String("knowledge_prompt").Optional(),
	}
}
