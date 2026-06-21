package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// AgentEnrollment is the VM-credential lifecycle for one agent (LLD-08 §3.1):
// pending (enroll_token issued) → active (vm_token in use) → revoked. Tokens are
// stored as bcrypt fingerprints, never plaintext (same model as User.password_hash).
type AgentEnrollment struct {
	ent.Schema
}

func (AgentEnrollment) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (AgentEnrollment) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		// Soft reference (no FK) — one enrollment per agent.
		field.UUID("agent_id", uuid.UUID{}).Unique(),
		// Stable identifier injected via guestinfo at deploy.
		field.String("vm_id").NotEmpty().Unique(),
		field.Enum("status").Values("pending", "active", "revoked").Default("pending"),
		// bcrypt fingerprint of the one-time enroll token; cleared after exchange.
		field.String("enroll_token_hash").Optional().Sensitive(),
		field.Time("enroll_expires_at"),
		// bcrypt fingerprint of the long-lived VM bearer token; never plaintext.
		field.String("vm_token_hash").Optional().Sensitive(),
		field.Time("vm_token_issued_at").Optional().Nillable(),
		field.Time("vm_token_expires_at").Optional().Nillable(),
		field.Time("last_seen_at").Optional().Nillable(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
	}
}

func (AgentEnrollment) Indexes() []ent.Index {
	// vm_id and agent_id are already unique via field .Unique(); only the
	// non-unique status index needs declaring here.
	return []ent.Index{
		index.Fields("status"),
	}
}

// AgentHeartbeat is an append-only health time-series; one row per heartbeat
// (LLD-08 §3.2). No updated_at (immutable, like AuditLog); rolling retention is
// handled out of band (90-day cleanup job).
type AgentHeartbeat struct {
	ent.Schema
}

func (AgentHeartbeat) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("agent_id", uuid.UUID{}),
		field.Time("reported_at"),                               // daemon clock
		field.Time("received_at").Immutable().Default(time.Now), // backend clock
		field.Enum("status").Values("ok", "degraded", "error"),
		field.String("agent_version").Optional(),
		field.Enum("rotation_state").Values("idle", "rotating", "failed").Optional(),
		field.JSON("detail", map[string]any{}).Optional(),
	}
}

func (AgentHeartbeat) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("agent_id"),
		index.Fields("received_at"),
	}
}
