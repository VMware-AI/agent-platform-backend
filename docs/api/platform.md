# Platform (Users, Roles, Permissions, Departments, Settings)

[← API Reference index](./README.md)

> Source: `schema/account.graphql`, `schema/rbac.graphql`, `schema/department.graphql`, `schema/settings.graphql`

## Queries

### `users`

```graphql
users(filter: UserFilter, pagination: Pagination, sort: UserSort): UserConnection!
```

- **Returns:** `UserConnection!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `filter` | `UserFilter` | no | — |
| `pagination` | `Pagination` | no | — |
| `sort` | `UserSort` | no | — |

### `roles`

```graphql
roles(pagination: Pagination): RoleConnection!
```

- **Returns:** `RoleConnection!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `pagination` | `Pagination` | no | — |

### `role`

```graphql
role(id: ID!): Role
```

- **Returns:** `Role`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `userExists`

Debounced dedupe check for the create-user form.

```graphql
userExists(username: String, email: String): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `username` | `String` | no | — |
| `email` | `String` | no | — |

### `customRoles`

```graphql
customRoles: [CustomRole!]!
```

- **Returns:** `[CustomRole!]!`
- **Auth:** `@hasRole(any: [admin])`

### `permissions`

```graphql
permissions: [Permission!]!
```

- **Returns:** `[Permission!]!`
- **Auth:** `@hasRole(any: [admin])`

### `userRoles`

```graphql
userRoles(userId: ID!): [CustomRole!]!
```

- **Returns:** `[CustomRole!]!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `userId` | `ID!` | yes | — |

### `departments`

```graphql
departments: [Department!]!
```

- **Returns:** `[Department!]!`
- **Auth:** `@hasRole(any: [admin])`

### `departmentMembers`

Platform/tenant admins OR the department's own dept-admin (delegation checked in-resolver — LLD-01 §4.1 三轨判权).

```graphql
departmentMembers(departmentId: ID!): [Membership!]!
```

- **Returns:** `[Membership!]!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `departmentId` | `ID!` | yes | — |

### `platformSettings`

```graphql
platformSettings: PlatformSettings!
```

- **Returns:** `PlatformSettings!`
- **Auth:** `@hasRole(any: [admin])`

## Mutations

### `createUser`

```graphql
createUser(input: CreateUserInput!): CreateUserPayload!
```

- **Returns:** `CreateUserPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `CreateUserInput!` | yes | — |

### `updateUser`

```graphql
updateUser(id: ID!, input: UpdateUserInput!): AccountUser!
```

- **Returns:** `AccountUser!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |
| `input` | `UpdateUserInput!` | yes | — |

### `deleteUser`

```graphql
deleteUser(id: ID!): DeleteUserPayload!
```

- **Returns:** `DeleteUserPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `resetUserPassword`

```graphql
resetUserPassword(id: ID!): ResetPasswordPayload!
```

- **Returns:** `ResetPasswordPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `toggleUserEnabled`

```graphql
toggleUserEnabled(id: ID!): ToggleUserEnabledPayload!
```

- **Returns:** `ToggleUserEnabledPayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `assignUsersToRole`

```graphql
assignUsersToRole(input: AssignUsersToRoleInput!): AssignUsersToRolePayload!
```

- **Returns:** `AssignUsersToRolePayload!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `AssignUsersToRoleInput!` | yes | — |

### `createCustomRole`

```graphql
createCustomRole(input: CreateCustomRoleInput!): CustomRole!
```

- **Returns:** `CustomRole!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `CreateCustomRoleInput!` | yes | — |

### `deleteCustomRole`

```graphql
deleteCustomRole(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `upsertPermission`

```graphql
upsertPermission(key: String!, description: String): Permission!
```

- **Returns:** `Permission!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `key` | `String!` | yes | — |
| `description` | `String` | no | — |

### `setRolePermissions`

Replace the role's permission set (the matrix row).

```graphql
setRolePermissions(roleId: ID!, permissionKeys: [String!]!): CustomRole!
```

