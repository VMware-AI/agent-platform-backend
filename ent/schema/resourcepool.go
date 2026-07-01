package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
	"github.com/google/uuid"
)

// ResourcePool is a compute resource pool (vCenter) the platform connects to
// (LLD-06). Credentials are NEVER stored in plaintext — only a secret_ref to the
// encrypted secret store; the backend resolves it at connect time (LLD-03).
type ResourcePool struct {
	ent.Schema
}

func (ResourcePool) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ResourcePool) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty(),
		field.Enum("kind").Values("vcenter").Default("vcenter"),
		field.String("endpoint").NotEmpty(),
		field.Enum("status").Values("connected", "disconnected", "error").Default("disconnected"),
		// Content library the pool deploys OVA templates from (console 接入表单).
		field.String("content_library_name").Optional().Default(""),
		// Reference into the secret store; never the credential itself.
		field.String("secret_ref").Optional(),
		// Skip vCenter TLS verification for THIS pool. Default false (verify on);
		// the operator opts in per-pool at 接入 time for air-gapped vCenters with a
		// self-signed/internal CA (LLD-13: replaces the global VCENTER_INSECURE env).
		field.Bool("insecure").Default(false),
		// vCenter inventory snapshot — the full inventory tree (DC > Cluster > Host
		// > ResourcePool, plus per-DC datastores / networks / vm folders / storage
		// policies). Synced by the background ticker; consumed by the OVA deploy
		// form to power cascading dropdowns. Storage policy sub-list may be nil
		// when PBM pull failed (frontend distinguishes null vs []). See B5: Go
		// type lives in internal/vcenter; ent imports it for serialization.
		field.JSON("inventory", []vcenter.DataCenter{}).Optional().Default([]vcenter.DataCenter{}),
		// last_synced_at: when syncResourcePool last succeeded. Nil = never synced.
		// Drives the console's syncStatus (NEVER/SYNCED) + lastSyncedAt column.
		field.Time("last_synced_at").Optional().Nillable(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("environment_id", uuid.UUID{}).Optional().Nillable(), // LLD-10 env_scope (default off)
	}
}
