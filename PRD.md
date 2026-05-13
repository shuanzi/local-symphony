# Local Symphony App v1 PRD

**状态**：v1 产品方案合并版  
**更新日期**：2026-05-11  
**来源**：`local-symphony.zip` 原始文档包经 agent-executable hardening 后更新  
**文档权威性**：本文档是 Local Symphony App v1 的唯一产品需求文档。技术实现、API、DB、CLI、安全、测试与发布细节以 `TECH_SPEC.md` 为准。

---

## 1. 产品摘要

Local Symphony App v1 是一个 **local-first agent engineering workflow control plane**。它在本地 Git 仓库中运行，把本地工程任务转化为可以由 Codex 执行、由 operator 观察、审批、复核、返工和留存记录的完整工程流程。

v1 的产品目标不是最大自动化，而是建立一条可靠、可观察、可审查、可恢复的本地 agent 工作流：

```text
symphony init
  ↓
创建本地 issue
  ↓
issue → Ready
  ↓
手动或 orchestrator dispatch
  ↓
创建 / 复用 git worktree + branch
  ↓
启动 Codex app-server
  ↓
Codex 在 issue workspace 内工作
  ↓
Codex 调用 symphony tool handoff submit
  ↓
worker terminal finally path 尝试 after_run hook（workspace 已准备时）
  ↓
handoff 存在、after_run 已尝试且无更高优先级失败/取消时生成 review packet
  ↓
issue → Human Review
  ↓
人工 Send to Rework 或 Mark Done
```

v1 是受 OpenAI Symphony SPEC 启发的本地产品变体，不是 upstream SPEC 的逐字实现，也不是“去掉 Linear 的 upstream Symphony”。它明确采用：

```text
tracker.kind = local
SQLite 本地 tracker
git worktree per issue
localhost dashboard/API
CLI/IPC Tool Gateway
two-stage handoff
review packet finalizer
manual failure pause/resume
```

## 2. 背景与问题

Coding agent 能完成大量工程任务，但直接把 agent 接入本地代码库存在几个产品问题：

1. **任务缺少本地 source of truth**：没有 Linear 或其他外部 issue tracker 时，任务状态、评论、阻塞关系、运行记录和复核状态容易散落在聊天记录、终端和临时文件中。
2. **执行缺少隔离**：agent 如果直接在主 repo 工作，容易污染主 working tree，也难以在失败、返工和复核之间保留清晰边界。
3. **交接不可复核**：agent 说“完成了”不等于 reviewer 拿到了 diff、测试结果、风险、验证步骤和上下文。
4. **失败不可诊断**：Codex 协议错误、审批拒绝、hook 失败、缺少 handoff、workspace 冲突等都需要被分类并可视化。
5. **安全边界需要产品化**：本地工具不等于默认放宽安全策略；v1 安全控制以本地 daemon enforcement、Codex-mediated approvals 和 diagnostics 为边界，默认 balanced-secure；command/network/protected path 控制不能描述为 OS-level isolation。

Local Symphony App v1 通过本地 tracker、orchestrator、workspace manager、Codex runner、tool gateway、review packet 和 dashboard 把这些问题变成一套固定工作流。

## 3. 与 upstream Symphony SPEC 的关系

上游 Symphony SPEC 的核心思想是：用一个长期运行的服务从 issue tracker 读取工作项，为每个 issue 创建隔离 workspace，并在该 workspace 内运行 coding agent；服务提供调度、workspace、workflow config 和可观察性能力。

Local Symphony App v1 继承以下理念：

```text
daemon-style orchestrator
repo-owned WORKFLOW.md
per-issue isolated workspace
bounded concurrency
agent runner abstraction
status/logging surface
active run reconciliation
```

但 v1 有明确本地化决策：

| 主题 | Local v1 决策 |
|---|---|
| Tracker | 只支持 local tracker，不实现 Linear adapter。 |
| Source of truth | Project SQLite DB 是本地 issue/run/review source of truth。 |
| Workspace | 每个 issue 一个 git worktree，workspace 保留，不自动清理。 |
| Review | workspace 已准备时所有 terminal outcome 都尝试 `after_run`；只有 handoff 存在、`after_run` 已尝试且 review packet 成功生成后才进入 Human Review。 |
| Done | 只能由 operator 触发，agent 不能 Done。 |
| Retry | v1 没有自动 retry queue/timers；失败后 pause，人工 resume。 |
| Publish | v1 不 push、不创建 PR、不 merge、不自动 commit。 |
| UI | localhost dashboard/API，不做 remote dashboard 或多租户 RBAC。 |

## 4. 目标用户

v1 面向本地开发者和小团队 operator：

- 希望让 Codex 在本地仓库中执行工程任务，但需要可审查交付物的人。
- 希望不依赖 Linear、GitHub Issues 或其他外部 tracker，就能管理 agent task lifecycle 的人。
- 希望把 agent 的执行过程、审批、工具调用、失败原因和 review packet 留存在本地的人。
- 希望在引入更高自动化之前，先建立可控、可调试 MVP 工作流的团队。

v1 不是远程 SaaS、多租户平台、完整 CI/CD、通用 workflow engine 或 Git provider automation 产品。

## 5. v1 产品目标

v1 必须达成以下产品目标：

