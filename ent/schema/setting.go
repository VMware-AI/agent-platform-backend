package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Setting is a single platform-wide key/value configuration row, editable in the
// console (LLD-13). It replaces startup envs that are really operator-entered
// config — currently `agent_user` (the OS account installed agents run as,
// substituted for {{AGENT_USER}} in catalog install commands). Keys are a small
// fixed vocabulary defined alongside the resolver (settingKey*).
type Setting struct {
	ent.Schema
}

func (Setting) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Setting) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("key").NotEmpty().Unique(),
		field.String("value").Optional(),
	}
}
