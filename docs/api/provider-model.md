# Provider Model (Upstream LLM)

[← API Reference index](./README.md)

> Source: `schema/provider-model.graphql`

## Queries

### `providerModelInfo`

```graphql
providerModelInfo(filter: ProviderModelInfoFilterInput, page: PageInput!, sort: ProviderModelInfoSort): ProviderModelInfoConnection!
```

- **Returns:** `ProviderModelInfoConnection!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `filter` | `ProviderModelInfoFilterInput` | no | — |
| `page` | `PageInput!` | yes | — |
| `sort` | `ProviderModelInfoSort` | no | — |

## Mutations

### `createProviderModel`

0.1.x: 纯 insert(重名报错,强制前端走 updateProviderModel)

```graphql
createProviderModel(input: CreateProviderModelInput!): ProviderModel!
```

- **Returns:** `ProviderModel!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `CreateProviderModelInput!` | yes | — |

### `deleteProviderModel`

```graphql
deleteProviderModel(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `addProviderModelSpec`

0.1.x: 单 spec CRUD(全部 @hasRole admin)

```graphql
addProviderModelSpec(input: AddProviderModelSpecInput!): ProviderModel!
```

- **Returns:** `ProviderModel!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `AddProviderModelSpecInput!` | yes | — |

### `updateProviderModelSpec`

```graphql
updateProviderModelSpec(input: UpdateProviderModelSpecInput!): ProviderModel!
```

- **Returns:** `ProviderModel!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `UpdateProviderModelSpecInput!` | yes | — |

### `updateProviderModel`

```graphql
updateProviderModel(input: UpdateProviderModelInput!): ProviderModel!
```

- **Returns:** `ProviderModel!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `UpdateProviderModelInput!` | yes | — |

### `deleteProviderModelSpec`

```graphql
deleteProviderModelSpec(input: ProviderModelSpecIdInput!): ProviderModel!
```

- **Returns:** `ProviderModel!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `ProviderModelSpecIdInput!` | yes | — |

### `blockProviderModelSpec`

```graphql
blockProviderModelSpec(input: ProviderModelSpecBlockInput!): ProviderModel!
```

- **Returns:** `ProviderModel!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `ProviderModelSpecBlockInput!` | yes | — |

### `testProviderConnection`

原有 probe / refresh(返回 4 档 status;ProviderModel 实体)

```graphql
testProviderConnection(name: String!): ProviderModelStatus!
```

- **Returns:** `ProviderModelStatus!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `name` | `String!` | yes | — |

### `refreshProviderModelStatus`

```graphql
refreshProviderModelStatus(id: ID!): ProviderModel!
```

- **Returns:** `ProviderModel!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `testPrivateModelSpecConnection`

0.1.x: dry-run probe of a private model spec's apiBase+apiKey against the upstream provider's /v1/models. Pre-save — no row read, no secret write. 供 ProviderModel 增改表单的"Test Connection"按钮调用(per-spec 一对一)。

```graphql
testPrivateModelSpecConnection(input: TestPrivateModelSpecConnectionInput!): PrivateModelSpecTestResult!
```