1. **本地 issue tracker 可用**：operator 能创建、编辑、评论、阻塞、状态流转、dispatch、pause/resume 本地 issue。
2. **Codex 执行可隔离**：每个 issue 在独立 git worktree 和 branch 内执行，不污染主 repo working tree。
3. **调度可控**：orchestrator 负责 dispatch、并发、active run reconciliation、取消、失败分类和暂停。
4. **agent 工具受控**：agent 只能通过固定 Tool Gateway registry 修改当前 issue、提交 artifact、创建 follow-up、提交 handoff 或 block 当前 issue。
5. **交接可复核**：Human Review 前必须生成 review packet，包含 handoff、diff、changed files、untracked files、测试、审批、tool calls 和 prompt snapshot metadata。
6. **人工拥有最终决策权**：operator 决定 Rework 或 Done；Done 不触发 commit/push/merge/cleanup。
7. **安全边界明确**：loopback API、session/CSRF、CLI bearer、run-scoped tool token、protected path、redaction、artifact containment、command/network policy 都必须有产品体现。
8. **失败可诊断**：dashboard、CLI、run events 和 diagnostics 能显示 workflow、Codex、Git、DB、approval、tool、review packet 等失败原因。

## 6. v1 非目标

v1 明确不做：

```text
Linear tracker dependency or adapter
Tauri desktop shell
remote dashboard
multi-tenant RBAC
automatic PR creation
git push / merge / publish automation
agent automatic commit
automatic SQLite backup
database migration / rollback framework
automatic retry queue or timers
crash recovery beyond startup stale-run interruption
full compliance-grade audit log
supply-chain deep risk scoring
dynamic tools / MCP
automatic workspace cleanup/delete/reset/rebase
raw prompt or raw Codex log export through v1 API
```

v1 supported platform scope：产品支持 macOS arm64/x64 和 Linux x64。Windows 为 best-effort，不作为 v1 产品验收阻塞；具体平台要求以 `TECH_SPEC.md` 第 4A 节为准。

## 7. 核心用户故事

### 7.1 创建本地任务并派发给 Codex

作为 operator，我可以在本地项目中执行 `symphony init` 初始化 Local Symphony，然后创建一个本地 issue，填写 title、description、acceptance criteria、priority、label，把它从 `Inbox` 移到 `Ready`，并通过 dashboard 或 CLI dispatch。

成功后，系统必须创建或复用该 issue 的 git worktree 和 branch，并启动 Codex app-server。Codex 的 cwd 必须是 issue workspace。

### 7.2 agent 完成后进入 Human Review

作为 operator，我希望 Codex 完成任务后不能直接把 issue 标成 Done，而是提交 handoff。只要 workspace 已准备，系统必须在所有 terminal worker outcome 的 finally path 中尝试 `after_run` hook。只有 handoff 存在、`after_run` 已尝试、且没有更高优先级失败或取消时，系统才生成 review packet；只有 review packet 生成成功，issue 才能进入 `Human Review`。

### 7.3 人工决定 Rework 或 Done

作为 reviewer，我可以打开 Review Packet 页面查看 summary、acceptance criteria、handoff、changed files、diff、tests、risks、verification、approvals、tool calls 和 continuation 指引。

如果需要返工，我可以 Send to Rework。系统保留 workspace 和 branch，让下一次 rework run 继续在同一 workspace 内工作，并生成新的 cumulative review packet。

如果接受结果，我可以 Mark Done。Mark Done 只完成本地 issue 接受与记录，包括完成时间、状态历史、operator comment 和事件；不自动 commit、push、merge、create PR 或删除 workspace。

### 7.4 出现审批、失败或阻塞时可以诊断

作为 operator，我可以在 dashboard 或 CLI 中看到 pending approvals、run timeline、failure_code、dispatch_paused 原因、workflow validation、Codex availability、Git/DB 路径和 redacted diagnostics。

当失败、operator cancel、approval `cancel_run`、agent `issue.block`，或 missing handoff 已耗尽配置允许的 handoff continuation 仍未提交 handoff 时，系统必须暂停该 issue 的 dispatch，避免下一次 tick 自动重复运行。默认 `max_handoff_continuations=1` 时，首次 missing handoff 触发 dedicated handoff continuation；若配置为 `0`，首次 missing handoff 直接进入终止并 pause 路径。

## 8. 产品模块范围

### 8.1 Local Tracker

Local Tracker 是 v1 的任务 source of truth。它存储：

```text
issues
labels
comments
blocker relations
state history
dispatch_paused state
run history
review packet references
```

Issue 产品层最小字段边界：

```text
创建 Inbox issue 最小只要求 title。
description 与 acceptance criteria 在 DTO 中始终存在；Inbox 阶段可为空。
进入 Ready 或 dispatch 前，必须补齐并满足必要 issue 字段有效：title trim 后非空；description trim 后非空；acceptance criteria 至少一条 trim 后非空；priority 为 1..5。
title: 必填，简短任务标题。
description: 任务背景、目标和约束；进入 Ready 或 dispatch 前必须非空。
acceptance criteria: 面向验收的可测试 checklist；进入 Ready 或 dispatch 前至少一条非空。
priority: 必填，范围 1..5，1 最高；未指定时默认 3。
labels: 可选，用于分类、筛选、展示和检索。
state: 必填，遵循 PRD 第 9 章状态集合与流转规则；技术枚举以 TECH_SPEC 8.1/12.12 为准。
comments: 可选，记录 operator、agent、reviewer 的讨论和决策。
blocked_by / blocks: 只展示直接 blocker relation；不在产品层展开传递依赖。
dispatch pause: 展示 dispatch_paused、reason、paused_at，用于解释为何不会自动调度。
workspace / run / review refs: 展示关联 workspace、active/latest run、latest review packet 的引用。
```

