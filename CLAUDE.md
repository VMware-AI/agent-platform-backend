# CLAUDE.md — 开发框架(团队约定)

> 本仓库(agent-platform-backend)AI 辅助开发的团队约定。**后续开发所有人 / agent 一律 follow**,直到维护者宣布进入收尾/发版阶段。

## 1. 测试:开发期「不反复维护」

项目仍在快速迭代、远未收尾。为控成本、保速度,**开发阶段不反复维护/运行单元测试**:

- ❌ 改源码**不要顺手改动/新增** `*_test.go`;源码变更不应连带 diff 测试文件。
- ❌ **不主动跑** `go test ./...`(全套慢);单次改动别全跑、别为一次改动读整屏测试输出。
- ✅ **CI gate 是安全网**:合并前靠 `ci.yml` 的 gate(docs-check + gofmt + vet + build + `migration-drift`)兜底,不本地反复跑。**PR/push 上根本不跑 `go test`**(省 CI 资源、不卡人)。
- ✅ **测试文件保留、不删** —— 只是暂不主动维护,供每周测试轮次。
- 🗓️ **每周一次**:独立的 `test.yml`(**周一 cron + 手动 `workflow_dispatch`**)跑 `go test ./...` 测 main 出结果,维护者据此统一审核、补/修/更新测试。
- **例外**:①被明确要求写/跑测试;②维护者主导的每周测试轮次内。恢复常规 TDD/覆盖率由**维护者显式宣布**。

## 2. CI gate 不得绕过(重要)

(2026-07-02 更新:主干 gate 已恢复全绿 —— 此前「gofmt / migration-drift 重跑即红」的漂移已修复。保持它绿:)

- **gate 红就修,不得强合 / 绕过。** 合并前确认目标分支 CI 全绿。
- 改 ent schema **必过 `migration-drift`**:用 `make migrate-diff name=<x>`(ent SDK 生成)出迁移,**别手写、别删 gate 要的 ALTER**(手改会与 schema.go 生成物不一致 → CI 再生成漂移)。
- 改 `schema/*.graphql` 或 Go **必过 `gofmt` + `docs-check`**(`make docs`)。

## 3. 工具/版本对齐 CI

本仓 CI 与 `go.mod` 用 **go 1.25**。本机若是更高版本(如 1.26),其 `gofmt` 对齐规则与 ent 代码生成会与 1.25 不一致 → 污染 diff / 挂 gate。

- 生成物 + 格式化统一用 CI 版本:`GOTOOLCHAIN=go1.25.0 go generate ./ent/... && GOTOOLCHAIN=go1.25.0 gofmt -l .`。
- migration/atlas.sum 以 **CI 为准**(本机 homebrew atlas 与 CI 的 SDK 算法可能不同)。

## 4. 范围最小化

一次 PR 聚焦一个模块 / 一件事;不夹带无关重构或大范围格式化(尤其工具/版本差异导致的无关 diff —— 先对齐 CI 版本再改)。

---
*本框架为临时开发期约定,优先「开发速度 + 控成本」;质量兜底交给 CI gate + 维护者每周轮次。*
