package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Membership is the user×department×role join (LLD-01 §1.4 / doc43, 多对多).
// Department-level role; platform/tenant权限 looks at User.role (判权三轨不交叉).
type Membership struct {
	ent.Schema
}

func (Membership) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (Membership) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("role").Values("user", "dept-admin").Default("user"),
	}
}

func (Membership) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("memberships").Unique().Required(),
		edge.From("department", Department.Type).Ref("memberships").Unique().Required(),
	}
}
