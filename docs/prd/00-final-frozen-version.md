# Local Symphony App v1 最终冻结版本

## 1. 产品定义

**Local Symphony App v1** 是一个本地运行的 agent engineering workflow control plane。

它由以下核心模块组成：

```text
Go daemon
+ React/TypeScript dashboard
+ SQLite local tracker
+ git worktree workspace
+ Codex app-server runner
+ CLI/IPC tool gateway
+ atomic handoff
+ review packet
+ REST/SSE API
+ balanced-secure local security baseline
```

v1 的核心目标是：

> 建立一条可靠、可观察、可审查的本地 agent 工程工作流，而不是追求最大自动化。

## 2. 冻结的 v1 主路径

```text
symphony init
  ↓
创建本地 issue
  ↓
issue → Ready
  ↓
手动或 orchestrator dispatch
  ↓
创建 git worktree + branch
  ↓
启动 Codex app-server
  ↓
Codex 在 workspace 内工作
  ↓
Codex 调用 symphony tool handoff
  ↓
生成 review packet
  ↓
issue → Human Review
  ↓
人工 review
  ├── Send to Rework
  └── Mark Done
```

## 3. 冻结的核心架构决策

| 领域 | 决策 |
|---|---|
| 后端 | Go daemon |
| 前端 | React + TypeScript |
| 桌面化 | v1 localhost dashboard；v2 Tauri + Go sidecar |
| Tracker | 本地 SQLite，不依赖 Linear |
| Project 单位 | 一个本地 Git repo |
| Workspace | 每 issue 一个 `git worktree` |
| Branch | 每 issue 一个稳定 branch |
| Agent runtime | Codex app-server |
| Transport | stdio JSONL |
| Tool Gateway | `symphony tool` CLI + 本地 IPC |
| Handoff | 原子化 `symphony tool handoff` |
| Review | review packet 是 Human Review 核心交付物 |
| API | `/api/v1` REST + SSE |
| UI | Control surface，不参与 orchestrator correctness |
| 安全 | `balanced-secure` local baseline |

## 4. 冻结的 v1 不做事项

| 不做 | 后续版本 |
|---|---|
| Tauri 桌面壳 | v2 |
| 自动 PR / merge | v1.2+ |
| agent 自动 commit | v1.1+ 可选 |
| 自动 SQLite backup | v1.1 |
| migration / rollback 生产流程 | v1.1 |
| crash recovery | v1.1 |
| 完整 audit log | v1.1 |
| 供应链深度风险策略 | v1.2 |
| dynamic tools / MCP | v1.1+ |
| 多租户 / RBAC | v2+ |
| 远程 dashboard | v2+ |

## 5. 冻结的 v1 验收标准

v1 必须完整通过：

```text
1. 在 Git repo 内运行 symphony init。
2. 系统创建 WORKFLOW.md 和 .symphony/symphony.db。
3. 用户创建 issue LOC-1。
4. 用户把 LOC-1 移动到 Ready。
5. 用户手动 dispatch LOC-1。
6. 系统创建 git worktree。
7. 系统创建 symphony/LOC-1-... branch。
8. 系统启动 codex app-server。
9. Codex 在 workspace 内修改文件。
10. Codex 运行测试。
11. Codex 调用 symphony tool handoff。
12. 系统生成 review packet。
13. LOC-1 进入 Human Review。
14. UI 展示 diff、summary、tests、risks、commands。
15. 用户选择 Send to Rework 或 Mark Done。
```

v1 可以不自动恢复、不自动备份、不自动迁移，但必须明确展示：

```text
workflow invalid
codex unavailable
approval timeout
tool token invalid
missing handoff
workspace conflict
blocker active
unsupported DB version
```

## 6. 冻结后的变更规则

| 变更类型 | 处理方式 |
|---|---|
| 不影响主路径的小改动 | 可直接进入实现 |
| 影响 tracker / workspace / agent / handoff / review packet 的改动 | 必须形成 ADR 修订 |
| 影响 v1 不做事项的改动 | 默认后移，不进入 v1 |
| 影响安全边界的改动 | 必须重新评估 threat model |
| 影响数据模型的改动 | 必须更新 schema 与 API contract |
