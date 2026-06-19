package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// RequestLog is an append-only gateway request record (请求日志, 0619). Written
// by the gateway/telemetry ingest; queried in the request-log UI.
type RequestLog struct {
	ent.Schema
}

func (RequestLog) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("request_id").NotEmpty(),
		field.UUID("user_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("agent_id", uuid.UUID{}).Optional().Nillable(),
		field.String("model").Optional(),
		field.Int("input_tokens").NonNegative().Default(0),
		field.Int("output_tokens").NonNegative().Default(0),
		field.Int("latency_ms").NonNegative().Default(0),
		field.Int("status_code").Default(200),
		field.Time("created_at").Immutable().Default(time.Now),
	}
}

func (RequestLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("request_id"),
		index.Fields("status_code"),
		index.Fields("created_at"),
	}
}
