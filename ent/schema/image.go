package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Image is a Harbor registry entry: OVA/container image with cosign signing
// status (LLD-06).
type Image struct {
	ent.Schema
}

func (Image) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Image) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("repository").NotEmpty(),
		field.String("tag").NotEmpty(),
		field.String("digest").Optional(),
		field.Bool("signed").Default(false),
	}
}
