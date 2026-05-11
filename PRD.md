# Local Symphony App v1 PRD

**状态**：v1 产品方案合并版  
**更新日期**：2026-05-11  
**来源**：`local-symphony.zip` 原始文档包经 agent-executable hardening 后更新  
**文档权威性**：本文档是 Local Symphony App v1 的唯一产品需求文档。技术实现、API、DB、CLI、安全、测试与发布细节以 `TECH_SPEC.md` 为准。

---

## 1. 产品摘要

Local Symphony App v1 是一个 **local-first agent engineering workflow control plane**。它在本地 Git 仓库中运行，把本地工程任务转化为可以由 Codex 执行、由 operator 观察、审批、复核、返工和归档的完整工程流程。

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
执行 after_run hook
  ↓
生成 review packet
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
5. **安全边界需要产品化**：本地工具可以较宽松，但必须明确 session、CSRF、tool token、protected path、artifact containment、redaction、网络和命令审批边界。

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
| Review | handoff 之后必须生成 review packet，成功后才进入 Human Review。 |
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

## 7. 核心用户故事

### 7.1 创建本地任务并派发给 Codex

作为 operator，我可以在本地项目中执行 `symphony init` 初始化 Local Symphony，然后创建一个本地 issue，填写 title、description、acceptance criteria、priority、label，把它从 `Inbox` 移到 `Ready`，并通过 dashboard 或 CLI dispatch。

成功后，系统必须创建或复用该 issue 的 git worktree 和 branch，并启动 Codex app-server。Codex 的 cwd 必须是 issue workspace。

### 7.2 agent 完成后进入 Human Review

作为 operator，我希望 Codex 完成任务后不能直接把 issue 标成 Done，而是提交 handoff。系统必须在 handoff 后执行 `after_run` hook，并生成 review packet。只有 review packet 生成成功，issue 才能进入 `Human Review`。

### 7.3 人工决定 Rework 或 Done

作为 reviewer，我可以打开 Review Packet 页面查看 summary、acceptance criteria、handoff、changed files、diff、tests、risks、verification、approvals、tool calls 和 continuation 指引。

如果需要返工，我可以 Send to Rework。系统保留 workspace 和 branch，让下一次 rework run 继续在同一 workspace 内工作，并生成新的 cumulative review packet。

如果接受结果，我可以 Mark Done。Mark Done 只改变 issue 状态，不自动 commit、push、merge、create PR 或删除 workspace。

### 7.4 出现审批、失败或阻塞时可以诊断

作为 operator，我可以在 dashboard 或 CLI 中看到 pending approvals、run timeline、failure_code、dispatch_paused 原因、workflow validation、Codex availability、Git/DB 路径和 redacted diagnostics。

当失败、operator cancel、approval `cancel_run`、agent `issue.block` 或 missing handoff twice 发生时，系统必须暂停该 issue 的 dispatch，避免下一次 tick 自动重复运行。

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

产品上，orchestrator 的核心要求是：不要重复乱跑，不要把失败自动隐藏，不要在缺少 review packet 时进入 Human Review。

### 8.3 Workspace / Git

v1 每个 issue 使用独立 git worktree 和 branch：

```text
workspace root: ~/.symphony/workspaces/<project_id>/<issue_identifier>/
branch: symphony/<issue_identifier>-<title_slug>-<short_hash>
base_ref: auto by default
```

