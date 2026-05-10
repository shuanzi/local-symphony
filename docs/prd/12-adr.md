# 产品决策索引（非物理 ADR 文件清单）

> **Implementation warning:** This PRD file is product background only. Do not implement API, DB schema, CLI, state-machine, security, or test contracts from this file. Start from `../AGENT_IMPLEMENTATION_GUIDE.md`; executable contracts are `../../api/openapi.yaml` and `../../db/schema/*.sql`.


> 产品背景说明：本文件仅保留产品上下文。若与 `docs/implementation/`、`docs/schema/`、`docs/config/` 或 `docs/api/` 冲突，后者为准；不要从本 PRD 复制实现级 enum、schema、命令策略或模板。

本文件中的 `ADR-001` 到 `ADR-051` 是早期产品决策索引编号，不表示 `docs/adr/` 下存在同名物理 ADR 文件。v1 的物理 ADR 文件只有 `docs/adr/ADR-001` 到 `docs/adr/ADR-009`，完整冻结决策以 `docs/adr/ADR-009-decision-register.md` 的 D0–D108 与 G1–G9 为准。


## 产品与 tracker

| ADR | 决策 |
|---|---|
| ADR-001 | v1 是本地 Symphony App，不做 SaaS、多租户、完整 PM 平台 |
| ADR-002 | tracker 使用 SQLite，不依赖 Linear |
| ADR-003 | 新增 `tracker.kind: local`，不模拟 Linear API |
| ADR-004 | Markdown / JSON 只做导入导出，不作为 source of truth |
| ADR-005 | 状态机采用 agent-friendly flow：Ready、Working、Rework、Human Review、Done |
| ADR-006 | agent 通过受限 `local_tracker` tool 写本地 issue |

## Runtime

| ADR | 决策 |
|---|---|
| ADR-007 | 后端使用 Go |
| ADR-008 | 前端使用 React + TypeScript |
| ADR-009 | v1 是 Go daemon + localhost dashboard |
| ADR-010 | v2 桌面化采用 Tauri + Go sidecar |
| ADR-011 | Orchestrator 是单权威 actor |
| ADR-012 | SQLite 分为 global app DB + project DB |
| ADR-013 | UI 使用 REST + SSE，不用 GraphQL |
| ADR-014 | 本地工具通过 CLI + IPC，不依赖 Codex dynamic tools |

## Workspace / Git

| ADR | 决策 |
|---|---|
| ADR-015 | v1 project = 一个本地 Git repo |
| ADR-016 | 每 issue 一个 git worktree |
| ADR-017 | 每 issue 一个稳定 branch |
| ADR-018 | workspace 默认在 `~/.symphony/workspaces/` |
| ADR-019 | 同 issue workspace 跨 operator re-dispatch / Rework 复用 |
| ADR-020 | 不自动 reset / rebase dirty workspace |
| ADR-021 | agent 默认不 commit、不 push、不 PR |
| ADR-022 | publish 由 UI / CLI 人工触发 |

## Agent / Prompt / Tools

| ADR | 决策 |
|---|---|
| ADR-023 | v1 只支持 Codex app-server |
| ADR-024 | 每 run 一个 Codex subprocess |
| ADR-025 | Codex adapter version-aware；transport/framing 是 adapter detail |
| ADR-026 | prompt = Runtime Envelope + WORKFLOW Prompt + Context Pack |
| ADR-027 | 每 run 保存 redacted prompt snapshot |
| ADR-028 | handoff tool 原子提交；review-packet finalizer 负责 Human Review 转换 |
| ADR-029 | dynamic tools / MCP 后移 |
| ADR-030 | 默认最多一个 handoff continuation |

## UI / Observability

| ADR | 决策 |
|---|---|
| ADR-031 | UI 是 control surface，不参与 correctness |
| ADR-032 | v1 Dashboard 有 Overview、Board、Issue、Run、Approval、Review、Workflow、Diagnostics |
| ADR-033 | durable event store 驱动 UI timeline |
| ADR-034 | raw Codex log 不作为主 UI，主 UI 展示归一化 timeline |
| ADR-035 | Approval Inbox 是一级页面 |
| ADR-036 | Review Packet 是 Human Review 核心交付物 |
| ADR-037 | 所有关键 UI 操作有 CLI fallback |

## Security

| ADR | 决策 |
|---|---|
| ADR-038 | 默认威胁模型：本地但不完全可信 |
| ADR-039 | 默认安全模式：`balanced-secure` |
| ADR-040 | API loopback-only + session token + CSRF |
| ADR-041 | v1 不托管第三方长期 secret |
| ADR-042 | agent 最小 env allowlist |
| ADR-043 | workspace 是唯一默认可写边界 |
| ADR-044 | 网络默认 deny |
| ADR-045 | sensitive path 默认保护 |
| ADR-046 | v1 不做自动备份、migration、crash recovery、完整 audit、供应链深度策略 |

## MVP Scope

| ADR | 决策 |
|---|---|
| ADR-047 | v1 主路径是 issue → Codex run → handoff.submit → review packet → Human Review |
| ADR-048 | v1 不做桌面壳、自动 PR、自动备份、migration、crash recovery、完整 audit |
| ADR-049 | v1 里程碑按 M0–M8 推进 |
| ADR-050 | v1.1 优先补 migration、backup、crash recovery、audit |
| ADR-051 | v1.2 再补 supply-chain policy、desktop shell、Git provider publish |
