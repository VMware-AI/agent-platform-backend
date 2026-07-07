# Plan: 重设计 ProviderModel 为 modelSpecs JSON + 接入 /model/new 同步推送

## Context

`provider_model.graphql` 当前是个**单行多字段** 的 ProviderModel — 一个 provider 行带 30+ 个字段(`model`/`tpm`/`rpm`/`tags`/`blocked`/`inputCostPerToken`/...),实际只对应**一个** litellm model_list 部署。用户重设计为 **1 个 ProviderModel 行装 N 个 model_spec**(每个 spec 一个 litellm 部署),`CreateProviderModel` 写入后端后,**同步**对每个 spec 调用 `POST /model/new`,失败报错。

主要动机: litellm 那边用 `model_name` 作为 deployment 唯一键。用户新结构让一个 provider(共享 api_base/api_key)在 litellm 中产生多个同名部署,彼此靠 `model_info.id`(后端 UUID)区分。

## 用户决策摘要(已与你确认)

| 议题 | 决策 |
|---|---|
| ProviderModel 存储 | **单行多变体 JSON**(`model_specs` JSON 数组) |
| 同步策略 | 同步 /model/new,**任一失败报错** |
| model_name | 全部 spec 同名 = `provider.name`,UUID 在 `model_info.id` 区分 |
| provider 枚举 | 最终 7 值:`custom` / `deepseek` / `minimax` / `moonshot` / `openrouter` / `openai` / `anthropic`(`custom` 首位,OpenRouter 在 openai 前) |
| tier | **完全去掉**;`ModelInfo` 不再有 `tier` 字段,`ProviderTier` enum 删除 |
| ProviderModel 形态 | **精简为 6 字段**: `id / name / status / createdAt / updatedAt / modelSpecs`(**去掉 `provider` 字段**) |
| 顶层 `CreateProviderModelInput` | 只剩 `name + modelSpecs` — **`provider` 和 `enabled` 都去掉**(provider 从 spec.litellm_params 取,enabled 字段删除) |
| apiKeyRef | **`LitellmParams` 中没有 `apiKeyRef` 字段**,只有 `apiKey`(明文输入);resolver 走 `secret_store.Put(label="provider_model/<name>/<spec.model>", key=...)` 写加密存储,ent JSON **只存 ref 字符串**(不存明文)。返回值同样只返 ref,不返明文。 |
| 同步字段粒度 | model_spec 独立 ID,createProviderModel 整体 setReplace |
| 数据迁移 | **不需要 Atlas data migration**;只做 schema-only 迁移(新增 model_specs 列 + 删老列)。**老 DB 升级时老 provider 行会丢失 model/tpm/... 字段** — 这是用户接受的语义损失 |
| 增删改查扩展 | 新增独立 model_spec 的 mutation 集合 — 详见第 6 节 |

## 关键文件

### 1. GraphQL Schema — `schema/provider-model.graphql` (重写)

新增 `LitellmParamsInput` / `LitellmParams`、`ModelInfoInput` / `ModelInfo`、`ModelSpecInput` / `ModelSpec` 类型。`ProviderModelProvider` enum 最终 7 值(custom / deepseek / minimax / moonshot / openrouter / openai / anthropic);**没有 tier 字段**(`ProviderTier` enum 删除)。`ProviderModel` 精简为 **6 字段**(`id / name / status / createdAt / updatedAt / modelSpecs` — `provider` 字段去掉,从 spec.litellm_params 取)。`CreateProviderModelInput` 改成 `name + modelSpecs`(只 2 个字段,**`provider` 和 `enabled` 都去掉**)。

完整新 schema 字段映射:

