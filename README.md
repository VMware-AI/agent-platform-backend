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

## 数据库迁移

dev/test（sqlite 内存）开机自动迁移。**生产 postgres 不自动改表**（`DB_AUTO_MIGRATE`
默认 dev=on / prod=off），用 [Atlas](https://atlasgo.io) 版本化迁移显式应用：

```bash
ATLAS_DEV_URL=postgres://localhost:5432/atlas_dev make migrate-diff name=add_x  # 生成
DATABASE_URL=postgres://… make migrate-apply                                    # 应用
DATABASE_URL=postgres://… make migrate-status                                   # 状态/漂移
```

配置见 `atlas.hcl`，迁移文件在 `ent/migrate/migrations/`（baseline `init` 待用真 pg 生成）。

## 配置（环境变量）

所有变量在启动时被 `internal/config/config.Load()` + `cmd/server/main.go` 读取（fail-fast）。
dev/prod 行为不同的用 ✅ / ⚠️ 标注。

| 环境变量 | 值示例 | 是否必须 | 含义 |
|---|---|---|---|
| `APP_ENV` | `dev` \| `prod` | 否（默认 `dev`） | 切 dev/prod；prod 强制 `SecureCookie`、`DB_AUTO_MIGRATE` 默认关；非 `dev`/`prod` 直接 fatal |
| `HTTP_ADDR` | `:8080` | 否（默认 `:8080`） | GraphQL + 控制面监听地址 |
| `DATABASE_URL` | `postgres://agentplatform_user:agentplatform_passwd@localhost:5432/agentplatform?sslmode=disable` | dev 否 / prod 是 | postgres 连接串；空 → sqlite 内存（仅 dev/test） |
| `REDIS_URL` | `redis://localhost:6379/0` | 否 | 空 → session 与登录限流走进程内（dev）；多副本必须设（限流是 GLOBAL） |
| `SESSION_TTL_SECONDS` | `28800`（8h） | 否 | 整数，>0；非整数或 ≤0 fatal |
| `ALLOWED_ORIGINS` | `http://localhost:5173,https://console.example.com` | dev 否 / prod 是（跨域 CSRF 放行） | 逗号分隔；同源请求始终放行 |
| `DB_AUTO_MIGRATE` | `true` \| `false` | 否（dev 默认 `true`，prod 默认 `false`） | dev 启动自动改表；prod 必须关，改用 Atlas 版本化迁移 |
| `VCENTER_INSECURE` | `false` | 否 | `true` → 跳过 vCenter TLS 校验（仅用于自签/内网 CA 的离线 vCenter） |
| `ADMIN_BOOTSTRAP_PASSWORD` | `AdminLocal123!` | prod 是（dev 否） | 空库时种子 admin 密码；dev 不设会用 `ChangeMe123!` 并强制首登改密 |
| `LITELLM_BASE_URL` | `http://localhost:4000` | 否 | 空 → 不启用模型网关（`upsertUpstream`/`issueVirtualKey` 等 resolver 拿不到 client） |
| `LITELLM_MASTER_KEY` | `sk-local-master-…` | 与上一行同时设 | litellm admin API 的 master key（与 litellm 服务端 `LITELLM_MASTER_KEY` 必须一致） |
| `GATEWAY_PUBLIC_URL` | `https://gateway.example.com` | 否 | 给前端拼 litellm 入口；resolver 透传，未在配置层校验 |
| `CONTROL_PLANE_URL` | `https://api.example.com` | 否 | 控制面自身对外 URL；resolver 透传 |
| `VAULTWARDEN_URL` | `https://vault.example.com` | prod 是（dev 否） | 空 → 用进程内静态 secret 存（写入即丢，仅 dev）；prod 必须接 Vaultwarden 做凭据持久化 |
| `RECONCILE_INTERVAL_SECONDS` | `300` | 否（默认 `0`=关） | 网关 key 与治理表的对账周期；>0 且配了 litellm 才生效 |
| `RECONCILE_PRUNE` | `false` | 否 | `true` → 对账时删除孤儿/吊销陈旧行（默认只报告，drift-safe） |
| `AGENT_PKG_BASE_URL` | `https://mirror.example.com/agent-pkgs` | 否 | 离线镜像基址，替换 catalog 安装命令里的 `{{AGENT_PKG_BASE_URL}}`；空 → 占位符保留 |
| `AGENT_USER` | `agent` | 否（默认 `agent`） | 安装后跑 agent 的 OS 用户，替换 `{{AGENT_USER}}` |
| `ENV_SCOPE_ENABLED` | `false` | 否 | LLD-10 环境隔离；前端 `X-Environment` 契约未就绪前保持关 |
| `ATLAS_DEV_URL` | `postgres://localhost:5432/atlas_dev` | 仅 `make migrate-diff` 时 | Atlas diff 的 dev DB；运行 backend 不读 |
| `*` (任意) | — | — | `internal/secrets/resolver.go` 允许把任意环境变量名写进 vaultwarden 凭据引用（`vaultwarden://env:USER:PASS:APIKEY` 形式），用于上游 API key 注入 |

dev 最小集（开箱即跑）：

```bash
APP_ENV=dev \
DATABASE_URL=postgres://agentplatform_user:agentplatform_passwd@localhost:5432/agentplatform?sslmode=disable \
LITELLM_BASE_URL=http://localhost:4000 \
LITELLM_MASTER_KEY=sk-local-… \
ALLOWED_ORIGINS=http://localhost:5173 \
make run
```

prod 最小集（按行删就是更严的子集）：

```bash
APP_ENV=prod \
DATABASE_URL=postgres://…?sslmode=require \
REDIS_URL=redis://… \
ADMIN_BOOTSTRAP_PASSWORD='≥12 字符强密码' \
ALLOWED_ORIGINS=https://console.example.com \
VAULTWARDEN_URL=https://vault.example.com \
LITELLM_BASE_URL=https://litellm.example.com \
LITELLM_MASTER_KEY=sk-… \
DB_AUTO_MIGRATE=false
```

## 状态（M1，70 测试全绿）

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

**22 Ent 实体**；GraphQL 契约分布在 `schema/*.graphql`（前后端契约单一事实源）。

待续（需用户方向 / 真实环境）：Vaultwarden 凭据解析、`deployAgent` GraphQL 接线、真机 vCenter 验收、前端联调（独立仓）。

## 安全

无明文 secret 入库/日志；`password_hash` Ent `Sensitive()` 不进 GraphQL 输出；
所有写操作记 AuditLog；输入边界校验 fail-fast。