详细 API/DTO 字段、required set、序列化形态和兼容 alias 以 TECH_SPEC 的 `NormalizedIssue` 为准；PRD 不重复定义完整技术字段。

Issue human identifier 使用项目 issue prefix 和 sequence，例如 `LOC-1`。内部 ID 使用 opaque prefixed ID，例如 `iss_...`。

### 8.2 Orchestrator

Orchestrator 是调度和 run lifecycle 的控制中心。它负责：

```text
poll/tick
manual dispatch
bounded concurrency
run claim
active run reconciliation
cancellation
failure classification
dispatch pause/resume
handoff finalizer coordination
startup stale-run guard
```

`manual dispatch` 只表示 operator 指定某个 issue 立即尝试调度；它不得绕过 scheduler 的 dispatch eligibility。scheduler dispatch 与 manual dispatch/API/CLI 必须共用第 10 章定义的 `DispatchIssue` preflight。

产品上，orchestrator 的核心要求是：不要重复乱跑，不要把失败自动隐藏，不要在缺少 review packet 时进入 Human Review。

### 8.3 Workspace / Git

v1 每个 issue 使用独立 git worktree 和 branch：

```text
workspace.root (effective project-scoped root): ~/.symphony/workspaces/<project_id>/
issue workspace path: <workspace.root>/<issue_identifier>/
branch: symphony/<issue_identifier>-<title_slug>-<short_hash>
base_ref: auto by default
```

workspace 在所有 issue state、所有 run terminal outcome、startup stale run `failed/daemon_restarted_run_interrupted`、以及 Rework 场景下均保留。v1 不自动 reset、clean、delete、rebase、push 或 create PR。

### 8.4 Codex Runner

Codex Runner 使用 `codex app-server`。v1 要求 Codex adapter 是 version-fixture gated：只有有 committed fixture 的 Codex protocol version 才可运行；不支持的版本必须在启动真实 Codex process 前失败，并给出 `unsupported_codex_version`。

默认测试使用 fake runner。Real Codex tests 只在显式设置 `SYMPHONY_TEST_CODEX=1` 时运行。

### 8.5 WORKFLOW.md 与 Prompt

`WORKFLOW.md` 是项目拥有的 workflow contract：

- YAML front matter 是配置。
- Markdown body 是 agent prompt。
- Config 字段不支持 `{{ ... }}` 插值。
- 只有 prompt body 支持 Liquid-style variables。
- Prompt body 不允许为空。

v1 默认 prompt 必须告诉 agent：

```text
只在当前 workspace 工作
不 push branch
不创建 PR
不标记 issue Done
除非 operator 在本次 run 外明确要求，否则不要 commit
完成后通过 stdin 提交 handoff JSON
运行 symphony tool handoff submit --json -
不要在 workspace 根目录留下 handoff.json 临时文件
handoff 只提交数据，Human Review 由 finalizer 成功后转换
```

### 8.6 Tool Gateway

Tool Gateway 是 agent 修改系统状态的唯一入口。v1 固定 registry：

```text
issue.get
issue.comment
issue.block
artifact.attach
followup.create
handoff.submit
```

agent 不能通过工具删除 issue、设置 Done、任意状态流转、修改其他 issue、读取 secrets、删除 workspace、push、create PR 或访问 project settings。

### 8.7 Handoff

v1 handoff 是 two-stage：

```text
1. agent 调用 handoff.submit，系统记录 handoff 数据。
2. worker 进入 terminal outcome；如果 workspace 已准备，finally path 必须尝试 after_run hook。
3. 只有 handoff 存在、after_run 已尝试且无更高优先级失败/取消时，finalizer 生成 review packet。
4. finalizer 成功后 issue 才进入 Human Review。
```

`handoff.submit` 本身永远不能直接把 issue 转成 `Human Review` 或 `Done`。v1 唯一 handoff target 是 `Human Review`。

`handoff.submit` payload 必填字段：

```text
summary
changed_files
tests
risks
verification
followups
```

其中 `followups` 必须提供，但可以为空数组。

`handoff.submit` payload 可选字段：

```text
target_state
```

`target_state` 可省略；若提供，必须为 `Human Review`。

同一 run 内首个 handoff wins。后续提交与已记录 handoff 的 canonical payload hash 相同时，视为幂等成功并返回同一 handoff；canonical payload hash 不同时，必须返回 state conflict，且不得推进 `Human Review`。

### 8.8 Review Packet

Review Packet 是 Human Review 的交付物。必须包含：

```text
review.md
review.json
changes.patch
changed-files.txt
untracked-files.json
```

并应包含：

```text
diffstat.txt
agent-final-message.md
test-output.txt
commands.jsonl
tool-calls.jsonl
approvals.jsonl
codex-events.redacted.jsonl
prompt/context.json
prompt/rendered_prompt.redacted.md
prompt/prompt_meta.json
prompt/tool_manifest.md
```

`review_packets.status=generated` 由 critical files 决定：`review.md`、`review.json`、`changes.patch`、`changed-files.txt`、`untracked-files.json` 必须全部存在且可登记为 artifacts。非 critical 文件缺失不得阻断 `generated`，但必须在 review metadata 或 diagnostics 中记录缺失文件名、缺失原因和生成阶段；不得静默遗漏。

`review.json` 必须是 Review Packet 的结构化 source of truth，覆盖 issue、run、git、files、handoff、changed_files、untracked_files、approvals、tool_calls、prompt_snapshot 和 failure metadata。

`review.md` 必须包含固定章节：