- **Returns:** `CustomRole!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `roleId` | `ID!` | yes | — |
| `permissionKeys` | `[String!]!` | yes | — |

### `assignUserRole`

```graphql
assignUserRole(userId: ID!, roleId: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `userId` | `ID!` | yes | — |
| `roleId` | `ID!` | yes | — |

### `removeUserRole`

```graphql
removeUserRole(userId: ID!, roleId: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `userId` | `ID!` | yes | — |
| `roleId` | `ID!` | yes | — |

### `createDepartment`

Creates the department AND its litellm team (no orphan: rolls back on sync failure).

```graphql
createDepartment(input: CreateDepartmentInput!): Department!
```

- **Returns:** `Department!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `CreateDepartmentInput!` | yes | — |

### `deleteDepartment`

```graphql
deleteDepartment(id: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `id` | `ID!` | yes | — |

### `addMembership`

Membership management is delegated: platform/tenant admins OR the department's dept-admin (checked in-resolver, since @hasRole only covers platform/tenant level).

```graphql
addMembership(userId: ID!, departmentId: ID!, role: MembershipRole): Membership!
```

- **Returns:** `Membership!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `userId` | `ID!` | yes | — |
| `departmentId` | `ID!` | yes | — |
| `role` | `MembershipRole` | no | — |

### `removeMembership`

```graphql
removeMembership(userId: ID!, departmentId: ID!): Boolean!
```

- **Returns:** `Boolean!`
- **Auth:** authenticated (no directive)

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `userId` | `ID!` | yes | — |
| `departmentId` | `ID!` | yes | — |

### `updatePlatformSettings`

```graphql
updatePlatformSettings(input: UpdatePlatformSettingsInput!): PlatformSettings!
```

- **Returns:** `PlatformSettings!`
- **Auth:** `@hasRole(any: [admin])`

| Argument | Type | Required | Default |
|----------|------|----------|---------|
| `input` | `UpdatePlatformSettingsInput!` | yes | — |

## Types

### AccountRoleRef

*Object*

The user's role as a lightweight reference (embedded in AccountUser). id is a standard UUID, derived from the same role key as the corresponding Role entity in the roles query — so AccountUser.role.id === roles(roleId).id for the same role key.

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |

### AccountUser

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `username` | `String!` | — |
| `displayName` | `String!` | — |
| `email` | `String!` | — |
| `role` | `AccountRoleRef!` | — |
| `connectionStatus` | `ConnectionStatus!` | ONLINE when the user currently has at least one live session. |
| `lastLoginAt` | `Time` | — |
| `enabled` | `Boolean!` | — |
| `createdAt` | `Time!` | — |
| `updatedAt` | `Time!` | — |

### AssignUsersToRolePayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `role` | `Role!` | — |
| `assignedCount` | `Int!` | — |

### CreateUserPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `user` | `AccountUser!` | — |
| `generatedPassword` | `String` | Present only when passwordMode = AUTO (the generated temp password, shown once). |

### CustomRole

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `name` | `String!` | — |
| `isSystem` | `Boolean!` | — |
| `permissions` | `[String!]!` | — |
| `createdAt` | `Time!` | — |

### DeleteUserPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |

### Department

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `tenantId` | `ID` | — |
| `name` | `String!` | — |
| `litellmTeamId` | `String` | — |
| `gatewayConnectionId` | `ID` | The gateway connection hosting this department's litellm team (LLD-13 §3.3). Null → the platform default gateway. |
| `createdAt` | `Time!` | — |

### Membership

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `userId` | `ID!` | — |
| `departmentId` | `ID!` | — |
| `role` | `MembershipRole!` | — |

### Permission

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `key` | `String!` | — |
| `description` | `String` | — |

### PlatformSettings

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `agentUser` | `String!` | OS user that runs installed agents on the VM. Defaults to "agent" when unset. |
| `packageSourceUrl` | `String!` | Internal agent-package mirror base URL (e.g. ftp://mirror.internal/agents) and its read-only username. The password is write-only (stored encrypted, never returned). |
| `packageSourceUser` | `String!` | — |

### ResetPasswordPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `user` | `AccountUser!` | — |
| `generatedPassword` | `String!` | — |

### Role

*Object*

A built-in assignable role surfaced as an entity. id is a standard UUID (deterministically derived from roleKey — see roles_builtin.go). roleKey is the stable string key ("admin" | "user" | "read_only") used in @hasRole directives and as a logical identifier.

| Field | Type | Description |
|-------|------|-------------|
| `id` | `ID!` | — |
| `roleKey` | `String!` | — |
| `name` | `String!` | — |
| `description` | `String!` | — |
| `userCount` | `Int!` | — |
| `builtIn` | `Boolean!` | — |

### RoleConnection

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `nodes` | `[Role!]!` | — |
| `totalCount` | `Int!` | — |
| `pageInfo` | `PageInfo!` | — |

### ToggleUserEnabledPayload

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `user` | `AccountUser!` | — |

### UserConnection

*Object*

| Field | Type | Description |
|-------|------|-------------|
| `nodes` | `[AccountUser!]!` | — |
| `totalCount` | `Int!` | — |
| `pageInfo` | `PageInfo!` | — |

### AssignUsersToRoleInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `roleId` | `ID!` | Built-in role UUID (from the roles query). User-id-as-string mapping is now server-driven via Role.roleKey. |
| `userIds` | `[ID!]!` | — |

### CreateCustomRoleInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `name` | `String!` | — |

### CreateDepartmentInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `tenantId` | `ID` | — |
| `name` | `String!` | — |
| `maxBudget` | `Float` | Shared budget for the litellm team backing this department. |
| `gatewayConnectionId` | `ID` | Which gateway connection hosts this department's litellm team (LLD-13 §3.3). Omitted → the platform default gateway (GatewayConnection.isDefault). |

### CreateUserInput

*Input*

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `username` | `String!` | — | — |
| `displayName` | `String!` | — | — |
| `email` | `String!` | — | — |
| `roleId` | `ID!` | — | — |
| `passwordMode` | `PasswordMode!` | — | — |
| `customPassword` | `String` | — | — |
| `enabled` | `Boolean` | `true` | — |

### UpdatePlatformSettingsInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `agentUser` | `String` | When provided, sets the agent OS user; omitted = unchanged. Must be non-empty. |
| `packageSourceUrl` | `String` | Package mirror (LLD-16 OQ-2). Each field: omitted = unchanged; empty string clears it. packageSourcePassword is write-only and stored encrypted (secrets). |
| `packageSourceUser` | `String` | — |
| `packageSourcePassword` | `String` | — |

### UpdateUserInput

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `displayName` | `String` | — |
| `email` | `String` | — |
| `roleId` | `ID` | — |
| `enabled` | `Boolean` | — |

### UserFilter

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `usernameKeyword` | `String` | — |
| `roleKeyword` | `String` | — |
| `emailKeyword` | `String` | — |
| `statusKeyword` | `ConnectionStatus` | — |
| `roleId` | `ID` | Built-in role UUID (from the roles query). |

### UserSort

*Input*

| Field | Type | Description |
|-------|------|-------------|
| `field` | `UserSortField!` | — |
| `direction` | `SortDirection!` | — |

### ConnectionStatus

*Enum*

| Value | Description |
|-------|-------------|
| `ONLINE` | — |
| `OFFLINE` | — |

### MembershipRole

*Enum*

| Value | Description |
|-------|-------------|
| `user` | — |
| `dept_admin` | — |

### PasswordMode

*Enum*

| Value | Description |
|-------|-------------|
| `AUTO` | backend generates a temp password, returned once in generatedPassword |
| `CUSTOM` | use customPassword |

### UserSortField

*Enum*

| Value | Description |
|-------|-------------|
| `USERNAME` | — |
| `ROLE` | — |
| `EMAIL` | — |
| `CONNECTION` | — |
| `LAST_LOGIN` | — |
| `CREATED_AT` | — |
| `UPDATED_AT` | — |
