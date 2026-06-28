package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// RotationCommand is a backend→daemon command with an idempotence key and a
// single-direction state machine (LLD-08 §3.3):
// pending → dispatched → acked → completed|failed (dispatched may revert to
// pending on timeout for at-least-once delivery).
type RotationCommand struct {
	ent.Schema
}

func (RotationCommand) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (RotationCommand) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		// Idempotence key the daemon dedups on (string form of id).
		field.String("command_id").NotEmpty().Unique(),
		field.UUID("agent_id", uuid.UUID{}),
		field.Enum("kind").Values("rotate_ui_password", "rotate_os_password"),
		field.Enum("status").
			Values("pending", "dispatched", "acked", "completed", "failed").
			Default("pending"),
		field.String("reason").Optional(), // max_age | manual | lifecycle:* | suspected_leak
		field.Time("dispatched_at").Optional().Nillable(),
		field.Time("acked_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		// Daemon-reported fingerprint of the new credential (not plaintext).
		field.String("result_fingerprint").Optional(),
		// Pointer to the new secret in Vaultwarden (never plaintext in DB).
		field.String("secret_ref").Optional(),
		field.String("error").Optional(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
	}
}

func (RotationCommand) Indexes() []ent.Index {
	// command_id is already unique via field .Unique().
	return []ent.Index{
		index.Fields("agent_id"),
		index.Fields("status"),
		index.Fields("agent_id", "status"),
		// At most ONE in-flight command per (agent, kind): the DB enforces the
		// "one rotation in flight" invariant so a TOCTOU between the EXISTS check
		// and the INSERT (concurrent heartbeats / a heartbeat racing a manual
		// RequestRotation) can't enqueue duplicate pending rotations. Phrased as the
		// complement of the terminal states (completed/failed) — two simple `<>`
		// clauses normalize stably on Postgres replay, unlike an IN(...) list which
		// re-expands to ANY(ARRAY[...]) and makes migrate-diff non-idempotent.
		index.Fields("agent_id", "kind").Unique().
			Annotations(entsql.IndexWhere("status <> 'completed' AND status <> 'failed'")),
	}
}