```text
Summary
Acceptance Criteria
Handoff
Changed Files
Tests
Risks
Verification Steps
Approvals
Tool Calls
Git
How to Continue
```

Review Packet 中的 Prompt snapshot 文件必须遵循 8.11 的脱敏和内容访问策略。它们只能包含 redacted content 和 safe metadata，例如 hash、长度类别、字段名和安全摘要。不得包含 raw secrets、raw rendered prompt content、raw prompt context values 或 raw Codex logs。

Review Packet UI/API 不得通过直接读取 packet 文件绕过脱敏。当 packet 条目指向 raw prompt 或 raw Codex logs 等不允许暴露的内容时，API 只能暴露 metadata、返回 `content_url=null`，或由 Artifact API 返回 refusal/error。

untracked files 必须进入 `changed-files.txt` 和 `untracked-files.json`，默认应进入 `changes.patch`。若因大文件、二进制或策略限制不能写入 patch，必须在 review metadata 中标记 `patch_included=false` 并记录 reason；不能因为 agent 没有 stage 文件或 patch 省略而静默遗漏新文件。

### 8.9 Dashboard

v1 Dashboard 是本地控制面，只通过 REST/SSE API 访问系统，不直接读写 SQLite、Git、filesystem、Codex 或 Tool Gateway。

必须提供页面：

| 页面 | 目的 |
|---|---|
| Overview | workflow status、running runs、pending approvals、failed runs、Human Review count、paused issues、Codex availability、recent events。 |
| Board | 展示 issue 状态列，支持 create、transition、dispatch、打开 review/run。 |
| Issue Detail | 展示 issue facts、comments、blockers、workspace、run history、review packets、dispatch pause/resume。 |
| Run Detail | 展示 normalized timeline、failure、approval、tool、handoff、review generated。 |
| Approval Inbox | 展示 command/file/network approvals 并支持 decide。 |
| Review Packet | 展示 review packet 并支持 Send to Rework / Mark Done。 |
| Workflow | 展示 validation、last valid config、warnings/errors、reload、render preview。 |
| Diagnostics | 展示 daemon、Codex、Git、DB、workflow、redacted export。 |

Approval Inbox 的 decision 枚举为 `approve_once`、`approve_for_run`、`approve_for_session`、`deny`、`cancel_run`。只有 pending approval 可以被决定；`deny` 只拒绝当前 approval action，不等同于取消 run，也不触发 `operator_cancelled` side effects。Codex 接收 decline 后，run 是否继续或以失败终止由 Codex/adapter 的 terminal outcome 决定；如果 denial 导致 terminal policy failure，必须使用对应 canonical `failure_code`，例如 `command_denied`、`network_denied` 或 `protected_path_denied`，并按失败路径 pause dispatch。只有 `cancel_run` 会立即取消当前 run，并触发 `operator_cancelled` side effects。

### 8.10 REST/SSE API 与 CLI

REST API 服务 dashboard、operator CLI 和未来 desktop shell。SSE 用于 live timeline 和 event replay。

CLI 包括：

```text
symphony init
symphony serve
symphony open
symphony status
symphony issue ...
symphony run ...
symphony approval ...
symphony review ...
symphony workflow ...
symphony diagnostics ...
symphony tool ...
```

`symphony status` 使用 `/api/v1/state` 输出与 Overview 对齐的 concise project/daemon/workflow/run/approval/review/paused/Codex/failure 状态；daemon 不可用时只能降级到 `/health` 可用性信息或报错。

正常 CLI 是 operator 工具，走 REST API。`symphony tool ...` 是 agent 工具入口，走 Tool Gateway 本地传输，不走 REST `/api/v1`；transport 可为 Unix socket、named pipe 或 loopback HTTP，具体以 TECH_SPEC 为准，并且必须 JSON-only 输出。

REST 与 CLI 的 approval decide 必须使用相同 decision 语义：`approve_once`、`approve_for_run`、`approve_for_session`、`deny`、`cancel_run`；非 pending approval 不允许被 decide，必须作为 state conflict 失败且不得写入新的 decision 或产生 side effects。Dashboard、REST 与 CLI 必须把 `deny` 呈现为“拒绝当前 action”，把 `cancel_run` 呈现为“取消整个 run”，二者不得共用 side effects。

### 8.11 Security / Operations

v1 是本地工具，不是远程多租户系统。默认安全模式是 balanced-secure：

```text
API 绑定 loopback
browser session cookie + CSRF hard enforcement
CLI bearer token hard enforcement
open token one-time exchange
run-scoped tool token + Tool Gateway scope hard enforcement
command/file/network approvals are Codex-mediated
network default deny: unknown network requests auto-deny via Codex-mediated policy bridge; Approval Inbox is used only when network policy explicitly reviews
protected paths: Codex-mediated for Codex file access; artifact.attach protected-path input is daemon-side hard deny
artifact/export containment hard enforcement
redaction and redacted diagnostics export hard enforcement
raw prompt, raw prompt context values, raw secrets, and raw Codex logs are never served through Review Packet, Review API, Artifact API, diagnostics, or dashboard surfaces
diagnostic-only events are observation/reporting, not prevention
```

v1 不实现 remote dashboard 或多租户 RBAC。权限按本地入口和 token 类型固定：

