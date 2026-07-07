package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// ProviderModel is a model-provider deployment behind the gateway (供应商模型 / per LLD-07 §2.2).
// 0.1.x: 重设计为「单行多变体 JSON」+ 强制 modelGateway 关联。
// 一个 row 装 N 个 model_spec(每个 spec 对应 litellm 一个 deployment,共享 model_name = pm.Name),
// 靠 spec.model_info.id(UUID)区分。物理上层 model_specs JSON 装 LitellmParams + ModelInfo;
// modelGateway 列指向 GatewayConnection(litellm 自己的 address)。
// apiBase(spec.litellm_params.apiBase)与 modelGateway 语义不重叠。
type ProviderModel struct {
	ent.Schema
}

func (ProviderModel) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ProviderModel) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name").NotEmpty().Unique(), // alias, e.g. tier-fast
		// 0.1.x: 后端 litellm 网关 — 该 ProviderModel 隶属哪个 GatewayConnection。
		// 与 spec.apiBase(物理上游)语义不重叠;NOT NULL 由 schema 一次性重建落地。
		// 不加 ent edge(与 modelroute.go:27 风格一致)。
		field.UUID("model_gateway_id", uuid.UUID{}),
		// 0.1.x: N 个 model_spec 的 JSON 数组,每个 spec 含 litellm_params + model_info(含 additional_prop1)。
		// worker 周期性分组聚合 4 档 status;per-spec additionalProp1 是只读派生字段。
		field.JSON("model_specs", []map[string]any{}).Optional(),
		// 0.1.x: 4 档分组聚合状态(由 provider_model_probe worker 写入,详见 §D)。
		field.Enum("status").Values("full_healthy", "partial_outage", "full_outage", "unknown").Default("unknown"),
		// 0.1.x: 用于 stale 判定 + UI "X 分钟前检查"显示。
		field.Time("last_checked_at").Optional().Nillable(),
	}
}

// Indexes — 0.1.x: GIN index on model_specs JSON column for fast spec-id lookup
// in updateProviderModelSpec / deleteProviderModelSpec / probe worker.
// Uses jsonb_path_ops operator class — 30% smaller than default jsonb_ops
// and faster for @>-style containment queries.
// See plan §D. If/when row counts grow past ~20K we may need to add a
// reverse spec table (plan §Y).
func (ProviderModel) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("model_gateway_id"),
	}
}