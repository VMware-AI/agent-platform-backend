# Agent Config, Artifacts, Skills & Images

[← API Reference index](./README.md)

> Source: `schema/content.graphql`

## Queries

### `artifacts`

Optional kind filter — e.g. artifacts(kind: knowledge) drives the 智能体配置 知识包选择器 (LLD-11 K2).

```graphql
artifacts(kind: ArtifactKind): [Artifact!]!
```

- **Returns:** `[Artifact!]!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `kind` | `ArtifactKind` | no | — |

### `artifactVersions`

All versions of a named artifact, newest first (制品库版本列表, LLD-06 §3).

```graphql
artifactVersions(name: String!): [Artifact!]!
```

- **Returns:** `[Artifact!]!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `name` | `String!` | yes | — |

### `skills`

```graphql
skills: [Skill!]!
```

- **Returns:** `[Skill!]!`
- **Auth:** authenticated (no directive)

### `images`

```graphql
images: [Image!]!
```

- **Returns:** `[Image!]!`
- **Auth:** authenticated (no directive)

## Mutations

### `upsertArtifact`

```graphql
upsertArtifact(input: UpsertArtifactInput!): Artifact!
```

- **Returns:** `Artifact!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `UpsertArtifactInput!` | yes | — |

### `deleteArtifact`

```graphql
deleteArtifact(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `upsertSkill`

```graphql
upsertSkill(input: UpsertSkillInput!): Skill!
```

- **Returns:** `Skill!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `UpsertSkillInput!` | yes | — |

### `deleteSkill`

```graphql
deleteSkill(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `upsertImage`

```graphql
upsertImage(input: UpsertImageInput!): Image!
```

- **Returns:** `Image!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `UpsertImageInput!` | yes | — |

### `deleteImage`

```graphql
deleteImage(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

## Types

### Artifact

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `kind` | `ArtifactKind!` | — |
| `version` | `String!` | — |
| `uri` | `String!` | — |
| `content` | `String` | Inline content for small config/script artifacts (≤64K). Empty for packages. |
| `sha256` | `String` | — |
| `metadata` | `Map` | — |
| `createdAt` | `Time!` | — |

### Image

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `repository` | `String!` | — |
| `tag` | `String!` | — |
| `digest` | `String` | — |
| `signed` | `Boolean!` | — |
| `createdAt` | `Time!` | — |

### Skill

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `version` | `String!` | — |
| `description` | `String` | — |
| `uri` | `String!` | — |
| `createdAt` | `Time!` | — |

### UpsertArtifactInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `kind` | `ArtifactKind!` | — |
| `version` | `String!` | — |
| `uri` | `String!` | — |
| `content` | `String` | Inline text for config/script/knowledge artifacts (≤64K); embedded into the agent VM at deploy. Rejected for kind=package. sha256 is recomputed from content. |
| `sha256` | `String` | — |
| `metadata` | `Map` | — |

### UpsertImageInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `repository` | `String!` | — |
| `tag` | `String!` | — |
| `digest` | `String` | — |
| `signed` | `Boolean` | — |

### UpsertSkillInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |
| `version` | `String!` | — |
| `description` | `String` | — |
| `uri` | `String!` | — |

### ArtifactKind

*Enum*

| Value | Description |
|-------|-------------|
| `script` | — |
| `config` | — |
| `package` | — |
| `knowledge` | OKF knowledge pack (互链 markdown bundle) for agent grounding, non-RAG (LLD-11). |