```graphql
# Provider-level 下拉约束(也是 LitellmParamsInput.customLlmProvider 的字面量集合)
enum ProviderModelProvider {
  custom       # 任意 OpenAI 兼容端点
  deepseek     # DeepSeek
  minimax      # MiniMax
  moonshot     # Moonshot(月之暗面)
  openrouter   # OpenRouter(多模型聚合)
  openai
  anthropic
}

type LitellmParams {
  # apiKey 明文输入 → resolver 写 secret store → 仅 ref 持久化在 ent JSON
  apiKey: String         # 入参明文,resolver 内部用 secret-store-minted 明文替代;返回值 null(filter)
  apiKeyRef: String      # wire 返的字段(服务端 secret store ref id,明文永不出库)
  apiBase: String
  model: String!
  # 注:字段类型为 String,接受 customLlmProvider 的任何字符串(包括 CUSTOM 上任意 URL base 的 openai 兼容商)
  customLlmProvider: String   # 默认 "openai"
  organization: String
  tpm: Int
  rpm: Int
  defaultApiKeyTpmLimit: Int
  defaultApiKeyRpmLimit: Int
  maxBudget: Float
  budgetDuration: String
  useInPassThrough: Boolean!
  useLitellmProxy: Boolean!
  useChatCompletionsApi: Boolean!
  mergeReasoningContentInChoices: Boolean!
  tags: [String!]!
  # Cost 字段:wire Float(per-token),DB 存 Float,resolver 不再"÷1e6",前端直传
  inputCostPerToken: Float
  outputCostPerToken: Float
  cacheReadInputTokenCost: Float
  cacheCreationInputTokenCost: Float
}

type ModelInfo {
  id: ID!                      # 后端 UUID
  mode: String
  blocked: Boolean!
}

type ModelSpec {
  litellmParams: LitellmParams!
  modelInfo: ModelInfo!
}

input LitellmParamsInput { ... }   # 与 LitellmParams 同形但字段全 Optional(除 model)
input ModelInfoInput { ... }       # id 可选(省略 = 后端生成)
input ModelSpecInput {
  litellmParams: LitellmParamsInput!
  modelInfo: ModelInfoInput = { blocked: false }   # 默认值
}

# ProviderModel 精简为 6 字段(去掉 provider)
type ProviderModel {
  id: ID!
  name: String!
  status: ProviderModelStatus!
  createdAt: Time!
  updatedAt: Time!
  modelSpecs: [ModelSpec!]!
}

# CreateProviderModelInput 只剩 2 个字段
input CreateProviderModelInput {
  name: String!
  modelSpecs: [ModelSpecInput!]!   # min 1
}

# 新增 model_spec 独立增删改查的 mutation 入参(input 全部以 ID 寻址)
input AddProviderModelSpecInput {
  providerModelId: ID!
  spec: ModelSpecInput!
}

input UpdateProviderModelSpecInput {
  specId: ID!                       # 由服务端生成(spec modelInfo.id),前端只持 ID
  litellmParams: LitellmParamsInput!
  modelInfo: ModelInfoInput!
}

# 新增:整条 ProviderModel 全量替换入参
input UpdateProviderModelInput {
  providerModelId: ID!
  modelSpecs: [ModelSpecInput!]!   # min 1,完整 setReplace
}

input ProviderModelSpecIdInput {
  specId: ID!
}

input ProviderModelSpecBlockInput {
  specId: ID!
  blocked: Boolean!
}

# 查询详情返回类型(分页 wrapper;与 ProviderModel 实体本身区分)
type ProviderModelInfoConnection {
  data: [ProviderModel!]!
  total_count: Int!
  current_page: Int!
  total_pages: Int!
  size: Int!
}

# providerModels 的 sort/filter 入参(分页查询用)
enum ProviderModelInfoSortField {
  NAME
  CREATED_AT
  UPDATED_AT
}

input ProviderModelInfoSort {
  field: ProviderModelInfoSortField!
  direction: SortDirection!
}

input ProviderModelInfoFilterInput {
  search: String
  status: ProviderModelStatus
}

extend type Query {
  providerModelInfo(
    filter: ProviderModelInfoFilterInput,
    page: PageInput!,
    sort: ProviderModelInfoSort
  ): ProviderModelInfoConnection! @hasRole(any: [admin])
}

extend type Mutation {
  createProviderModel(input: CreateProviderModelInput!): ProviderModel! @hasRole(any: [admin])
  deleteProviderModel(id: ID!): Boolean! @hasRole(any: [admin])
  # 单 spec 增删改查
  addProviderModelSpec(input: AddProviderModelSpecInput!): ProviderModel! @hasRole(any: [admin])
  updateProviderModelSpec(input: UpdateProviderModelSpecInput!): ProviderModel! @hasRole(any: [admin])
  updateProviderModel(input: UpdateProviderModelInput!): ProviderModel! @hasRole(any: [admin])
  deleteProviderModelSpec(input: ProviderModelSpecIdInput!): ProviderModel! @hasRole(any: [admin])
  blockProviderModelSpec(input: ProviderModelSpecBlockInput!): ProviderModel! @hasRole(any: [admin])
  # 原有 probe / refresh
  testProviderConnection(name: String!): ProviderModelStatus! @hasRole(any: [admin])
  refreshProviderModelStatus(id: ID!): ProviderModel! @hasRole(any: [admin])
}
```

