# OVA Marketplace

[← API Reference index](./README.md)

> Source: `schema/ova.graphql`

## Queries

### `ovaTemplateFamilies`

```graphql
ovaTemplateFamilies(filter: OvaTemplateFamilyFilter pagination: Pagination sort: OvaTemplateFamilySort): OvaTemplateFamilyConnection!
```

- **Returns:** `OvaTemplateFamilyConnection!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `filter` | `OvaTemplateFamilyFilter pagination: Pagination sort: OvaTemplateFamilySort` | no | — |

### `ovaTemplateVersions`

```graphql
ovaTemplateVersions(familyId: ID, pagination: Pagination): OvaTemplateVersionConnection!
```

- **Returns:** `OvaTemplateVersionConnection!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `familyId` | `ID` | no | — |
| `pagination` | `Pagination` | no | — |

## Mutations

### `createOvaTemplateFamily`

Create a family + its initial version in one transaction.

```graphql
createOvaTemplateFamily(input: CreateOvaTemplateFamilyInput!): CreateOvaTemplateFamilyPayload!
```

- **Returns:** `CreateOvaTemplateFamilyPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `CreateOvaTemplateFamilyInput!` | yes | — |

### `addOvaTemplateVersion`

Append a version to an existing family (errors if the family is missing).

```graphql
addOvaTemplateVersion(input: AddOvaTemplateVersionInput!): AddOvaTemplateVersionPayload!
```

- **Returns:** `AddOvaTemplateVersionPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `AddOvaTemplateVersionInput!` | yes | — |

## Types

### AddOvaTemplateVersionPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `version` | `OvaTemplateVersion!` | — |

### CreateOvaTemplateFamilyPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `family` | `OvaTemplateFamily!` | — |

### OvaTemplateFamily

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `type` | `String!` | — |
| `description` | `String!` | — |
| `tools` | `[String!]!` | — |
| `skills` | `[String!]!` | — |
| `scenarios` | `[String!]!` | — |
| `iconShape` | `String!` | — |
| `iconColor` | `OvaTemplateColor!` | — |
| `latestVersion` | `String` | Version string of the most recently created version, or null when none. |
| `createdAt` | `Time!` | — |
| `updatedAt` | `Time!` | — |
| `versions` | `[OvaTemplateVersion!]!` | All versions, newest-first. |

### OvaTemplateFamilyConnection

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `nodes` | `[OvaTemplateFamily!]!` | — |
| `totalCount` | `Int!` | — |
| `pageInfo` | `PageInfo!` | — |

### OvaTemplateVersion

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `familyId` | `ID!` | Owning family id (resolved from the family edge). |
| `version` | `String!` | — |
| `ovaIdentifier` | `String!` | — |
| `notes` | `String` | — |
| `createdAt` | `Time!` | — |

### OvaTemplateVersionConnection

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `nodes` | `[OvaTemplateVersion!]!` | — |
| `totalCount` | `Int!` | — |
| `pageInfo` | `PageInfo!` | — |

### AddOvaTemplateVersionInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `familyId` | `ID!` | — |
| `version` | `String!` | — |
| `ovaIdentifier` | `String!` | — |
| `notes` | `String` | — |

### CreateOvaTemplateFamilyInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `type` | `String!` | — |
| `description` | `String!` | — |
| `tools` | `[String!]!` | — |
| `scenarios` | `[String!]!` | — |
| `skills` | `[String!]!` | — |
| `iconShape` | `String!` | — |
| `iconColor` | `OvaTemplateColor!` | — |
| `initialVersion` | `CreateOvaTemplateVersionInput!` | — |

### CreateOvaTemplateVersionInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `version` | `String!` | — |
| `ovaIdentifier` | `String!` | — |
| `notes` | `String` | — |

### OvaTemplateFamilyFilter

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `nameKeyword` | `String` | — |
| `type` | `String` | — |

### OvaTemplateFamilySort

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `field` | `OvaTemplateFamilySortField!` | — |
| `direction` | `SortDirection!` | — |

### OvaTemplateColor

*Enum*

Fixed marketplace card palette (console-defined).

| Value | Description |
|-------|-------------|
| `BLUE` | — |
| `PURPLE` | — |
| `ORANGE` | — |
| `GREEN` | — |
| `RED` | — |
| `CYAN` | — |

### OvaTemplateFamilySortField

*Enum*

| Value | Description |
|-------|-------------|
| `OVA_NAME` | — |
| `TYPE` | — |
| `CREATED_AT` | — |
| `UPDATED_AT` | — |