workspace 在 Done、Cancelled、Duplicate、Blocked、failed、interrupted 和 rework 情况下都保留。v1 不自动 reset、clean、delete、rebase、push 或 create PR。

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
完成后写 handoff.json
运行 symphony tool handoff submit --json ./handoff.json
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
2. worker 执行 after_run hook。
3. finalizer 生成 review packet。
4. finalizer 成功后 issue 才进入 Human Review。
```

`handoff.submit` 本身永远不能直接把 issue 转成 `Human Review` 或 `Done`。v1 唯一 handoff target 是 `Human Review`。

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

untracked files 必须进入 `changed-files.txt` 和 `changes.patch`。不能因为 agent 没有 stage 文件就遗漏新文件。

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

正常 CLI 是 operator 工具，走 REST API。`symphony tool ...` 是 agent 工具入口，走 Tool Gateway IPC，并且必须 JSON-only 输出。

### 8.11 Security / Operations

v1 是本地工具，不是远程多租户系统。默认安全模式是 balanced-secure：

```text
API 绑定 loopback
browser session cookie + CSRF
CLI bearer token
open token one-time exchange
run-scoped tool token
command allow/review/deny
network default deny
protected paths
redaction
artifact containment
redacted diagnostics export only
```

产品文案必须诚实说明：network deny 和 protected path enforcement 依赖 Codex approval/sandbox surfacing，不是 OS-level firewall 或完整文件系统沙箱。

## 8A. v1 可执行合同交付物

为了让 implementation agent 能基于文档完成开发、测试和验证，v1 文档包除 PRD / TECH_SPEC 外，还必须包含以下可执行合同：

| 类别 | 文件 | 用途 |
|---|---|---|
| REST/SSE API | `api/openapi.yaml` | 生成 API client、handler stub、contract tests。 |
| App DB | `db/schema/v1_app.sql` | daemon/app 级项目注册、session、open token、runtime 元数据。 |
| Project DB | `db/schema/v1_project.sql` | issue、run、event、approval、tool、handoff、review packet 本地 source of truth。 |
| JSON Schema | `schemas/*.schema.json`、`schemas/tools/*.input.schema.json` | 校验 WORKFLOW config、NormalizedIssue、RunEvent、Tool Gateway、各工具输入、Review Packet、Diagnostics、FailureCode。 |
| 默认模板 | `examples/WORKFLOW.default.md` | `symphony init` 默认生成的 repo-owned workflow。 |
| 开发任务包 | `docs/agent_work_orders/M0-M8` | 将 v1 拆成可验收 milestone，避免 agent 自行发散。 |
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
| Inbox | Ready | operator | 必要 issue 字段有效。 |
| Ready | Working | orchestrator | dispatch claim 成功，并在 run_attempt 记录 `source_issue_state=Ready`。 |
| Rework | Working | orchestrator | rework dispatch claim 成功，并在 run_attempt 记录 `source_issue_state=Rework`。 |
| Working | Human Review | finalizer | handoff 存在且 review packet generated。 |
| Working | Ready/Rework | finalizer/orchestrator | run failure、missing handoff、review packet failure、operator cancel、startup stale run 等失败路径；回到 `run_attempt.source_issue_state` 并设置 `dispatch_paused=true`。 |
| Human Review | Rework | operator | reviewer 提供 reason/feedback；workspace/branch 保留。 |
| Human Review | Done | operator | latest review packet generated 且无 active run。 |
| any non-terminal | Blocked | operator 或 agent tool | 若有 active run，reconciliation cancel；agent block 会 pause dispatch。 |
| any non-terminal | Cancelled | operator | 若有 active run，reconciliation cancel。 |
| any non-terminal | Duplicate | operator | 若有 active run，reconciliation cancel；如指定 canonical issue，记录 duplicate relation。 |
| Blocked | Ready | operator | 阻塞解除，且 active blocker relations 不存在。 |
| Done/Cancelled/Duplicate | non-terminal | operator | 只允许显式 reopen，不复用旧 active run。 |

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

正常 scheduler 只 claim：

```text
Ready
Rework
```

`Working` 仅用于 active run reconciliation 和 operator 显式 recovery，不是普通 tick 自动 redispatch candidate。

Issue 可 dispatch 的必要条件：

```text
state ∈ Ready/Rework
not dispatch_paused
no active blocker relations
no active run
workflow valid or last valid config available according to reload semantics
available concurrency slot
```

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
security auto-deny
```

失败或取消路径的 issue state 规则：

```text
若 run_attempt.source_issue_state ∈ Ready/Rework：
  issue.state = run_attempt.source_issue_state
  issue.dispatch_paused = true

若 issue 已被 operator 显式转到 Blocked/Cancelled/Duplicate：
  保留 operator 目标状态
  issue.dispatch_paused = true
```

Resume 只清除 pause，不改变 issue 内容，不移除 blockers，不自动切换 Blocked，不自动创建新 run。Resume 后，如果 issue 已处于 Ready/Rework 且满足 eligibility，下一次 tick 可以正常 claim。若 operator 需要立即运行，应使用：

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

### 11.2 Send to Rework

Send to Rework 必须满足：

```text
issue.state = Human Review
latest review_packet.status = generated
no active run
operator supplies non-empty reason or feedback
```

Side effects：

```text
issue.state = Rework
clear dispatch_paused
insert feedback comment
emit review.sent_to_rework event
keep same workspace, branch, base_sha
```

### 11.3 Rework review packet

Rework 生成新 review packet，旧 packet 不覆盖。diff 是从 workspace `base_sha` 到当前 workspace tree 的累计 diff，不是相对上一个 review packet 的增量 diff。

### 11.4 Mark Done

Mark Done 必须满足：

```text
issue.state = Human Review
latest review_packet.status = generated
no active run
```

Mark Done 不 commit、不 push、不 merge、不 create PR、不 delete workspace。

## 12. 成功指标

v1 成功的最低指标：

1. operator 可以无外部 tracker 完成主路径：`init → create issue → Ready → dispatch → fake handoff → review packet → Human Review → Done`。
2. review packet 可让 reviewer 独立判断变更质量，包含 diff、tests、risks、verification 和 tool/approval history。
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
- Review packet 生成失败时 issue 不进入 Human Review。
- Missing handoff twice 后 run `completed_without_handoff`，issue dispatch paused。
- Operator cancel / approval `cancel_run` 后 run `cancelled/operator_cancelled`，不自动 redispatch。
- Agent `issue.block` 后 issue `Blocked`，run `cancelled/agent_blocked`，dispatch paused。
- Untracked file 被包含在 review packet patch 中。
- Rework 复用 workspace，生成新的 immutable cumulative review packet。
- Protected paths、artifact containment、redaction、loopback/session/CSRF/tool token 等安全控制通过 regression tests。

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
