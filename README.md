# agent-platform-backend

Go control-plane backend for the **Agent Platform** (前后端分离重构).
GraphQL API · vCenter 编排 (govmomi) · litellm 网关治理 · RBAC · pgsql + redis.

> Design: private repo [agent-platform-design](https://github.com/VMware-AI/agent-platform-design)
> — [HLD](https://github.com/VMware-AI/agent-platform-design/blob/main/docs/architecture/01-hld-agent-platform.md) ·
> [LLD-01 用户与权限](https://github.com/VMware-AI/agent-platform-design/blob/main/docs/lld/01-data-model-and-rbac.md)

## 栈

Go · [Ent](https://entgo.io)（实体→迁移）· [gqlgen](https://gqlgen.com)（GraphQL）·
[govmomi](https://github.com/vmware/govmomi)（vSphere）· pgx · go-redis · bcrypt + session.
单静态二进制，air-gap 友好。

## 布局

```
cmd/server            入口
internal/
  auth/               bcrypt 密码 + RBAC + session 上下文
  session/            会话存储（内存 / redis）
  config/             配置加载 + 启动校验（fail-fast）
  graph/              gqlgen resolver（薄）
ent/ (生成)            Ent 客户端 + 迁移
ent/schema/           Ent 实体定义（手写）
schema/*.graphql      GraphQL 契约（单一事实源，跨仓同步给前端）
```

## 开发

```bash
make tidy        # go mod tidy
make generate    # Ent + gqlgen 代码生成
make test        # 单测（auth/session/config 无需 DB；ent 用 sqlite 内存）
make build
docker compose -f deploy/docker-compose.yml up   # 本地 pg + redis
make run
```

## 状态（M1，~41 测试全绿）

**0619 四大 nav 全覆盖**：

| 域 | 能力 |
|---|---|
| 认证 / RBAC | bcrypt 密码 + 强度校验、session（内存/redis）、角色枚举 + 权限矩阵 + `@hasRole`/`@hasPermission` directive（**e2e 验证真实拦截**） |
| 用户与权限 | login/logout/changePassword/me + 用户 CRUD + resetPassword（首登强制改密） |
| 智能体中心 | AgentTemplate（市场 catalog）/ AgentConfig / Agent（实例，owner 隔离 + 状态鉴权） |
| 模型网关 | VirtualKey（issue/revoke → litellm gateway client）、RateLimitPolicy（限流策略） |
| 可观测性 | TokenUsage（计量中心，按 model 聚合）、RequestLog（请求日志）、AuditLog（审计） |
| 系统配置 | ResourcePool（vCenter 接入，govmomi）、用户与权限 |
| vCenter | `internal/vcenter`：连接 / ListVMs / SetGuestinfo（govmomi，**vcsim 测试**） |
| 网关 | `internal/gateway`：litellm admin API client（key/team/budget） |
| 部署编排 | `internal/deploy`：签 key → cloud-init → guestinfo 注入（**vcsim + fake gateway 集成测试**） |

**18 Ent 实体**；GraphQL 契约分布在 `schema/*.graphql`（前后端契约单一事实源）。

待续（需用户方向 / 真实环境）：Vaultwarden 凭据解析、`deployAgent` GraphQL 接线、真机 vCenter 验收、前端联调（独立仓）。

## 安全

无明文 secret 入库/日志；`password_hash` Ent `Sensitive()` 不进 GraphQL 输出；
所有写操作记 AuditLog；输入边界校验 fail-fast。
