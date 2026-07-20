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
make run
# 模型网关在 console「模型网关接入」页添加（按上面的 endpoint + 配在 .env 里的 master key）
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
| `LITELLM_RECONCILE_INTERVAL_SECONDS` | `900`（15m） | 否 | DB→LiteLLM 5 阶段统一对账周期（keys / gateway_status / provider_models / spend_refresh / router_settings）。Drift A（LiteLLM 单方面）只记录不处理；Drift B/C（DB 单方面）直接执行。`0`=关闭后台 ticker（手动触发仍可用）；多副本经 Postgres advisory lock 选主 |
| `LITELLM_MASTER_KEY` | `sk-...` | **dev only** | gateway master key 解析的兜底：console 配置的 `gateway_connections.master_key_ref` 在 secrets store 解不出来时（如首次启动 / 加密密钥尚未就绪），落到这个 env var 上拿值。**主路径是 DB store**，任何带 vCenter/litellm 的真实部署都别设——会绕过 secrets 加密 |
| `POOL_SYNC_INTERVAL_SECONDS` | `3600`（60m） | 否 | 资源池后台同步周期；扫描所有有 `secret_ref` 的池子并过 timeout→retry→breaker 链。`0`=关闭后台 ticker（手动 `syncResourcePool` 或创建即同步仍可用）。详见 [ResourcePool 同步机制](#resourcepool-同步机制) |
| `POOL_SYNC_TIMEOUT_SECONDS` | `30` | 否 | 单池同步全链（connect + inventory + full inventory + DB 写回）总超时；终端 vCenter 慢响应不会拖住整个 ticker。`0` 走 30s 兜底（防 `ctx(0)` 即过期） |
| `POOL_SYNC_MAX_RETRIES` | `3` | 否 | 失败重试次数（不含首次尝试）；指数退避 1s/2s/4s + 25% jitter。仅 `*vcenter.RetryableError`（网络/超时/5xx 等）触发；鉴权失败、对象不存在等业务错误不重试。`0`=只试一次 |
| `POOL_SYNC_BREAKER_THRESHOLD` | `5` | 否 | 同一 endpoint（vCenter）连续失败该次数后熔断器跳闸（per-endpoint，`sony/gobreaker`）。`0` 或负数 → 关闭熔断器层（仍走 timeout + retry）。任一 endpoint 跳闸只影响该 endpoint 的池子，不会污染其他 endpoint |
| `POOL_SYNC_BREAKER_OPEN_SECONDS` | `60` | 否 | 熔断器 Open 态持续秒数；到期进 HalfOpen，放 1 个探测请求（`MaxRequests=1`）。Open 期请求立即返回 `ErrOpenState`，**不写 `status`**（避免被误标 error；其他健康端不受影响） |
| `PROVIDER_PROBE_INTERVAL_SECONDS` | `600`（10m） | 否 | 后台探活每个 enabled `ProviderModel` 的上游 API，并把 Active/Degraded/Melted/Unknown 写回行；前端只读缓存态，不阻塞在线探。`0`=关闭周期（upsert 时仍会 fire-and-forget 一次）。正交于 DB↔LiteLLM 对账（探的是上游 provider，不是 gateway） |
| `OBS_SPEND_CACHE_TTL_SECONDS` | `30` | 否 | LLD-15 observability spend 报表在多 gateway 间的进程内缓存秒数；`0`=关闭（每次请求 fan-out 到 litellm） |
| `PERM_CACHE_TTL_SECONDS` | `0` | 否 | `@hasPermission` 进程内缓存 TTL；`0`=关闭。**进程级缓存，多副本下吊销不会跨副本生效**，仅适合单副本。`>0` 才启用（建议后续 Redis pub/sub 失效通道落地后再开） |
| `DB_MAX_OPEN_CONNS` | `20` | 否 | postgres 连接池上限；`0`=Go 默认无上限（多副本下可打爆 `max_connections`），按 `max_connections / 副本数` 调；仅 postgres |
| `DB_MAX_IDLE_CONNS` | `10` | 否 | 连接池空闲连接上限（Go 默认 2，高并发下连接抖动）；仅 postgres |
| `DB_CONN_MAX_LIFETIME_MINUTES` | `30` | 否 | 连接最大存活分钟数，`0`=不回收；配合故障转移 / PgBouncer；仅 postgres |
| `AGENT_PKG_BASE_URL` | `https://mirror.example.com/agent-pkgs` | 否 | 离线镜像基址：替换 catalog 安装命令里的 `{{AGENT_PKG_BASE_URL}}`，并在部署时以 `guestinfo.agentmgr.agent_pkg_base_url` 下发给 VM 供 webadmin 升级拉包（daemon 侧拼 `{base}/{name}-{version}.tar.gz`；可含只读 FTP 凭据，勿入日志）；空 → 占位符保留、不下发 |
| `AGENT_KEEP_VERSIONS` | `3` | 否 | 部署时以 `guestinfo.agentmgr.agent_keep_versions` 下发：VM 升级后保留的历史版本数。`0`（默认）= 不下发（与未设置等价），走 daemon 默认 `3`；daemon 侧拒绝 `<1`，最小有效值为 `1` |
| `AGENT_USER` | `agent` | 否 | 仅作 `os.Getenv` 直读（**不经 `config.Load`**）：装机命令里 `{{AGENT_USER}}` 替换的 OS 用户。生产值走 LLD-13 数据库「平台设置」表，不推荐用此 env 覆盖 |
| `ENV_SCOPE_ENABLED` | `false` | 否 | LLD-10 环境隔离；前端 `X-Environment` 契约未就绪前保持关 |
| `SECRETS_ENCRYPTION_KEY` | `openssl rand -hex 32` | **是**（任何环境，除非用 `SECRETS_ENCRYPTION_KEYS`） | AES-256-GCM 加密 `platform_secrets` 的密钥；SHA-256 派生到 32 字节；空 → 启动 fail-fast。dev 用 `deploy/start_backend_*.sh` 首次运行自动生成并写到 `deploy/.secrets_encryption_key`（mode `0600`），prod 必须手动注入并离线备份。详见下方「凭据加密密钥」一节 |
| `SECRETS_ENCRYPTION_KEYS` | `k1:passphrase1,k2:passphrase2` | 轮换期 **是**（否则空） | 轮换友好的多密钥形式：`id:passphrase` 逗号分隔。**第一项是 active key**（新写入用它）；其余参与解密以兼容旧密文。**优先级高于 `SECRETS_ENCRYPTION_KEY`**；设置它就忽略单密钥版本。详见下方「密钥轮换」 |
| `SECRETS_ROTATION_INTERVAL_SECONDS` | `0` | 否 | 后台 worker 扫描 `platform_secrets` 中用退役 key 加密的行并 re-encrypt 到 active key 的周期；`0`=关闭（轮换是运维主动行为，不自动迁）。`>0` 才启用 |
| `SECRETS_AUDIT_ENABLED` | `false` | 否 | 每次 `Secrets.Resolve` 成功都写一条 `audit_log`（action=`secret.read`）；5min router 同步 + 60s probe × N 上游能轻易堆出每小时数百条，调查期间再开 |
| `ATLAS_DEV_URL` | `postgres://localhost:5432/atlas_dev` | 仅 `make migrate-diff` 时 | Atlas diff 的 dev DB；运行 backend 不读 |
| `*` (任意) | — | — | 凭据引用：模型网关 / vCenter 等凭据在 console 配置后由 `internal/secrets` 解析到 `platform_secrets` 表里的密文（不需要 `vaultwarden://` 这类特殊 scheme——旧方案已删除）。小众 air-gap 场景可用 `env://USER_VAR,PASS_VAR[,APIKEY_VAR]` 从进程 env 直读（不落库），但主路径是 DBStore |

> 模型网关 endpoint（`LITELLM_BASE_URL` / `GATEWAY_PUBLIC_URL`）不再是启动 env——在 console「模型网关接入」页添加网关连接，后端按部门/默认网关从 DB 解析（LLD-13 §3.3）。`LITELLM_MASTER_KEY` 仅作为 gateway master key 的 dev 兜底（见上表），主路径是 `platform_secrets`。`AGENT_USER` 仍然只在启动时从 env 读，未来再迁移到平台设置表。

## 凭据加密密钥（`SECRETS_ENCRYPTION_KEY` / `SECRETS_ENCRYPTION_KEYS`）

vCenter 密码、模型网关凭据等都加密存在 `platform_secrets` 表里，加密用 AES-256-GCM，密钥经 SHA-256 派生到 32 字节。**同一把密钥在所有环境里复用**——没有 dev/prod 之分。

两种形式互斥：**`SECRETS_ENCRYPTION_KEY`**（单密钥，简单）或 **`SECRETS_ENCRYPTION_KEYS`**（多密钥，支持轮换）。设了 `SECRETS_ENCRYPTION_KEYS` 就忽略单密钥版本。

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

### 设置密钥（单密钥）

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

### 密钥轮换（`SECRETS_ENCRYPTION_KEYS`）

存储格式**带 key-version 前缀**（`internal/secrets/crypto.go`），支持原地轮换：

1. **生成新密钥**：`openssl rand -hex 32`（同上）。
2. **改成多密钥形式**——**第一个 id 是 active key**（新写入用它），其余参与解密以兼容旧密文：

   ```bash
   export SECRETS_ENCRYPTION_KEYS='k1:<新密码>,k0:<旧密码>'
   ```

3. **后台 worker 自动迁移**（`SECRETS_ROTATION_INTERVAL_SECONDS > 0`）：扫描 `platform_secrets` 中仍用 `k0` 加密的行，re-encrypt 到 `k1`。设为 `0` 时 worker 不启动，需要运维手动触发或自己跑迁移。
4. **轮换完**：从 `SECRETS_ENCRYPTION_KEYS` 去掉 `k0`，最终恢复成单密钥：`SECRETS_ENCRYPTION_KEY=<k1 串>`。

格式约束：`id:passphrase` 逗号分隔；id 必须非空且唯一；空 passphrase 被跳过；启动期校验 fail-fast。

### ⚠️ 备份 & 多副本一致

- **丢失密钥 = 永久丢失所有 `platform_secrets` 加密凭据**（vCenter 密码、模型网关 key 等）。必须离线备份到密码管理器 / Vault / 加密 USB。
- 同一环境所有副本必须使用**同一把** `SECRETS_ENCRYPTION_KEY`（或同一组 `SECRETS_ENCRYPTION_KEYS`，id 顺序一致）——副本之间任何写入会用本进程配置的 active key 加密，副本之间任何读取会用本进程能解的密钥解密；id 列表不一致会出现「副本 A 写的密文副本 B 解不开」。
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
| 模型网关 | VirtualKey（issue/revoke → litellm gateway client）、上/下游对账（`RECONCILE_*`） |
| 可观测性 | TokenUsage（计量中心，按 model 聚合）、RequestLog（请求日志）、AuditLog（审计 + platform-admin 校验硬化） |
| 系统配置 | ResourcePool（vCenter 接入，govmomi）、用户与权限 |
| vCenter | `internal/vcenter`：连接 / ListVMs / SetGuestinfo（govmomi，**vcsim 测试**） |
| 网关 | `internal/gateway`：litellm admin API client（key/team/budget） |
| 部署编排 | `internal/deploy`：签 key → cloud-init → guestinfo 注入（**vcsim + fake gateway 集成测试**） |
| 本地全栈 | `deploy/`：`start_litellm_and_db.sh` + `start_backend_local.sh`（源码循环）/ `start_backend_docker.sh`（`quay.io/vmware-ai/agent-platform-backend:latest` 镜像）+ Prometheus 抓 litellm `/metrics` |

**22 Ent 实体**；GraphQL 契约分布在 `schema/*.graphql`（前后端契约单一事实源）。

待续（需用户方向 / 真实环境）：`deployAgent` GraphQL 接线、真机 vCenter 验收、前端联调（独立仓）。凭据解析已落地（加密 `platform_secrets` 表，取代旧 Vaultwarden 方案）。

## 安全

无明文 secret 入库/日志；`password_hash` Ent `Sensitive()` 不进 GraphQL 输出；
所有写操作记 AuditLog；输入边界校验 fail-fast。