> 关键变化:
> - **`provider` enum 加 `CUSTOM`**:对应任意 OpenAI 兼容端点;resolver 收到 `CUSTOM` 时强制要求每个 spec 的 `LitellmParams.apiBase`。
> - **没有 `ProviderTier` enum,没有 `tier` 字段**:彻底删除。
> - **顶层去 `apiBase` / `apiKeyRef` / `apiKey`**:完全下沉到 `LitellmParams`;每个 spec 自带 endpoint。
> - **apiKey 三段式流动**(LitellmParams):
>   1. **入参**:`LitellmParamsInput.apiKey` 是明文(同 provider 不同 spec 可填不同 key)
>   2. **写盘**:`secret_store.Put(label="provider_model/<name>/<spec.model>",key=apiKey)` → 拿 `ref` (vault://...),把 ref 入库到 ent `model_specs[i].litellm_params.api_key_ref`
>   3. **取用**:`CreateProviderModel` 写 ent 前完成 secret 写入;`provider_model_probe` / `model/new` 调用前 resolver 解析 ref 拿到明文再发请求
>   4. **返回值**:`LitellmParams.apiKey` 字段在 wire 上始终 `null`(filter 掉明文),返 `apiKeyRef` 即可。

### 2. ent Schema — `ent/schema/provider_model.go` (重写)

**`provider` 列删除**(从 spec.litellm_params.customLlmProvider 推;provider 整条语义迁入 JSON 内)。`apiBase / apiKeyRef / enabled` 也删除。剩 ent 列:**`id, name, status, created_at, updated_at, model_specs`**。老 30 个字段(model/custom_llm_provider/tpm/rpm/tags/mode/tier/blocked/input_cost_per_token/...)全部删除;新增 `model_specs` JSON 列(ent `field.JSON`)。`status` enum 保留(active/degraded/melted/unknown)。

需要 atlas 迁移,**只做 schema-only**:`ADD COLUMN model_specs` + `DROP COLUMN x` for 老 30 个字段。**不做数据迁移 SQL** — 老 DB 升级时老 provider 行的 model/tpm/... 字段会丢失(语义损失已签)。

### 3. GraphQL Resolver — `internal/graph/provider-model.resolvers.go` (重写)

`CreateProviderModel`(**纯 insert**,重名报错 — 不再做 upsert):
0. **重名检测**:`r.Ent.ProviderModel.Query().Where(providermodel.Name(input.Name)).Only(ctx)`,`Only` 命中 → 返 GraphQL 错 `"provider_model %q already exists; use updateProviderModel to mutate"`,强制前端走 `updateProviderModel`(input.providerModelId + 新整组 spec)路径
1. **校验顶层**:`provider == CUSTOM` 时,至少一个 spec 必须有 `apiBase`。
2. **secret store 预下料**:对**每个 spec 单独调** `secret_store.Put(label="provider_model/" + input.Name + "/" + spec.litellmParams.model, key=apiKey)`(spec-by-spec,允许同 provider 不同 model 用不同 key)。**入参明文不能落到 ent JSON**;`apiKeyRef` 写到 spec 里。
   - 如果 spec 的 `LitellmParamsInput.apiKey` 为空 → 报错"spec apiKey required"(仅首次创建不允许,updateProviderModel 在 put failed 时可空)
3. `r.Ent.ProviderModel.Create().SetName(...).SetModelSpecs(jsonSlice).Save(ctx)` — 纯创建路径,无 update 分支
4. **同步循环**对每个 spec 调 `mm.NewModel(spec)`,**model_name 全部 = `provider.name`**;`LitellmParams.apiBase` 优先用 spec 自己的,否则报错(spec 不带 apiBase 又 provider=CUSTOM 是错误状态)。`apiKey` resolver 内部用从 `apiKeyRef` 解析的明文替代。失败:log + 跳到 **回滚 step**。
5. **回滚**(任一失败时) — 对前面已成功调 `/model/new` 的 spec 调 `DELETE /model/delete {id: spec.modelInfo.id}`(best-effort,失败只 log)。最后返 GraphQL 错误,**删除 ent 行**(回滚 CREATE)+ **删除刚才 mint 的 secret refs**(避免残留孤儿);前端可重新调 `CreateProviderModel` 重试

**`CUSTOM` 校验**:`provider == CUSTOM` 时,每个 spec 必须带 `LitellmParams.apiBase`;否则 resolver 报错。provider 是 `openai`/`anthropic` 等预定义时,apiBase 是可选。

`DeleteProviderModel`:
1. 读 `model_specs` JSON,记下每个 `model_info.id` + 每个 spec 的 `litellm_params.api_base`(拿 endpoint 用于调 `/model/delete`,或通过 `mm.ListModels(provider 第一个 api_base 的 gateway)` 过滤)
2. 删 ent 行
3. 对每个 spec **同步** `mm.DeleteModel(model_info.id)`(失败仅 log — 已知孤儿,operator 可在 litellm 端清理)

`testProviderConnection(name)`:读 ent 行,**任一 spec 的 `litellm_params.apiBase`** 都用作 probe endpoint。`ListModels` 探测成功 → `status=active`。
`refreshProviderModelStatus(id)`:同上。
`patchProviderModel(id, blocked)`:**删除** — `model_info.blocked` 现在通过新 mutation `blockProviderModelSpec(specId, blocked)`(见 9.4 节)单独 setReplace 单 spec。

### 4. Probe — `internal/graph/provider_model_probe.go` (重写)

`probeOneProviderModel(u *ent.ProviderModel)` 改为:
- `u` 不再有 `APIBase` / `APIKeyRef` 列;从 `u.ModelSpecs[0].LitellmParams.apiBase` + `u.ModelSpecs[0].LitellmParams.apiKeyRef` 读(单个 spec 足够,probe 用任一 endpoint 即可,因为同 provider 都在一个 litellm 主机上)
- **支持 `CUSTOM` provider**:resolver 把第一个非空 `apiBase` 当成 litellm 端点;如果是 `openai` 等预定义,fallback 用 litellm 默认 URL
- `ListModels()` 探测成功 → row.status = active,失败 → melted
- 不再依赖单 spec 字段

### 5. Wire ModelSpec JSON → gateway.ModelSpec

`internal/gateway/models.go` 中的 `ModelSpec` wire 类型(`ModelName/Model/APIBase/APIKey/Tags/.../ModelID`)**直接复用**。resolver 将 spec-level 字段映射过去:`ModelName = provider.name`,`Model = spec.litellmParams.model`,`APIBase = spec.litellmParams.apiBase`,`APIKey = secret-store-minted 明文(从 spec.litellmParams.apiKeyRef 解析)`,`ModelID = spec.modelInfo.id`(让 litellm 的 `model_info.id` 与我们 ent 里持久化的 UUID 一致,这样 `DELETE /model/delete {id: <UUID>}` 能一对一命中)。

### 6. 删 `internal/graph/provider_model_upsert.go`

`applyCreateProviderModelFields` / `updateCreateProviderModelFields` / `costPerToken` 全部不再使用,文件删除。

### 7. 删除字段级 helper

- `input.outputCostPerToken` 改名为 `outputCostPerToken` 等已经是 GraphQL Float(per-token)— DB 也存 Float,**不需要** `÷1e6`。原 `costPerToken()` 函数删除。

### 8. 输入转换 `internal/graph/model_spec.go` (新增)

`buildModelSpecs(spec)` 把 GraphQL `ModelSpecInput` 转为:
- JSON 入库:`map[string]any`(每个 spec 一份,塞进 ent JSON 数组)
- litellm `gateway.ModelSpec`:`model_name = provider.name`,`model_info.id = spec.modelInfo.id`

需要导出 4 个 helper:
- `specToJSON(spec ModelSpecInput) map[string]any`(生成 `litellm_params + model_info` JSON,`apiKey` 字段置 `__newsecret__:<base64>` 占位,留 secret store 写入后 setReplace 为 `<ref>` 字符串)
- `specToLitellmModelSpec(providerName string, spec ModelSpecInput, secretPlain string) gateway.ModelSpec`
- `litellmSpecDeleteID(spec ModelInfo) string` — 返回 `spec.id`(UUID),用 `/model/delete`
- `parseSpecFromJSON(raw []byte) ([]ModelSpec, error)` — 反序列化 ent JSON

### 9. 新增独立 model_spec CRUD mutation

`CreateProviderModel` 是 **整组** setReplace(改了 N 条就上 litellm 调 N 次)。但运维场景常常是"给一个 provider 加一个 model"或"删一个"——不应触发整组重写。所以**新增 4 个针对单 spec 的 mutation**,支持细粒度操作:

| GraphQL 字段 | 用途 |
|---|---|
| `addProviderModelSpec(input: AddProviderModelSpecInput!): ProviderModel!` | 给指定 provider 添加一条 model_spec,调一次 `POST /model/new` 上 litellm |
| `updateProviderModelSpec(input: UpdateProviderModelSpecInput!): ProviderModel!` | **(改名为 `UpdateProviderModelSpecBySpecId`,按 `specId` 改一条 spec),调 litellm `PATCH /model/{spec.modelInfo.id}/update` |
| `updateProviderModel(input: UpdateProviderModelInput!): ProviderModel!` | **(新)**按 `providerModelId`**整条 provider** 全量重置 model_specs,调 litellm `POST /model/update` 全量同步 |
| `deleteProviderModelSpec(input: ProviderModelSpecIdInput!): ProviderModel!` | 按 `specId` 删除一条 spec,调 `DELETE /model/delete {id}`,再 setReplace JSON |
| `blockProviderModelSpec(input: ProviderModelSpecBlockInput!): ProviderModel!` | 单独 setReplace `model_info.blocked`;**不调 litellm**(数据面仍可见 blocked 字段,console toggle 立即生效,但 litellm 后台依据 model_info.blocked 决定对外可见性,**TODO** 未来调 PATCH 同步) |
| `providerModelInfo(filter: ProviderModelInfoFilterInput, page: PageInput!, sort: ProviderModelInfoSort): ProviderModelInfoConnection!` | **(新)分页查询 provider_model 表**,返回 `{ data: [ProviderModel!], total_count, current_page, total_pages, size }` |

#### 9.1 `addProviderModelSpec(input)`

入参:
- `providerModelId: ID!`(find the row)
- `spec: ModelSpecInput!`(新 spec,modelInfo.id 可选)

**resolver 流程**:
1. `r.Ent.ProviderModel.Get(ctx, parsedID)` — 找不到返 GraphQL 错
2. 解析 spec.litellmParams.apiKey → secret store (label=`provider_model/<name>/<spec.model>`);已存在的 ref 同 label Put 覆盖;存到内存
3. JSON setReplace:旧的 `model_specs`(数组) + 新一条 spec 拼回去
4. **同步** `mm.NewModel(...)` 调 `POST /model/new`(只调一次,不是整组)— model_name = provider.name,model_info.id = spec.modelInfo.id(没传则后端生成 UUID)
5. 失败:best-effort `DELETE /model/delete {id: 新 spec.modelInfo.id}`(如果 spec 已上 litellm 部分成功);从 JSON 中移除该 spec;返错
6. 成功:`r.Ent.ProviderModel.UpdateOneID(id).SetModelSpecs(newJSON).Save(ctx)`;审计 `provider_model.add_spec`;返回更新后的 ProviderModel row

#### 9.2 `updateProviderModelSpec(input)`(原名;**按 `specId` 单 spec 修改**)

入参:
- `specId: ID!`(model_info.id,UUID)
- `litellmParams: LitellmParamsInput!`(完整替换 litellm_params)
- `modelInfo: ModelInfoInput!`(完整替换 model_info,**`id` 必须等于 specId**;其他字段 mode/blocked 也覆盖)

**resolver 流程**:
1. `r.Ent.ProviderModel.Query().Where(<model_specs JSONB 含该 model_info.id>)` — ent 不支持 JSON 查询中的 UUID 过滤,所以**拉所有 ProviderModel 行** 在内存中 grep 找到含此 specId 的行(M-size < 200 假设 O(1) 耗时可以接受)
2. 找不到 specId → 返 GraphQL 错 "spec not found"
3. **校验**:`modelInfo.id` 必须等于入参 `specId` — 不一致报错("spec id mismatch")
4. 处理 apiKey:同上(Put secret store with same label,return ref)
5. setReplace model_specs:替换匹配的 spec 对象,**整组其他 spec 不变**
6. **同步** PATCH 到 litellm:`gateway.AdminClient.PatchModel(ctx, litellmSpec)` — `PATCH /model/{spec.modelInfo.id}/update`
7. 失败:setReplace 已经在 ent 写了;**回滚**:用老的 spec 重 PATCH 一次(best-effort,失败只 log);返错
8. 成功:返 ProviderModel

> 注:litellm 的 `PATCH /model/{id}/update` 接收的是**当前 ModelSpec 字段**(litellm_params 部分可以局部更新,model_info 也是)。resolver 用整组 litellm_params + model_info 提交即可。

#### 9.3 `updateProviderModel(input)`(新 — 整条 provider 全量重置)

入参:
- `providerModelId: ID!`(找 row)
- `modelSpecs: [ModelSpecInput!]!`(完整替换整组 spec,min 1)

**resolver 流程**:
1. 拉 ProviderModel row;找不到 → 返错
2. **不调 `/model/new` 一条条** — `POST /model/update` 在 litellm 是 **bulk end point**,接受一组 spec;一次调用全量覆盖该 provider name 在 litellm 上的部署
3. 按 litellm `/model/update` 的 wire 规则拼 body:对每个 spec 调 `mm.NewModel`-like struct,**model_name = provider.name**,**model_info.id = spec.modelInfo.id**;POST body 用 `gateway.AdminClient.PushModelUpdate(ctx, providerName, []gateway.ModelSpec)`,内部走 `POST /model/update`
4. setReplace model_specs JSON(整体)
5. 失败:best-effort 重拉老 spec 走 `POST /model/update` 一次(若老 spec 在 litellm 存在)— 若回滚也失败,log 孤儿,ent 已写
6. 成功:返 ProviderModel

> **litellm `/model/update` 与 `/model/new` 差异**:`/model/update` 是**全量替换**该 group/model_name 下所有 deployments;调它之后**不在 body 里的 spec 在 litellm 端删除**。所以这个 mutation 必须保证 body 是「该 provider 的最终 spec 集合」,否则会**静默删 litellm 上有但我们忘的 deployment**。

### 实现细节:`ProviderModelInfo` 分页查询

**核心决策**:新 query **只读 ent `provider_models` 表**,不调 litellm(`/model/info` 旧 schema 的 `GetGroupedModelInfo` 仍保留在 gateway package 但本次不暴露到 GraphQL)。**不为每个 ProviderModel 多发异步 litellm 请求** — 分页列表只返 ent 状态,健康状态 (`status` 列) 已由 `provider_model_probe` 周期 worker 维护。

#### GraphQL schema(pagination 风格与 `modelGateways` / `resources` 一致)

```graphql
# 分页输入 — 复用 schema.graphql 共享的 PageInput
# (limit/offset 默认 50/0)

enum ProviderModelInfoSortField {
  NAME
  CREATED_AT
  UPDATED_AT
}

input ProviderModelInfoSort {
  field: ProviderModelInfoSortField!
  direction: SortDirection!
}

input ProviderModelInfoFilterInput {
  # 按 ProviderModel.name 模糊匹配
  search: String
  # 按 status enum 过滤(active / degraded / melted / unknown)
  status: ProviderModelStatus
}

# provider-model.md 中展示给前端的实体结构
type ProviderModelInfoConnection {
  data: [ProviderModel!]!
  total_count: Int!
  current_page: Int!
  total_pages: Int!
  size: Int!               # = page.limit
}
```

> 这与 `ProviderModelConnection`(modelgateway 接口面)在命名上**故意区分**:保持 `ProviderModel` GraphQL 主实体单一来源,`...Info` 后缀查询接口携带 pagination metadata。

#### resolver 实现:`providerModelInfo`

**入参**:
```graphql
providerModelInfo(
  filter: ProviderModelInfoFilterInput,
  page: PageInput!,
  sort: ProviderModelInfoSort
): ProviderModelInfoConnection!
```

**流程**:
1. 起始 base query:`r.Ent.ProviderModel.Query()`
2. **filter 应用**:
   - `filter.search` → `Where(providermodel.NameContainsFold(filter.search))`(ent 字段类型 `Filter`)
   - `filter.status` → `Where(providermodel.StatusEQ(providermodel.Status(filter.status)))`
3. **sort 应用**(复用类似 `applyModelGatewaySort` 的实现,但无额外 sort 字段沿用 `orderNewest`):
   - `sort.field == NAME` → `Order(Asc(name))`
   - `sort.field == CREATED_AT` → `Order(Desc(created_at))`(默认)
   - `sort.field == UPDATED_AT` → `Order(Desc(updated_at))`
4. **paginate**:
   - `count` 走一次 `base.Clone().Count(ctx)` — ent 原生高效
   - `data` 走 `applySort(base.Clone(), sort).Limit(page.limit).Offset(page.offset).All(ctx)`
5. **元数据换算**:
   - `current_page = floor(offset / size) + 1`(size 0 → 1)
   - `total_pages = ceil(count / size)`,size 0 时 total_pages 为 1
   - `size = page.limit`(默认 50)
6. 返 `&model.ProviderModelInfoConnection{Data, TotalCount, CurrentPage, TotalPages, Size}`

#### ent 谓词:补 `NameContainsFold`

此谓词 ent 默认**没有** — `ent generate` 不为 provider_models 生成 `ContainsFold`/`Fold` 等模糊匹配谓词。检查后生成(若缺失)在 `ent/provider_model/where.go` 内补一条:

```go
func NameContainsFold(v string) predicate.ProviderModel {
    return predicate.ProviderModel(sql.FieldContainsFold(FieldName, v))
}
```

> 这个谓词是 `ent` 的 `sql.ContainFold` 包装,默认支持(无需 `sql/modifier` feature flag);`go generate ./ent/...` 后会出现在 `ent/provider_model/where.go` 中。

### 9.7 gateway package 接口扩展

新增 2 个 litellm AdminClient 方法(为 **`updateProviderModel`**):
- `PatchModel(ctx, specID string, partial map[string]any) error` — 已有,`PATCH /model/{id}/update`
- **`PushModelUpdate(ctx, providerName string, specs []gateway.ModelSpec) error`**(新)— `POST /model/update`;body 结构:`{"router_settings":{...}, ...}` 或简单的 `{"models": [spec1, spec2, ...]}`。**wire 形态待实现期读 litellm 源码或文档敲定**。

> 注:`GET /v2/model/info` 端点 + `GetGroupedModelInfo` 暂不暴露到 GraphQL — 单独留作 `gateway` package 内部 tool,需要时再 wire。本次只走 ent 分页查询。

#### 9.3 `deleteProviderModelSpec(input)`

入参:
- `specId: ID!`

**resolver 流程**:
1. 遍历所有 ProviderModel 行找到含 specId 的行(同 9.2 内存 grep)
2. 找不到 → 返 GraphQL 错 "spec not found"
3. **同步** `mm.DeleteModel(ctx, specId)`(`POST /model/delete {id: specId}`)— 失败**也继续**(孤儿)
4. 内存移除该 spec,setReplace model_specs JSON
5. 删除该 spec 的 secret store ref(用 `deleteSecretRef`)
6. 返 ProviderModel

> 注:`enabled` 字段已删除,所以"删除整条 provider"等同于"删除所有 spec 后删 row"。

#### 9.4 `blockProviderModelSpec(input)`

入参:
- `specId: ID!`
- `blocked: Boolean!`

**resolver 流程**:
1. 同 9.2 找到 spec
2. in-memory replace 该 spec 的 `model_info.blocked`
3. setReplace model_specs
4. **不调 litellm**;**TODO 未来调 PATCH `/model/{id}/update`**(本次简化 — 用户接受)
5. 返 ProviderModel

### 10. Atlas Migration(schema-only)

`ent/migrate/migrations/20260704000004_provider_model_specs.sql`:**纯 DDL,不带数据迁移**。老 DB 升级时老 provider 行的 model/tpm/api_base/api_key_ref/... 等字段会被丢弃;运维人员应手动 re-upsert 所有 ProviderModel。

```sql
-- Add new column
ALTER TABLE "provider_models" ADD COLUMN "model_specs" jsonb NULL;
-- Drop columns moved into model_specs JSON
ALTER TABLE "provider_models" DROP COLUMN "model";
ALTER TABLE "provider_models" DROP COLUMN "custom_llm_provider";
ALTER TABLE "provider_models" DROP COLUMN "organization";
ALTER TABLE "provider_models" DROP COLUMN "tpm";
ALTER TABLE "provider_models" DROP COLUMN "rpm";
ALTER TABLE "provider_models" DROP COLUMN "default_api_key_tpm_limit";
ALTER TABLE "provider_models" DROP COLUMN "default_api_key_rpm_limit";
ALTER TABLE "provider_models" DROP COLUMN "max_budget";
ALTER TABLE "provider_models" DROP COLUMN "budget_duration";
ALTER TABLE "provider_models" DROP COLUMN "use_in_pass_through";
ALTER TABLE "provider_models" DROP COLUMN "use_litellm_proxy";
ALTER TABLE "provider_models" DROP COLUMN "use_chat_completions_api";
ALTER TABLE "provider_models" DROP COLUMN "merge_reasoning_content_in_choices";
ALTER TABLE "provider_models" DROP COLUMN "tags";
ALTER TABLE "provider_models" DROP COLUMN "mode";
ALTER TABLE "provider_models" DROP COLUMN "blocked";
ALTER TABLE "provider_models" DROP COLUMN "input_cost_per_token";
ALTER TABLE "provider_models" DROP COLUMN "output_cost_per_token";
ALTER TABLE "provider_models" DROP COLUMN "cache_read_input_token_cost";
ALTER TABLE "provider_models" DROP COLUMN "cache_creation_input_token_cost";
-- 顶层 apiBase / apiKeyRef / enabled 下沉到 model_specs JSON,列删除
ALTER TABLE "provider_models" DROP COLUMN "api_base";
ALTER TABLE "provider_models" DROP COLUMN "api_key_ref";
ALTER TABLE "provider_models" DROP COLUMN "enabled";
-- provider 列删除(provider 完全迁入 spec.litellm_params.customLlmProvider)
ALTER TABLE "provider_models" DROP COLUMN "provider";
-- 注:provider_model_provider enum 类型保留(ent 内部代码用了,即使无表列引用)
```

**没有数据迁移 UPDATE**:运维手动 re-upsert。release notes 必须告知。

### 11. 删除 `internal/graph/testdata/client_operations/*.graphql` 老 ops + 新增

老 `CreateProviderModel.graphql` / `DeleteProviderModel.graphql` / `PatchProviderModel.graphql` / `RefreshProviderModelStatus.graphql` / `TestProviderConnection.graphql` 等 — 都需要重写以适应新 schema(或者直接删除 + 重新生成)。**新增** `AddProviderModelSpec.graphql` / `UpdateProviderModelSpec.graphql` / `DeleteProviderModelSpec.graphql` / `BlockProviderModelSpec.graphql` 4 个新 operation fixtures。

### 12. `docs/api/provider-model.md`

自动重生成。`CreateProviderModel` 章节会有大变,`ProviderModel` 类型表小 80%;新增 `addProviderModelSpec` 等 4 节。

### 13. `postman/agent-platform-backend.postman_collection.json`

`CreateProviderModel` body 重写。其他 mutation 也要更新变量名。**新增** `AddProviderModelSpec` / `UpdateProviderModelSpec` / `DeleteProviderModelSpec` / `BlockProviderModelSpec` 4 条 Postman request。Request 数 +4。

### 14. CI Gate

`gofmt -l .` / `go vet ./...` / `go build ./...` / `go test -count=1 -run=^$ ./...` / `make docs` / `make migrate-diff`(reviewer 跑)。

## 风险

1. **`POST /model/new` 失败时的回滚**(CreateProviderModel 路径)— 第 K 个失败时,前 K-1 个已在 litellm 部署。best-effort `DELETE /model/delete {id}` 也要 sync,可能再次失败。最坏情况:litellm 有孤儿 + ent 没行。运维需要提供 litellm 侧清理入口。
2. **内存 grep 找 specId**(`updateProviderModelSpec` / `deleteProviderModelSpec` 路径)— ent 不支持 JSONB 数组中按 UUID 过滤,需要拉所有 ProviderModel 行在内存中 grep。ProviderModel 表目前 m-sized;若 > 5K 行需要改 schema(给 model_specs JSON 一个反向表,或加显式 `model_specs_model_info_id` 索引列)。
3. **wire GraphQL 字段重命名** — `input.outputCostPerToken`(Int, per-1M) → `outputCostPerToken`(Float, per-token)。前端需同步改。前端本就是 admin 表单,有完整控制。
4. **`addProviderModelSpec` / `updateProviderModelSpec` 部分成功** — 失败回滚已写 ent 时,需要 best-effort PATCH `/model/{id}/update` 用老的 litellm_params 回滚;可能再次失败,产生 litellm 与 ent 不一致。
5. **`enabled` 字段全删** — 前端 `Enabled` toggle UI 组件失去依据;运维需要隐藏相关控件。
6. **老 DB 升级语义损失** — 不做 data migration,老 provider 行的 model/tpm/api_base/api_key_ref/... 字段被丢弃,运维必须重新 upsert 所有 ProviderModel。release notes 必须告知。
7. **`POST /model/update` 静默删部署** — `updateProviderModel` mutation 用 `POST /model/update` 全量替换该 provider name 在 litellm 上的所有 deployment;若 ent JSON 不完整(漏列老 spec)会导致 litellm 端**静默删除**有但忘的 deployment。`updateProviderModel` 必须严格保证 body = 该 provider 的最终 spec 集合。
8. **`POST /model/update` wire 形态需核实** — litellm 的 `/model/update` endpoint wire 形态需在编码阶段拉官方源码或文档确认(body 单条/多条、字段别名等)。本 plan 假定 body 形如 `{"models": [spec1, spec2, ...]}`,实际可能有出入。
9. **`providerModelInfo` 整页性能** — `Count + Limit/Offset` 在 ent pg 走两次查询,ProviderModel 表行数若上万需要切到 keyset pagination;但当前 dataset 假设 < 5K,**暂用 offset**。

## 验证

1. **空 DB 启动**:Ent schema 重建 + atlas 迁移到空库 → 不报错
2. **有老 DB 升级**:`make migrate-apply` schema-only DDL 应用 → 老 provider 行变 `model_specs = NULL`(或 `[]`);运维手动 re-upsert 所有 ProviderModel
3. **CreateProviderModel happy path**:1 个 provider + 2 个 model_specs → 入参校验 → 写 ent model_specs JSON → 调 litellm `/model/new` 2 次 → 返 row
4. **addProviderModelSpec happy path**:1 个 provider 已存在 1 spec → add 第 2 spec → 调 litellm `/model/new` 1 次 → ent JSON setReplace 新数组 → 返 row
5. **deleteProviderModelSpec happy path**:1 个 provider 2 spec → 删第 2 → 调 litellm `/model/delete {id: <UUID>}` 1 次 → ent JSON setReplace 1 spec → 返 row
6. **失败回滚**(CreateProviderModel):N=2 spec,第 2 个 `/model/new` 失败 → 第 1 个 best-effort `DELETE /model/delete` → 返 GraphQL 错。ent 写入已发生(spec 1 部分成功),secret store 写入已发生,**有 revert 半截状态**

## 关键文件清单

| 操作 | 路径 |
|---|---|
| 重写 | `schema/provider-model.graphql` |
| 重写 | `ent/schema/provider_model.go` |
| 重写 | `internal/graph/provider-model.resolvers.go`(新增 5 个 mutation + 1 个 query:`addProviderModelSpec` / `updateProviderModelSpec` / `updateProviderModel` / `deleteProviderModelSpec` / `blockProviderModelSpec` / `providerModelInfo` 分页) |
| 重写 | `internal/graph/provider_model_probe.go` |
| 新增 | `internal/graph/model_spec.go`(wire JSON ↔ ent ↔ gateway 三向转换 + 单 spec 工具 + 详情 query) |
| 修改 | `internal/gateway/admin.go`(恢复 `GetGroupedModelInfo` method + 新增 `PushModelUpdate` method 走 `POST /model/update`) |
| 修改 | `internal/gateway/models.go`(`ModelManager` 接口加 `ListModels` 已有 / 添加 wire 反映 `/model/update`) |
| 删除 | `internal/graph/provider_model_upsert.go`(字段 setter helper,字段平迁入 JSON 后无需) |
| 新增 | `ent/migrate/migrations/20260704000004_provider_model_specs.sql`(schema-only) |
| 更新 | `atlas.sum` |
| 重生成 | `internal/graph/generated.go` + `models_gen.go` + `ent/*` |
| 重生成 | `docs/schema.graphql` + `docs/api/provider-model.md`(加入 `providerModelSpec` query + `ProviderModelSpecDetail` type) |
| 新增 + 更新 | `postman/agent-platform-backend.postman_collection.json`(CreateProviderModel body 重写;新增 5 mutations + 1 query = +6 requests) |
| 新增 + 更新 | `internal/graph/testdata/client_operations/ProviderModel*.graphql`(老 5 个重写 + 新增 6 个 operation fixtures,含 `ProviderModelInfo.graphql` 走分页 query) |