| Actor / entrypoint | Auth material | v1 authorization |
|---|---|---|
| local operator browser | loopback browser session cookie + CSRF | 完整 operator command 权限；只能通过 REST/SSE API 操作系统，不直接读写 DB/Git/filesystem/Codex/Tool Gateway。 |
| operator CLI | CLI bearer token | 与 authenticated browser 等价的完整 operator command 权限；用于普通 `symphony ...` REST 命令。 |
| future desktop shell | authenticated local operator session or equivalent local token | v1 不交付 desktop shell；未来若作为本地 UI wrapper，也只能继承 operator command 权限，不获得额外 backend 绕过能力。 |
| agent Tool Gateway | run-scoped tool token | 只能访问当前 run scope 内固定 Tool Gateway registry；不得调用 REST `/api/v1` operator command API，不得标记 Done。 |
| unauthenticated | none, or invalid/expired token | 仅可使用 bootstrap 能力，例如 `GET /api/v1/health` 和带有效 one-time open token 的 auth exchange；不得访问 project state、commands、SSE、artifacts、diagnostics 或 Tool Gateway。 |

Codex-mediated command auto-deny、network auto-deny、protected-path read/write deny 必须写入 approval row，并将状态标为 `auto_denied`；同时必须终止当前 run，设置对应 `failure_code`：`command_denied`、`network_denied` 或 `protected_path_denied`，并按失败路径 pause dispatch。默认 unknown network request 使用 auto-deny，不进入 Approval Inbox；只有 network policy 明确返回 `review` 时，才创建 pending operator decision 并出现在 Approval Inbox。

Tool Gateway `artifact.attach` 命中 protected path 是另一类拒绝：daemon 必须 hard deny 该 tool call、记录 failed tool_call 并向 agent 返回 tool error；不得写入 approval row，也不得由该拒绝本身直接终止 run。若 agent 后续无法恢复或完成任务，run 才按正常失败路径进入终止状态。

产品文案必须区分三层安全边界：

- Hard daemon/API enforcement：API auth/CSRF、CLI bearer、open token、Tool Gateway token/scope/cwd/schema/registry、`artifact.attach` protected-path 拒绝、artifact/export containment、diagnostics export/redaction。
- Codex-mediated enforcement：command/file/network approvals，以及 Codex 暴露的 network deny、protected-path read/write deny。
- Diagnostic-only：命令已执行后观察到的事件、日志、failure/reporting，不应表述为预防性隔离。

诚实边界：v1 不是 OS-level firewall，也不是完整文件系统沙箱。Codex 活动中的 network deny 和 protected-path file access deny 依赖 Codex approval/sandbox surfacing；daemon/API 入口仍必须对 auth/CSRF、Tool Gateway scope、artifact attach protected-path、artifact/export containment 与 redacted diagnostics export 做 hard enforcement。

## 8A. v1 可执行合同交付物

为了让 implementation agent 能基于文档完成开发、测试和验证，v1 文档包除 PRD / TECH_SPEC 外，还必须包含以下可执行合同：

| 类别 | 文件 | 用途 |
|---|---|---|
| REST/SSE API | `api/openapi.yaml` | 生成 API client、handler stub、contract tests。 |
| App DB | `db/schema/v1_app.sql` | daemon/app 级项目注册、session、open token、runtime 元数据。 |
| Project DB | `db/schema/v1_project.sql` | issue、run、event、approval、tool、handoff、review packet 本地 source of truth。 |
| JSON Schema | `schemas/*.schema.json`、`schemas/tools/*.input.schema.json` | 校验 WORKFLOW config、NormalizedIssue、RunEvent、Tool Gateway、各工具输入、Review Packet、Diagnostics、FailureCode。 |
| 默认模板 | `examples/WORKFLOW.default.md` | `symphony init` 默认生成的 repo-owned workflow。 |
| 开发任务包 | `docs/agent_work_orders/*.md` | 包含 `M0_*.md` 至 `M8_*.md` milestone 任务单，以及该目录下的 `README.md`、`EXECUTION_PROTOCOL.md` 合同说明；共同避免 agent 自行发散。 |
| 验收材料 | `docs/testing/*` | 给 test/review agent 执行端到端验证。 |
| Codex 映射 | `docs/codex/*` | 固定 Codex app-server protocol fixture gate 和事件映射。 |

这些文件不取代 PRD/TECH_SPEC，但 implementation agent MUST 以它们作为实现、生成类型、写测试和验收的输入。

## 9. Issue 状态机与业务规则

v1 issue states：

```text
Inbox
Ready
Working
Rework
Blocked
Human Review
Done
Cancelled
Duplicate
```

状态语义：

| State | 产品语义 |
|---|---|
| Inbox | 新建任务，尚未准备 dispatch。 |
| Ready | operator 确认可由 scheduler 或手动 dispatch。 |
| Working | orchestrator 已 claim，run 正在执行；只用于 active run reconciliation，不是普通 tick 的自动候选状态。 |
| Rework | review 后要求返工，可再次 dispatch。 |
| Blocked | 当前任务被 operator 或 agent 明确标记为阻塞，需要人工处理。 |
| Human Review | review packet 已生成，等待人工复核。 |
| Done | operator 接受结果。 |
| Cancelled | operator 取消任务。 |
| Duplicate | operator 标记重复。 |

允许的核心状态流转：

