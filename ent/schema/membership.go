package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Membership is the user×department×role join (LLD-01 §1.4 / doc43, 多对多).
// Uses soft-reference uuid fields (no ent edges) for simplicity; uniqueness on
// (user_id, department_id) is enforced by index.
type Membership struct {
	ent.Schema
}

func (Membership) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Membership) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("user_id", uuid.UUID{}),
		field.UUID("department_id", uuid.UUID{}),
		// department-level role; platform/tenant权限 looks at User.role (判权三轨不交叉).
		field.Enum("role").Values("user", "dept-admin").Default("user"),
	}
}

func (Membership) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "department_id").Unique(),
		index.Fields("department_id"),
	}
}