- **Returns:** `PrivateModelSpecTestResult!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `TestPrivateModelSpecConnectionInput!` | yes | — |

## Types

### AdditionalProp1

*Object*

0.1.x: per-spec 探测 snapshot。worker 顺便写此字段;unhealthy / unknown 时 message 携带错误细节(超时、5xx body、auth 失败等)。前端在 ProviderModel 详情 / 表格 cell 内 select additionalProp1 { status, message } 显示。

| Field | Type | Description |
|-------|------|-------------|
| `status` | `ModelHealth!` | — |
| `message` | `String` | unhealthy / unknown 时携带错误信息;healthy 时为 null |

### LitellmParams

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `apiKey` | `String` | apiKey 明文输入 → resolver 写 secret store → 仅 ref 持久化在 ent JSON 入参明文,resolver 内部用 secret-store-minted 明文替代;返回值 null(filter) |
| `apiKeyRef` | `String` | wire 返的字段(服务端 secret store ref id,明文永不出库) |
| `apiBase` | `String` | 真实物理上游 LLM 地址(与 modelGateway 语义不重叠) |
| `model` | `String!` | — |
| `customLlmProvider` | `String` | 注:字段类型为 String,接受 customLlmProvider 的任何字符串(包括 CUSTOM 上任意 URL base 的 openai 兼容商) 默认 "openai" |
| `organization` | `String` | — |
| `tpm` | `Int` | — |
| `rpm` | `Int` | — |
| `defaultApiKeyTpmLimit` | `Int` | — |
| `defaultApiKeyRpmLimit` | `Int` | — |
| `maxBudget` | `Float` | — |
| `budgetDuration` | `String` | — |
| `useInPassThrough` | `Boolean!` | — |
| `useChatCompletionsApi` | `Boolean!` | — |
| `mergeReasoningContentInChoices` | `Boolean!` | — |
| `tags` | `[String!]!` | — |
| `inputCostPerToken` | `Float` | Cost 字段:wire Float(per-token),DB 存 Float,resolver 不再"÷1e6",前端直传 |
| `outputCostPerToken` | `Float` | — |
| `cacheReadInputTokenCost` | `Float` | — |
| `cacheCreationInputTokenCost` | `Float` | — |

### ModelInfo

*Object*

0.1.x: 精简到 spec-level 字段。

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | 后端 UUID |
| `mode` | `String` | — |
| `blocked` | `Boolean!` | — |
| `additionalProp1` | `AdditionalProp1!` | worker 写入的派生字段;resolver 创建/更新时不接受外部写入(ModelInfoInput 故意不含)。 |

### ModelSpec

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `litellmParams` | `LitellmParams!` | — |
| `modelInfo` | `ModelInfo!` | — |

### PrivateModelSpecTestResult

*Object*

0.1.x: TestPrivateModelSpecConnection 返回 — success/message + 上游 /v1/models 返回的 model id 列表

| Field | Type | Description |
|-------|------|-------------|
| `success` | `Boolean!` | — |
| `message` | `String!` | — |
| `modelList` | `[String!]!` | 上游 data[].id;失败时 [] |
| `testedAt` | `Time!` | — |

### ProviderModel

*Object*

ProviderModel 精简为 7 字段(去掉 provider,增加 modelGateway 关联对象)

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `modelGateway` | `ModelGateway!` | 0.1.x: 必填关联对象;masterKey 不在 wire 上 |
| `status` | `ProviderModelStatus!` | — |
| `createdAt` | `Time!` | — |
| `updatedAt` | `Time!` | — |
| `lastCheckedAt` | `Time` | 用于 UI "X 分钟前检查"显示 |
| `modelSpecs` | `[ModelSpec!]!` | — |

### ProviderModelInfoConnection

*Object*

0.1.x: 分页 wrapper

| Field | Type | Description |
|-------|------|-------------|
| `data` | `[ProviderModel!]!` | — |
| `total_count` | `Int!` | — |
| `current_page` | `Int!` | — |
| `total_pages` | `Int!` | — |
| `size` | `Int!` | — |

### AddProviderModelSpecInput

*Input*

0.1.x: 单 spec 增

| Field | Type | Description |
|-------|------|-------------|
| `providerModelId` | `ID!` | — |
| `spec` | `ModelSpecInput!` | — |

### CreateProviderModelInput

*Input*

CreateProviderModelInput: name + modelSpecs + modelGateway

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `modelGateway` | `ID!` | 必填写盘用(引用已存在 GatewayConnection);resolver 据此解析 masterKey + 调 /model/new |
| `modelSpecs` | `[ModelSpecInput!]!` | min 1 |

### LitellmParamsInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `apiKey` | `String` | — |
| `apiKeyRef` | `String` | — |
| `apiBase` | `String` | — |
| `model` | `String!` | — |
| `customLlmProvider` | `String` | — |
| `organization` | `String` | — |
| `tpm` | `Int` | — |
| `rpm` | `Int` | — |
| `defaultApiKeyTpmLimit` | `Int` | — |
| `defaultApiKeyRpmLimit` | `Int` | — |
| `maxBudget` | `Float` | — |
| `budgetDuration` | `String` | — |
| `useInPassThrough` | `Boolean` | — |
| `useChatCompletionsApi` | `Boolean` | — |
| `mergeReasoningContentInChoices` | `Boolean` | — |
| `tags` | `[String!]` | — |
| `inputCostPerToken` | `Float` | — |
| `outputCostPerToken` | `Float` | — |
| `cacheReadInputTokenCost` | `Float` | — |
| `cacheCreationInputTokenCost` | `Float` | — |

### ModelInfoInput

*Input*

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `id` | `ID` | — | id 可选(省略 = 后端生成) |
| `mode` | `String` | — | — |
| `blocked` | `Boolean` | `false` | — |

### ModelSpecInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `litellmParams` | `LitellmParamsInput!` | — |
| `modelInfo` | `ModelInfoInput` | — |

### ProviderModelInfoFilterInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `search` | `String` | — |
| `status` | `ProviderModelStatus` | — |
| `modelGatewayId` | `ID` | Filter to a single modelGateway (the FK column on provider_models). Operator Console uses this to scope the model list to one gateway when the operator picks a gateway in the filter bar; null = all gateways. |

### ProviderModelInfoSort

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `field` | `ProviderModelInfoSortField!` | — |
| `direction` | `SortDirection!` | — |

### ProviderModelSpecBlockInput

*Input*

0.1.x: 单 spec 阻断 toggle(不调 litellm;TODO 未来 PATCH 同步)

| Field | Type | Description |
|-------|------|-------------|
| `specId` | `ID!` | — |
| `blocked` | `Boolean!` | — |

### ProviderModelSpecIdInput

*Input*

0.1.x: 按 specId 单 spec 删

| Field | Type | Description |
|-------|------|-------------|
| `specId` | `ID!` | — |

### TestPrivateModelSpecConnectionInput

*Input*

0.1.x: TestPrivateModelSpecConnection 入参(apiBase + apiKey 直接传,不经 secret store)

| Field | Type | Description |
|-------|------|-------------|
| `apiBase` | `String!` | — |
| `apiKey` | `String!` | — |

### UpdateProviderModelInput

*Input*

0.1.x: 整组 ProviderModel 全量替换入参(POST /model/update 一次性推)

| Field | Type | Description |
|-------|------|-------------|
| `providerModelId` | `ID!` | — |
| `modelSpecs` | `[ModelSpecInput!]!` | min 1,完整 setReplace |

### UpdateProviderModelSpecInput

*Input*

0.1.x: 按 specId 单 spec 修改(PATCH /model/{id}/update)

| Field | Type | Description |
|-------|------|-------------|
| `specId` | `ID!` | — |
| `litellmParams` | `LitellmParamsInput!` | — |
| `modelInfo` | `ModelInfoInput!` | — |

### ModelHealth

*Enum*

0.1.x: per-spec 物理部署探测结果(healthy / unhealthy / unknown)。

| Value | Description |
|-------|-------------|
| `healthy` | — |
| `unhealthy` | — |
| `unknown` | — |

### ProviderModelInfoSortField

*Enum*

0.1.x: NAME / STATUS / GATEWAY sort. Direction is currently ignored server-side — every field sorts Asc to match the rest of the codebase's paginated-query convention. The console UI flips the visual order client-side when a Desc order is requested, so a future direction-aware rewrite can land in one place without breaking the wire contract.

| Value | Description |
|-------|-------------|
| `NAME` | — |
| `STATUS` | — |
| `GATEWAY` | — |

### ProviderModelProvider

*Enum*

Provider-level 下拉约束(也是 LitellmParamsInput.customLlmProvider 的字面量集合)

| Value | Description |
|-------|-------------|
| `custom` | 任意 OpenAI 兼容端点 |
| `deepseek` | DeepSeek |
| `minimax` | MiniMax |
| `moonshot` | Moonshot(月之暗面) |
| `openrouter` | OpenRouter(多模型聚合) |
| `openai` | — |
| `anthropic` | — |

### ProviderModelStatus

*Enum*

0.1.x: 按 model_name 分组健康聚合 4 档。详见 §D probeOneProviderModel 重写。

| Value | Description |
|-------|-------------|
| `full_healthy` | 全部署 healthy,绿色 |
| `partial_outage` | 至少 1 healthy + 至少 1 unhealthy,黄色(仍可服务) |
| `full_outage` | 全部 unhealthy 冷却,红色(拦截新请求) |
| `unknown` | 全部署 last_checked 超时,健康但静默,灰色 |