| From | To | Actor | Guard / Side effect |
|---|---|---|---|
| Inbox | Ready | operator | 必要 issue 字段有效：title trim 后非空；description trim 后非空；acceptance criteria 至少一条 trim 后非空；priority 为 1..5。 |
| Ready | Working | orchestrator | dispatch claim 成功，且必要 issue 字段有效；在 run_attempt 记录 `source_issue_state=Ready`。 |
| Rework | Working | orchestrator | rework dispatch claim 成功，且必要 issue 字段有效；在 run_attempt 记录 `source_issue_state=Rework`。 |
| Working | Human Review | finalizer | handoff 存在且 review packet generated。 |
| Working | Ready/Rework | finalizer/orchestrator | run failure、missing handoff、review packet failure、operator cancel、startup stale run 等失败路径；回到 `run_attempt.source_issue_state` 并设置 `dispatch_paused=true`。 |
| Human Review | Rework | operator | reviewer 提供 reason/feedback；workspace/branch 保留。 |
| Human Review | Done | operator | operator 提供非空 reason/comment；latest review packet generated；latest review_packet.run_id belongs to latest completed handoff run；且无 active run。 |
| any non-terminal | Blocked | operator 或 agent tool | 若有 active run，reconciliation cancel；agent block 会 pause dispatch。 |
| any non-terminal | Cancelled | operator | 若有 active run，reconciliation cancel。 |
| any non-terminal | Duplicate | operator | 若有 active run，reconciliation cancel；如指定 canonical issue，记录 duplicate relation。 |
| Blocked | Ready | operator | 阻塞解除，active blocker relations 不存在，且必要 issue 字段有效。 |
| Done/Cancelled/Duplicate | Inbox/Ready | operator | 只允许显式 reopen；reopen 到 `Ready` 要求必要 issue 字段有效，reopen 到 `Inbox` 仍只要求 title；禁止直接 reopen 到 `Working`、`Human Review`、`Rework` 或 `Blocked`；要求无 active run，且不复用旧 run。reopen 时 `completed_at=null`，清除 `dispatch_paused`/reason/paused_at；workspace、history、review packets、duplicate relation 保留。 |

Reopen 到 `Inbox` 表示任务需要重新整理，不会自动 dispatch。Reopen 到 `Ready` 表示下一次 scheduler tick 可按正常 eligibility 新建 run_attempt。旧 latest review packet 仅作为历史保留；新一轮完成必须来自 reopen 后新 run 生成的 latest review packet。若 reopened Duplicate 不再是重复任务，operator 必须单独移除或失效 duplicate relation。

Terminal states：

```text
Done
Cancelled
Duplicate
```

`Human Review` 不是 terminal。workspace 在所有状态都保留。

Blocked 与 blocker relation 的关系：

```text
Blocked state 表示当前 issue 被显式阻塞，解除需要 operator transition。
issue_relations.blocks 表示依赖型 blocker，会影响 dispatch eligibility。
添加 active blocker relation 不自动改变 issue.state，但会使 Ready/Rework issue 不可 dispatch。
移除最后一个 blocker relation 不自动从 Blocked 转 Ready；operator 仍需显式 transition。
agent issue.block 只设置当前 issue state=Blocked 与 dispatch_paused=true，不创建 blocker relation。
```

## 10. Dispatch 与 pause/resume 规则

scheduler dispatch 与 manual dispatch/API/CLI 共用同一个 `DispatchIssue` preflight。manual dispatch 可以指定单个 issue，但不能绕过 workflow、blocker、active run、pause 或 concurrency 检查。

正常 scheduler 只 claim：

```text
Ready
Rework
```

`Working` 仅用于 active run reconciliation，不是普通 tick 自动 redispatch candidate。无 active run 的 `Working` 不会被 scheduler 自动 redispatch；startup stale-run guard 必须把它作为 inconsistent issue 诊断并恢复，或要求 operator 显式状态转换处理。

无 active run 的 inconsistent `Working` issue 启动/诊断恢复规则：

```text
不得自动 redispatch；不得 create/claim/enqueue/start 新 run
latest/source run_attempt 选择：同一 issue 中 source_issue_state ∈ Ready/Rework 的 latest run_attempt，按 attempt_no DESC / created_at DESC 排序
若存在可恢复 source run：
  issue.state = latest_run_attempt.source_issue_state
  issue.dispatch_paused = true
  issue.dispatch_pause_reason = daemon_restarted_run_interrupted
  issue.dispatch_paused_at = now
  emit diagnostic/system event
  emit issue.state_changed Working -> source_issue_state
若没有可恢复 source run：
  保留 issue.state = Working
  issue.dispatch_paused = true
  issue.dispatch_pause_reason = daemon_restarted_run_interrupted
  issue.dispatch_paused_at = now
  emit inconsistent issue diagnostic/system event
  diagnostics 暴露需要 operator 处理：显式 transition 或 reopen
second scan 仅修复或 pause inconsistent issue 并写 diagnostics；不得修改 terminal run_attempt
```

Issue 可 dispatch 的必要条件：

```text
state ∈ Ready/Rework
必要 issue 字段按 8.1 有效（title、description、acceptance criteria、priority）
not dispatch_paused
no active blocker relations
no active run
workflow valid or last valid config available according to reload semantics
available concurrency slot
```

任一 preflight 条件失败时，dispatch 必须失败且不得创建 `run_attempt`，不得改变 `issue.state`。manual dispatch/API/CLI 必须返回明确失败原因；其中 workflow 无可用有效配置返回 `workflow_invalid`，concurrency slot 不足返回 `concurrency_limit_reached`，其余条件使用对应的 state/blocker/pause/running/invalid request 错误。

Dispatch claim 必须把来源状态写入 run attempt：

```text
Ready  → Working, run_attempt.source_issue_state = Ready
Rework → Working, run_attempt.source_issue_state = Rework
```

会设置 `dispatch_paused=true` 的情况：

