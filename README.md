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

## 状态（M1.0）

- ✅ 密码哈希（bcrypt cost 12 + 强度校验）+ 单测
- ✅ RBAC 权限矩阵（超管/普通/可观测性专员）+ 单测
- ✅ 会话存储（内存实现 + 接口）+ 单测
- ✅ Ent 实体（User/Role/Permission/Tenant/Department/Membership/AuditLog）
- ✅ GraphQL 契约（用户与权限）
- ⏭ 代码生成接线 + resolver + 登录/用户 CRUD 端到端

## 安全

无明文 secret 入库/日志；`password_hash` Ent `Sensitive()` 不进 GraphQL 输出；
所有写操作记 AuditLog；输入边界校验 fail-fast。
