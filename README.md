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

最小循环：

```bash
make tidy        # go mod tidy
make generate    # Ent + gqlgen 代码生成
make test        # 单测（auth/session/config 无需 DB；ent 用 sqlite 内存）
make build
```

### 本地全栈（推荐新手）

`deploy/` 是一个 **单条 `docker compose up` 就能跑通的端到端栈**：litellm 数据面 +
Go 控制面 + Postgres + Redis + Prometheus。详细步骤见 [deploy/README.md](deploy/README.md)。

```bash
cd deploy
./start_litellm_and_db.sh        # up litellm + pg(:5433) + redis(:6379) + prometheus(:9090)
./start_backend_local.sh         # up Go control plane (auto-migrate, dev defaults)
# 镜像快查（不重建）：
HOST_IP=host.docker.internal ./start_backend_docker.sh up
./start_litellm_and_db.sh down   # 停容器（保留 volume）
./start_litellm_and_db.sh clean  # 停 + 清 volume
```

### 纯 Go 控制面（自带 pg/redis）

如果 Postgres/Redis 已经在跑（例如用 `deploy/docker-compose.yml` 起的）：

```bash
docker compose -f deploy/docker-compose.yml up   # 本地 pg + redis
APP_ENV=dev \
DATABASE_URL=postgres://agentplatform_user:agentplatform_passwd@127.0.0.1:5433/agentplatform?sslmode=disable \
REDIS_URL=redis://127.0.0.1:6379/0 \
ALLOWED_ORIGINS=http://localhost:5173 \
LITELLM_BASE_URL=http://localhost:4000 \
LITELLM_MASTER_KEY=$(grep '^LITELLM_MASTER_KEY=' deploy/.env | cut -d= -f2-) \
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
| `ADMIN_BOOTSTRAP_PASSWORD` | `AdminLocal123!` | prod 是（dev 否） | 空库时种子 admin 密码；dev 不设会用 `ChangeMe123!` 并强制首登改密 |
| `CONTROL_PLANE_URL` | `https://api.example.com` | 否 | 控制面自身对外 URL；resolver 透传 |
| `SECRETS_ENCRYPTION_KEY` | `openssl rand -hex 32` | **是**（任何环境） | AES-256-GCM 加密 `platform_secrets` 的密钥；SHA-256 派生到 32 字节；空 → 启动 fail-fast。**不支持轮换**：存储格式无 key-version 前缀，换密钥会让所有密文 GCM 校验失败。dev 用 `deploy/start_backend_*.sh` 首次运行自动生成并写到 `deploy/.secrets_encryption_key`（mode `0600`），prod 必须手动注入并离线备份。详见下方"凭据加密密钥"一节 |
| `RECONCILE_INTERVAL_SECONDS` | `300` | 否（默认 `0`=关） | 网关 key 与治理表的对账周期；>0 且存在默认网关连接才生效 |
| `RECONCILE_PRUNE` | `false` | 否 | `true` → 对账时删除孤儿/吊销陈旧行（默认只报告，drift-safe）。多副本下对账经 Postgres advisory lock 选主，仅单副本执行 prune（不会重复删） |
| `POOL_SYNC_INTERVAL_SECONDS` | `3600`（60m） | 否 | 资源池后台同步周期；扫描所有有 `secret_ref` 的池子并过 timeout→retry→breaker 链。`0`=关闭后台 ticker（手动 `syncResourcePool` 或创建即同步仍可用）。详见 [ResourcePool 同步机制](#resourcepool-同步机制) |
| `POOL_SYNC_TIMEOUT_SECONDS` | `30` | 否 | 单池同步全链（connect + inventory + full inventory + DB 写回）总超时；终端 vCenter 慢响应不会拖住整个 ticker。`0` 走 30s 兜底（防 `ctx(0)` 即过期） |
| `POOL_SYNC_MAX_RETRIES` | `3` | 否 | 失败重试次数（不含首次尝试）；指数退避 1s/2s/4s + 25% jitter。仅 `*vcenter.RetryableError`（网络/超时/5xx 等）触发；鉴权失败、对象不存在等业务错误不重试。`0`=只试一次 |
| `POOL_SYNC_BREAKER_THRESHOLD` | `5` | 否 | 同一 endpoint（vCenter）连续失败该次数后熔断器跳闸（per-endpoint，`sony/gobreaker`）。`0` 或负数 → 关闭熔断器层（仍走 timeout + retry）。任一 endpoint 跳闸只影响该 endpoint 的池子，不会污染其他 endpoint |
| `POOL_SYNC_BREAKER_OPEN_SECONDS` | `60` | 否 | 熔断器 Open 态持续秒数；到期进 HalfOpen，放 1 个探测请求（`MaxRequests=1`）。Open 期请求立即返回 `ErrOpenState`，**不写 `status`**（避免被误标 error；其他健康端不受影响） |
| `DB_MAX_OPEN_CONNS` | `20` | 否 | postgres 连接池上限；`0`=Go 默认无上限（多副本下可打爆 `max_connections`），按 `max_connections / 副本数` 调；仅 postgres |
| `DB_MAX_IDLE_CONNS` | `10` | 否 | 连接池空闲连接上限（Go 默认 2，高并发下连接抖动）；仅 postgres |
| `DB_CONN_MAX_LIFETIME_MINUTES` | `30` | 否 | 连接最大存活分钟数，`0`=不回收；配合故障转移 / PgBouncer；仅 postgres |
| `AGENT_PKG_BASE_URL` | `https://mirror.example.com/agent-pkgs` | 否 | 离线镜像基址，替换 catalog 安装命令里的 `{{AGENT_PKG_BASE_URL}}`；空 → 占位符保留 |
| `ENV_SCOPE_ENABLED` | `false` | 否 | LLD-10 环境隔离；前端 `X-Environment` 契约未就绪前保持关 |
| `ATLAS_DEV_URL` | `postgres://localhost:5432/atlas_dev` | 仅 `make migrate-diff` 时 | Atlas diff 的 dev DB；运行 backend 不读 |
| `*` (任意) | — | — | 凭据引用：模型网关 / vCenter 等凭据在 console 配置后由 `internal/secrets` 解析到 `platform_secrets` 表里的密文（不需要 `vaultwarden://` 这类特殊 scheme——旧方案已删除） |

> `AGENT_USER`（装机命令里 `{{AGENT_USER}}` 的 OS 用户）不再是启动 env——它是数据库平台设置（LLD-13），在 console「平台设置」页里改，默认 `agent`。
>
> 模型网关（`LITELLM_BASE_URL` / `LITELLM_MASTER_KEY` / `GATEWAY_PUBLIC_URL`）也不再是启动 env——在 console「模型网关接入」页添加网关连接，后端按部门/默认网关从 DB 解析（LLD-13 §3.3）。

## 凭据加密密钥（`SECRETS_ENCRYPTION_KEY`）

vCenter 密码、模型网关凭据等都加密存在 `platform_secrets` 表里，加密用 AES-256-GCM，密钥从 `SECRETS_ENCRYPTION_KEY` 经 SHA-256 派生到 32 字节。**同一把密钥在所有环境里复用**——没有 dev/prod 之分。

### 生成密钥

任何非空字符串都行（短了等于熵被压到 32 字节，长了被截），但**用全熵的密码学随机串**：

```bash
# 32 字节随机，hex 编码（64 字符；最常用）
openssl rand -hex 32

# 32 字节随机，base64 编码（44 字符，含末尾 =）
openssl rand -base64 32

# 64 字节随机，base64url（无 padding，URL/文件友好）
openssl rand -base64 64 | tr -d '=' | tr '/+' '_-'
```

或者用 `/dev/urandom`（Linux）：

```bash
# 32 字节裸二进制 → 转 hex
head -c 32 /dev/urandom | xxd -p -c 64
```

校验长度：hex 串 64 字符、base64 串 44 字符（`=` 结尾），分别对应 32 字节。

### 设置密钥

```bash
# 1. 临时（当前 shell）
export SECRETS_ENCRYPTION_KEY='<上面生成的串>'

# 2. 写入 deploy/.env（连同 litellm 配置一起；用 deploy/start_backend_*.sh 时自动加载）
echo "SECRETS_ENCRYPTION_KEY=$(openssl rand -hex 32)" >> deploy/.env

# 3. Kubernetes（推荐 Secret；不要进 ConfigMap）
kubectl create secret generic agent-platform \
  --from-literal=SECRETS_ENCRYPTION_KEY='<生成的串>'

# 4. systemd / 裸机：写到 /etc/agent-platform.env，service 里用 EnvironmentFile=
```

### ⚠️ 备份 & 不要轮换

存储格式**不带 key-version 前缀**（`internal/secrets/crypto.go`），轮换密钥会让旧密文 GCM 校验失败 —— **当前不支持密钥轮换**。所以：

- **丢失密钥 = 永久丢失所有 `platform_secrets` 加密凭据**（vCenter 密码、模型网关 key 等）。必须离线备份到密码管理器 / Vault / 加密 USB。
- 将来要加轮换需要先在密文里塞一个 version 字节并做 re-encrypt 流程（见 `crypto.go` 注释）。**现在不要尝试轮换**。
- 生产用专用密钥，跟 dev 区分；dev 的串泄露也不会丢生产凭据。

### `deploy/start_backend_*.sh` 自动生成

为了一键跑通，dev 启动器会在首次运行自动生成密钥并写入 `deploy/.secrets_encryption_key`（mode `0600`），后续启动直接复用。容器路径（`start_backend_docker.sh`）在 host 侧读出密钥再 `-e` 注入容器，因为容器每次 `docker run --pull=always` 是新的，**不能在容器内自生成**（每次拉新镜像会跑出不同密钥，把已有密文全锁死）。

dev 最小集（开箱即跑）：

```bash
APP_ENV=dev \
DATABASE_URL=postgres://agentplatform_user:agentplatform_passwd@localhost:5432/agentplatform?sslmode=disable \
ALLOWED_ORIGINS=http://localhost:5173 \
make run
# 模型网关在 console「模型网关接入」页添加（不再是启动 env）
```

prod 最小集（按行删就是更严的子集）：

```bash
APP_ENV=prod \
DATABASE_URL=postgres://…?sslmode=require \
REDIS_URL=redis://… \
ADMIN_BOOTSTRAP_PASSWORD='≥12 字符强密码' \
ALLOWED_ORIGINS=https://console.example.com \
SECRETS_ENCRYPTION_KEY='<openssl rand -hex 32 生成的串>' \
DB_AUTO_MIGRATE=false
```

## ResourcePool 同步机制

vCenter 资源池（`ResourcePool`）同步由 5 个 env 控制，三个入口（`CreateResourcePool` 的 fire-and-forget 首同步 / `SyncResourcePool` 手动 mutation / `StartAutoSync` 后台 ticker）**走同一条 `syncOnePool` 路径**，共用同一条容错链：

```
30s 单池超时（POOL_SYNC_TIMEOUT_SECONDS）
  → 指数退避重试（POOL_SYNC_MAX_RETRIES 次，1s/2s/4s + jitter）
    → per-endpoint 熔断器（POOL_SYNC_BREAKER_THRESHOLD 次连败 → Open POOL_SYNC_BREAKER_OPEN_SECONDS）
```

**入口自动启用**（`cmd/server/main.go`）：
- 创建后立即 fire-and-forget 首同步（不等 ticker 跑）
- `POOL_SYNC_INTERVAL_SECONDS > 0` 时启 ticker；`0` 关掉后台周期但手动 sync 仍可用
- 熔断器层始终启用（`EnablePoolSync` 在 main 里无条件调）；单测里 `resolver` 默认未启用 → 跳过 breaker 层直接走 retry+timeout

**`status` / `last_synced_at` 写入规则**（唯一写入点：`internal/graph/pool_sync_one.go`）：
- 成功 → `status=connected` + `last_synced_at=now`
- 真失败 → `status=error`，`last_synced_at` **保留**上次成功时间（前端能展示"上次成功 + 当前失败"）
- 熔断器 Open → **不写** `status`，下次 ticker / 手动 sync 自动探测恢复
- `updateResourcePool` 改名字/凭据 → 不重置同步状态

完整状态机、`syncStatus` 派生逻辑、修改入口时的不变量清单见 [internal/RESOURCE_POOL.md](internal/RESOURCE_POOL.md)。

## 状态（M1，70 测试全绿）

**0619 四大 nav 全覆盖**：

| 域 | 能力 |
|---|---|
| 认证 / RBAC | bcrypt 密码 + 强度校验、session（内存/redis）、角色枚举 + 权限矩阵 + `@hasRole`/`@hasPermission` directive（**e2e 验证真实拦截**） |
| 用户与权限 | login/logout/changePassword/me + 用户 CRUD + resetPassword（首登强制改密）；`LoginInput.remember` → session-cookie 生命周期（LLD-12） |
| 智能体中心 | AgentTemplate（市场 catalog）/ AgentConfig / Agent（实例，owner 隔离 + 状态鉴权，**N+1 消除**） |
| 模型网关 | VirtualKey（issue/revoke → litellm gateway client）、RateLimitPolicy（限流策略）、上/下游对账（`RECONCILE_*`） |
| 可观测性 | TokenUsage（计量中心，按 model 聚合）、RequestLog（请求日志）、AuditLog（审计 + platform-admin 校验硬化） |
| 系统配置 | ResourcePool（vCenter 接入，govmomi）、用户与权限 |
| vCenter | `internal/vcenter`：连接 / ListVMs / SetGuestinfo（govmomi，**vcsim 测试**） |
| 网关 | `internal/gateway`：litellm admin API client（key/team/budget） |
| 部署编排 | `internal/deploy`：签 key → cloud-init → guestinfo 注入（**vcsim + fake gateway 集成测试**） |
| 本地全栈 | `deploy/`：`start_litellm_and_db.sh` + `start_backend_local.sh`（源码循环）/ `start_backend_docker.sh`（`quay.io/vmware-ai/agent-platform-backend:latest` 镜像）+ Prometheus 抓 litellm `/metrics` |

**22 Ent 实体**；GraphQL 契约分布在 `schema/*.graphql`（前后端契约单一事实源）。

待续（需用户方向 / 真实环境）：Vaultwarden 凭据解析、`deployAgent` GraphQL 接线、真机 vCenter 验收、前端联调（独立仓）。

## 安全

无明文 secret 入库/日志；`password_hash` Ent `Sensitive()` 不进 GraphQL 输出；
所有写操作记 AuditLog；输入边界校验 fail-fast。