```text
run failure
missing handoff after allowed continuation
operator run cancel
approval cancel_run
agent issue.block
startup stale active run interruption
review packet failure
command auto-deny → command_denied
network auto-deny → network_denied
Codex-mediated protected-path read/write auto-deny → protected_path_denied
```

自动 pause 必须统一写入：

```text
issue.dispatch_paused = true
issue.dispatch_pause_reason = <canonical reason>
issue.dispatch_paused_at = now
```

canonical `dispatch_pause_reason` 映射：

| 自动 pause 场景 | canonical reason |
|---|---|
| missing handoff after allowed continuation | `missing_handoff` |
| operator run cancel | `operator_cancelled` |
| approval `cancel_run` | `operator_cancelled` |
| agent `issue.block` | `agent_blocked` |
| startup stale active run interruption / inconsistent Working recovery | `daemon_restarted_run_interrupted` |
| review packet failure | `review_packet_failed` |
| command auto-deny 或 terminal command denial | `command_denied` |
| network auto-deny 或 terminal network denial | `network_denied` |
| Codex-mediated protected-path read/write auto-deny 或 terminal denial | `protected_path_denied` |

通用 run failure 若进入自动 pause，`dispatch_pause_reason` 必须等于对应 terminal `failure_code` 的 canonical 值；不得写自由文本。以上规则不改变 Operator 手动 `dispatch-pause` 的 `request.reason` 语义。

Operator 手动 `dispatch-pause`：

```text
允许：any non-terminal 且 non-archived issue state
拒绝：Done/Cancelled/Duplicate，或 archived_at 非空；返回 invalid_state_transition，需先 reopen 或 transition
要求：reason 为非空字符串
Side effects:
  issue.dispatch_paused = true
  issue.dispatch_pause_reason = request.reason
  issue.dispatch_paused_at = now
  append system event
  append issue comment with operator reason
```

失败或取消路径的 issue state 规则：

```text
若 run_attempt.source_issue_state ∈ Ready/Rework，且 issue 未因 operator transition 或 agent issue.block 转为 Blocked/Cancelled/Duplicate：
  issue.state = run_attempt.source_issue_state
  issue.dispatch_paused = true

若 issue 已因 operator transition 或 agent issue.block 转为 Blocked/Cancelled/Duplicate：
  保留当前 issue.state；agent_blocked 不恢复 run_attempt.source_issue_state
  issue.dispatch_paused = true
```

Operator 手动 `dispatch-resume`：

```text
允许：any non-terminal 且 non-archived issue state
拒绝：Done/Cancelled/Duplicate，或 archived_at 非空；返回 invalid_state_transition，需先 reopen 或 transition
要求：reason 为非空字符串
Side effects:
  clear issue.dispatch_paused
  clear issue.dispatch_pause_reason
  clear issue.dispatch_paused_at
  append system event
  append issue comment with operator reason
禁止：
  不改变 issue.state
  不改变 title/description/acceptance criteria/labels/priority/workspace/git fields
  不移除 blockers
  不自动切换 Blocked 到 Ready/Rework
  不创建、claim、enqueue 或 start run
```

Resume 后，如果 issue 已处于 Ready/Rework 且满足 eligibility，下一次 tick 可以正常 claim。若 operator 需要立即运行，应使用：

```bash
symphony issue dispatch-resume LOC-1 --reason "..."
symphony issue dispatch LOC-1
```

## 11. Review / Rework / Done 规则

### 11.1 Human Review gate

Issue 进入 `Human Review` 必须同时满足：

```text
handoff exists for run
handoff.target_state = Human Review
hooks.after_run 已在 workspace 存在时被尝试执行
critical review packet files 已写入
review_packets.status = generated
run terminal outcome otherwise successful
```

### 11.2 Run outcome precedence / 结果优先级

当一次 run 同时触发多个结束条件时，产品结果按以下优先级判定；高优先级结果不能被低优先级结果覆盖：

本小节中 active-run-valid states 指 `Ready`/`Working`/`Rework`；其中 `Working` 是 active run 的正常状态，不属于普通 dispatch eligibility。

1. finalizer commit 前，operator cancel 或 approval `cancel_run` 优先，run 进入 cancelled，并按第 10 章失败或取消路径 pause dispatch。
2. finalizer commit 前，issue 若已离开 active-run-valid states（`Ready`/`Working`/`Rework`），优先于成功 finalizer；系统不得再把该 run 作为成功 handoff 推入 `Human Review`。
3. 其余结果按顺序判定：startup stale active run interruption；Codex runner / prompt / workspace failure；allowed continuation 后仍 missing handoff；handoff 存在但 review packet failure；handoff 存在且 review packet generated。
4. finalizer commit 已完成并写入 `issue.state=Human Review`、run completed 后，后续 cancel 必须被拒绝为非 active run。

### 11.3 Send to Rework

Send to Rework 必须满足：

```text
issue.state = Human Review
latest review_packet.status = generated
no active run
operator supplies non-empty reason or feedback
```

错误语义：

```text
missing/blank reason -> invalid_request, no mutation
issue not in Human Review -> invalid_state_transition, no mutation
missing/non-generated/mismatched latest review packet -> review_packet_required, no mutation
active run exists -> issue_already_running, no mutation
```

Side effects：

```text
issue.state = Rework
issue.dispatch_paused = false
clear issue.dispatch_pause_reason
clear issue.dispatch_paused_at
insert issue_state_history Human Review → Rework
insert feedback comment
emit review.sent_to_rework
keep same workspace, branch, base_sha
```

### 11.4 Rework review packet

