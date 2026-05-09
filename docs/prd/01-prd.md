# PRD：Local Symphony App v1

> 产品背景说明：本文件仅保留产品上下文。若与 `docs/implementation/`、`docs/schema/`、`docs/config/` 或 `docs/api/` 冲突，后者为准；不要从本 PRD 复制实现级 enum、schema、命令策略或模板。


## 1. 产品定位

Local Symphony App v1 是一个本地运行的 agent orchestration control plane，用于把本地工程任务转化为可由 Codex 执行、可观察、可审批、可交接、可复盘的工程工作流。

它不是：

```text
Linear 替代品
Jira 替代品
GitHub Issues 替代品
通用项目管理平台
多租户 SaaS
自动 merge / deploy 系统
```

它是：

```text
本地 issue tracker
+ agent scheduler / runner
+ git workspace manager
+ two-stage handoff / review system
+ local dashboard
```

## 2. 目标用户

v1 面向：

```text
个人开发者
小团队本地工作站
希望用 Codex 执行本地工程任务的人
希望保留人工 review / publish 控制权的人
```

v1 不面向：

```text
企业多租户平台
远程协作平台
高敏感生产 secret 环境
合规审计系统
全自动发布系统
```

## 3. 核心用户故事

### 用户故事 1：创建本地任务并派发给 Codex

作为开发者，我希望在本地创建一个 issue，把它移动到 Ready，然后让 Symphony 为它创建独立 workspace 并启动 Codex，以便 agent 可以在隔离分支里完成实现。

验收：

```text
issue 可创建
issue 可进入 Ready
orchestrator 可 dispatch
worktree 被创建
branch 被创建
Codex cwd 是 issue workspace
Run Detail 可看到执行 timeline
```

### 用户故事 2：agent 完成后进入 Human Review

作为开发者，我希望 agent 完成实现后提交 handoff，系统自动生成 review packet，并把 issue 移动到 Human Review，以便我可以审查代码、测试结果和风险。

验收：

```text
agent 调用 symphony tool handoff
handoff 写入 handoff row / comment / tool call record
review packet 生成
review packet status = generated 后 issue state = Human Review
Review 页面展示 summary、diff、tests、risks、commands
```

### 用户故事 3：人工决定 Rework 或 Done

作为开发者，我希望在 Review 页面决定把任务退回 Rework 或标记 Done，以便保留最终交付控制权。

验收：

```text
Human Review issue 可 Send to Rework
Human Review issue 可 Mark Done
没有 review packet 不允许 Done
agent 默认不能 Done
agent 默认不能 push / PR / merge
```

### 用户故事 4：出现审批或失败时可以诊断

作为 operator，我希望看到 agent 请求了什么命令、为什么卡住、哪个工具失败、workflow 是否有效，以便我可以安全地批准、拒绝或修复问题。

验收：

```text
Approval Inbox 展示待审批命令 / 文件 / 网络请求
Run Detail 展示归一化 timeline
Diagnostics 展示 Codex、Git、workflow、DB 状态
失败有明确类别：prompt / codex / approval / tool / handoff / workspace
```

## 4. v1 必做能力

| 模块 | v1 范围 |
|---|---|
| Local Tracker | issue CRUD、状态机、priority、labels、comments、blockers |
| Orchestrator | poll、manual dispatch、claim、run status、cancel、failure pause/resume；不实现自动 retry queue/timers |
| Workspace / Git | 每 issue 一个 `git worktree` + branch，workspace 复用 |
| Codex Runner | 每 run 一个 `codex app-server` 子进程；adapter 隔离目标 Codex protocol |
| Prompt | Runtime Envelope + WORKFLOW Prompt + Context Pack |
| Tool Gateway | `symphony tool` CLI + 本地 IPC + run-scoped token |
| Handoff | `symphony tool handoff` 原子提交数据；review-packet finalizer 成功后进入 `Human Review` |
| Review Packet | diff、changed files、summary、tests、risks、commands、tool calls |
| UI | React Dashboard：Overview、Board、Issue、Run、Approval、Review、Workflow、Diagnostics |
| API | `/api/v1` REST + SSE |
| Security | loopback API、session token、CSRF、network deny、command policy、redaction |
| CLI | init、serve、issue、run、review、workflow、diagnostics、tool |

## 5. v1 不做能力

| 不做 | 后续版本 |
|---|---|
| 桌面壳 Tauri | v2 |
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

## 6. 成功指标

v1 成功的标准不是“agent 自动完成所有任务”，而是：

```text
主路径稳定通过
每个 agent run 都有可审查 trace
每次交付都有 review packet
人工始终掌握 Done / publish 权限
失败原因可以被定位
安全默认值不会裸奔
```