Rework 生成新 review packet，旧 packet 不覆盖。diff 是从 workspace `base_sha` 到当前 workspace tree 的累计 diff，不是相对上一个 review packet 的增量 diff。

### 11.5 Mark Done

Mark Done 必须满足：

```text
issue.state = Human Review
latest review_packet.status = generated
latest review_packet.run_id belongs to latest completed handoff run
no active run
operator supplies non-empty reason/comment
```

Side effects：

```text
issue.state = Done
issue.completed_at = now
insert issue_state_history Human Review → Done
insert operator comment with reason
emit review.marked_done
emit issue.completed
keep same workspace, branch, base_sha, review packets
```

Mark Done 不 commit、不 push、不 merge、不 create PR、不 delete workspace。

## 12. 成功指标

v1 成功的最低指标：

1. operator 可以无外部 tracker 完成主路径：`init → create issue → Ready → dispatch → fake handoff → review packet → Human Review → Done`。
2. review packet 完整性可按字段和行为验收：latest `generated` packet 必须包含并可展示 diff、changed files、untracked files、tests、risks、verification、tool history 和 approval history；若 critical files（`review.md`、`review.json`、`changes.patch`、`changed-files.txt`、`untracked-files.json`）缺失，packet 不得视为 `generated`，也不得通过 Mark Done guard。
3. 失败后 dashboard/CLI 能显示明确 `failure_code` 和 pause 原因。
4. dispatch 不会因为失败、取消、missing handoff 或 block 自动重复运行。
5. workspace 保留且可被 operator 手动检查。
6. default CI 不需要真实 Codex，fake runner 覆盖主路径和关键失败场景。

## 13. v1 验收标准

v1 产品验收必须覆盖：

- 本地 tracker 无 Linear credentials/config/API/code path。
- 主路径完整可运行并最终 Done。
- Handoff target 固定 `Human Review`；其他 target workflow validation fail。
- `handoff.submit` 只记录数据，不直接状态流转。
- `handoff.submit` payload 必填覆盖 summary、changed_files、tests、risks、verification、followups；followups 可为空数组；target_state 可省略，若提供必须为 `Human Review`。
- 同一 run 重复提交相同 canonical payload hash 时幂等返回同一 handoff；不同 canonical payload hash 时返回 state conflict，且 issue 不进入 Human Review。
- Review packet 生成失败时 issue 不进入 Human Review。
- 默认 `max_handoff_continuations=1` 时，首次 missing handoff 触发同一 session/thread 内的 dedicated handoff continuation；continuation 提交 handoff 后进入正常 review finalizer 路径。
- 默认配置下 dedicated handoff continuation 后仍 missing handoff 时，run `completed_without_handoff/missing_handoff`，issue dispatch paused；若配置 `max_handoff_continuations=0`，首次 missing handoff 直接进入同一终止并 pause 路径。
- Operator cancel / approval `cancel_run` 后 run `cancelled/operator_cancelled`，不自动 redispatch。
- Approval Inbox / CLI 只允许对 pending approval 使用受支持 decision 枚举。
- Agent `issue.block` 后 issue `Blocked`，run `cancelled/agent_blocked`，dispatch paused。
- Active run 所属 issue 被转出 Ready/Working/Rework 时，run 被取消并标记合适 `failure_code`，workspace 保留，且不会自动 redispatch。
- Daemon startup 发现 stale active run 时，run 标记 `failed/daemon_restarted_run_interrupted`，issue 恢复到来源状态或保留当前状态，dispatch paused。
- Daemon startup 发现 `Working` issue 无 active run 时，不自动 redispatch；若 latest/source run 可恢复则回到 `Ready/Rework` 并 pause dispatch，否则保留 `Working`、pause dispatch，并在 diagnostics 要求 operator 显式 transition/reopen。
- 普通 untracked 文本文件必须包含在 review packet patch；大文件、二进制或策略限制例外必须进入 `changed-files.txt` 和 `untracked-files.json`，并带 `patch_included=false` 与非空 reason。
- Review Packet prompt snapshot 文件只包含 redacted content / safe metadata；Review API 对 raw prompt/raw Codex log/raw secret 类内容返回 metadata 或 `content_url=null`，Artifact API 内容读取必须 refusal/error。
- Rework 复用 workspace，生成新的 immutable cumulative review packet。
- Protected paths、artifact containment、redaction、loopback/session/CSRF/tool token、command allow/review/deny、network denied fake request、raw prompt/raw Codex log API refusal 等安全控制必须通过 security regression tests；Codex-mediated command/network/protected-path read/write auto-deny 必须写入 approval row `auto_denied`、终止当前 run、设置 `command_denied`/`network_denied`/`protected_path_denied` 并 pause dispatch；默认 unknown network auto-deny 不进入 Approval Inbox，只有 policy 返回 `review` 时才进入 Approval Inbox；Tool Gateway `artifact.attach` protected-path 拒绝必须验证为 failed tool_call + tool error，且不写 approval row、不直接终止 run。
- v1 release 必须满足 `TECH_SPEC.md` 第 20 章 Definition of Done：API/DB/CLI/dashboard conform、Codex adapter fixture-gated、real Codex tests opt-in、raw prompt/raw Codex logs 不暴露、single dist/symphony binary builds，且 known limitations documented。

## 14. 后续版本路线

v1.1 建议方向：

```text
schema migration framework
SQLite backup/restore
crash recovery leases and reconciliation
full audit log
```

v1.2 建议方向：

```text
supply-chain policy
Git provider publish / PR workflow
Tauri desktop shell planning
```

这些都不得进入 v1，除非重新冻结产品范围。
