# Local Symphony App v1 PRD

**状态**：v1 阶段 D 收口（2026-06-09）
**更新日期**：2026-06-09（阶段 D 收口状态注；产品范围与 §1–§22 一致，未新增/扩大 v1 能力）
**来源**：`local-symphony.zip` 原始文档包经 agent-executable hardening 后更新
**文档权威性**：Local Symphony App v1 的产品事实和产品范围以本 PRD 定义为准。`TECH_SPEC.md` 只作为字段、表结构、API/schema、状态机、校验规则等技术合同细节的 source of truth；它和 executable contracts 不得新增或扩大本 PRD 未定义的 v1 产品能力。
**阶段 D 收口状态指针**：`docs/productization/D6_DOCS_CLOSE_NOTES.md`（R 项 status note 表、文档变更清单、已知限制）

---

## 1. 产品摘要

Local Symphony App v1 是一个 **local-first agent engineering workflow control plane**。它在本地 Git 仓库中运行，把本地工程任务转化为可以由 Codex 执行、由 operator 观察、审批、复核、返工和留存记录的完整工程流程。

v1 的产品目标不是最大自动化，而是建立一条可靠、可观察、可审查、恢复边界可验收的本地 agent 工作流：

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
operator CLI over REST API
agent Tool Gateway over local IPC
two-stage handoff
review packet finalizer
manual failure pause/resume
```

这里的“恢复”只承诺 workspace 保留、失败后 pause、operator 手动 resume/redispatch，以及启动时 stale active-run interruption；v1 不承诺通用 crash recovery 或自动续跑。

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
| Source of truth | Project SQLite DB 是本地 issue/run/event/approval/tool/handoff/review/workflow snapshot/prompt snapshot 等运行事实的 source of truth；v1 产品事实和产品范围以本 PRD 定义为准。`TECH_SPEC.md` 只细化相关技术合同，不新增 PRD 未定义的产品能力。 |
| Workspace | 每个 issue 一个 git worktree，workspace 保留，不自动清理。 |
| Review | workspace 已准备时所有 terminal outcome 都尝试 `after_run`；只有 handoff 存在、`after_run` 已尝试且 review packet 成功生成后才进入 Human Review。 |
| Done | 只能由 operator 触发，agent 不能 Done。 |
| Retry | v1 没有自动 retry queue/timers；失败后 pause，人工 resume。 |
| Publish | v1 不 push、不创建 PR、不 merge、不自动 commit。 |
| UI | localhost dashboard/API，不做 remote dashboard 或多租户 RBAC。 |

## 4. 目标用户

v1 面向本地开发者、单机本地 operator，以及小团队中负责在某个本地 checkout 上运行该控制面的 operator：

- 希望让 Codex 在本地仓库中执行工程任务，但需要可审查交付物的人。
- 希望不依赖 Linear、GitHub Issues 或其他外部 tracker，就能管理 agent task lifecycle 的人。
- 希望把 agent 的执行过程、审批、工具调用、失败原因和 review packet 留存在本地的人。
- 希望在引入更高自动化之前，先建立可控、可调试本地工作流的团队。

v1 的“小团队”含义不是多人同时登录同一远程服务，也不是共享权限模型；v1 只交付单机本地 daemon 与本地 operator 入口。团队协作发生在本地仓库、workspace、review packet 和人工沟通层面，不引入用户身份、租户、审批人归属或远程共享 DB 语义。

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
issue archive/delete lifecycle
automatic SQLite backup/restore
database migration / rollback framework
automatic retry queue or timers
crash recovery beyond startup stale-run interruption
full compliance-grade audit log
supply-chain deep risk scoring
dynamic tools / MCP
automatic workspace cleanup/delete/reset/rebase
raw prompt or raw Codex log export through v1 API
```

v1 supported platform scope：产品支持 macOS arm64/x64 和 Linux x64。Windows 为 best-effort，不作为 v1 产品验收阻塞；v1 不要求发布 Windows binary 或让 Windows CI 成为阻断项。若实现提供 Windows 实验性构建，必须在 known limitations 中标明 named pipe、process group termination、CRLF patch 和 path normalization 的覆盖情况。具体 runtime/build/test matrix、版本和验收细节以 `TECH_SPEC.md` 第 4A 节的技术合同为准，不改变上述平台产品范围。

## 7. 核心用户故事

### 7.1 创建本地任务并派发给 Codex

作为 operator，我可以在本地项目中执行 `symphony init` 初始化 Local Symphony，然后创建一个本地 issue，填写 title、description、acceptance criteria、priority、可选 `labels`，把它从 `Inbox` 移到 `Ready`，并通过 dashboard 或 CLI dispatch。

`symphony init` 的产品结果必须可解释：它只在 Git repository 内初始化 project DB、runtime metadata 所需目录和默认 `WORKFLOW.md`；重复执行应保持幂等，除非已有文件与 v1 生成内容冲突。非 Git repo、无写权限、DB schema version 不支持或默认 workflow 写入冲突时，必须失败且不产生部分初始化状态，并给出 operator 下一步指引。初始化成功后，CLI 输出应指向 `symphony serve`、`symphony open` 和创建首个 issue 的下一步命令；Dashboard 首次打开时必须显示空 Board 和创建 issue 的入口。

dispatch 成功必须先通过 shared `DispatchIssue` preflight。成功后，系统必须创建或复用该 issue 的 git worktree 和 branch，并启动 Codex app-server。Codex 的 cwd 必须是 issue workspace。若 preflight 因 pause、blocked、workflow invalid、concurrency full 等原因失败，系统不得创建 run、workspace 或 process。

### 7.2 agent 完成后进入 Human Review

作为 operator，我希望 Codex 完成任务后不能直接把 issue 标成 Done，而是提交 handoff。只要 workspace 已准备，系统必须在所有 terminal worker outcome 的 finally path 中尝试 `after_run` hook。只有 handoff 存在、`after_run` 已尝试、且依据 PRD §11.2 与 `TECH_SPEC.md` §8.14 Run outcome precedence 允许生成 review packet 时，系统才生成 review packet；只有 review packet 生成成功，issue 才能进入 `Human Review`。

### 7.3 人工决定 Rework 或 Done

作为 reviewer workflow persona，我可以打开 Review Packet 页面查看 summary、acceptance criteria、handoff、changed files、diff、tests、risks、verification、approvals、tool calls 和 continuation 指引。`reviewer` 是 workflow persona，不是 v1 RBAC 或权限角色。

如果需要返工，我可以 Send to Rework。系统保留 workspace 和 branch，让下一次 rework run 继续在同一 workspace 内工作，并生成新的 cumulative review packet。

如果接受结果，我可以 Mark Done。Mark Done 只完成本地 issue 接受与记录，包括完成时间、状态历史、operator comment 和事件；不自动 commit、push、merge、create PR 或删除 workspace。

### 7.4 出现审批、失败或阻塞时可以诊断

作为 operator，我可以在 dashboard 或 CLI 中看到 pending approvals、run timeline、canonical `failure_code`、`dispatch_pause_reason`、workflow validation、Codex availability、Git/DB 路径和符合 diagnostics schema 的 redacted diagnostics。

当失败、operator run cancel、approval `cancel_run`、agent `issue.block`，或 missing handoff 已耗尽配置允许的 handoff continuation 仍未提交 handoff 时，系统必须暂停该 issue 的 dispatch，避免下一次 tick 自动重复运行。默认 `max_handoff_continuations=1` 时，首次 missing handoff 触发 dedicated handoff continuation；若配置为 `0`，首次 missing handoff 直接进入终止并 pause 路径。

## 8. 产品模块范围

### 8.1 Local Tracker

Local Tracker 是 v1 的任务 source of truth。它存储：

```text
issues
labels
comments
issue_relations（blocks / duplicates / followup_of；本 PRD 重点展开 blocker 对 dispatch 的影响）
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
duplicate_of / duplicates: 展示当前 issue 的 active duplicate canonical relation，以及指向当前 issue 的直接 duplicate issue；用于支持误标后从 Issue Detail/API/CLI 发现并解除 relation。
followup_of / followups: 展示当前 issue 来源于哪个 follow-up relation，以及当前 issue 已创建的直接 follow-up issue。
dispatch pause: 展示 dispatch_paused、reason、paused_at，用于解释为何不会自动调度。
workspace / run / review refs: 展示关联 workspace、active/latest run、latest review packet 的引用。
```

详细 API/DTO 字段、required set、序列化形态和兼容 alias 以 TECH_SPEC 的 `NormalizedIssue` 为准；PRD 不重复定义完整技术字段。

Issue human identifier 使用项目 issue prefix 和 sequence，例如 `LOC-1`。内部 ID 使用 opaque prefixed ID，例如 `iss_...`。

Issue 列表与编辑的产品规则：

```text
create: Inbox issue 最小只接受 trim 后非空 title；未指定 priority 时为 3。
update: 可编辑 title、description、acceptance criteria、priority、labels；不得通过 update 改 state、workspace、run、review 或 dispatch pause 字段。
comment: comment body 必须 trim 后非空。
labels: 作为 trim 后非空、lowercase-normalized 的字符串标签存储；同一 issue 内重复 label 必须去重。
list: 必须支持按 state、label、text query、dispatch_paused 过滤，并支持稳定分页；默认排序为 priority ASC、updated_at DESC、identifier ASC。
empty list: API 返回空数组和分页 metadata；Dashboard 显示空态和创建 issue 入口。
invalid input: 返回结构化错误且 no mutation。
```

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
startup inconsistent Working issue guard
```

`manual dispatch` 只表示 operator 指定某个 issue 立即尝试调度；它不得绕过 scheduler 的 dispatch eligibility。scheduler dispatch 与 manual dispatch/API/CLI 必须共用第 10 章定义的 `DispatchIssue` preflight。

产品上，orchestrator 的核心要求是：不要重复乱跑，不要把失败自动隐藏，不要在缺少 review packet 时进入 Human Review。

### 8.3 Workspace / Git

v1 每个 issue 使用独立 git worktree 和 branch：

```text
workspace.root (global workspace root): ~/.symphony/workspaces/
issue workspace path: <workspace.root>/<project_id>/<issue_identifier>/
branch: symphony/<issue_identifier>-<title_slug>-<short_hash>
base_ref: auto by default
```

workspace 在所有 issue state、所有 run terminal outcome、startup stale run `failed/daemon_restarted_run_interrupted`、以及 Rework 场景下均保留。v1 不自动 reset、clean、delete、rebase、push 或 create PR。

### 8.4 Codex Runner

Codex Runner 使用 `codex app-server`。v1 要求 Codex adapter 是 version-fixture gated：prelaunch gate 基于 installed Codex version 与 committed fixture metadata/static compatibility metadata；generated protocol/schema version 来自该 metadata，而不是启动真实 `codex app-server` 后才知道。只有有 committed fixture 的 Codex protocol/schema version 才可运行；不支持的版本必须在启动真实 Codex process 前失败，并给出 `unsupported_codex_version`。若启动后的 initialize handshake 与 metadata 不一致或出现 schema mismatch，run 走 `codex_protocol_error` 失败路径。

真实 Codex adapter 是 v1 release scope，但真实 Codex 兼容性必须通过 committed fixture gate 进入；没有匹配 fixture 的本机 Codex 版本只阻断 real Codex dispatch，不影响 fake runner 主路径和默认 CI。默认测试使用 fake runner。Real Codex tests 只在显式设置 `SYMPHONY_TEST_CODEX=1` 时运行。

> **阶段 D 收口状态（2026-06-09；已同步 2026-06-17 `origin/main`）**：D3 / R14 已把 Codex availability preflight 接到 diagnostics / status / state surface：`symphony diagnostics` / `GET /api/v1/diagnostics` 经 `internal/observability.Diagnostics` 调 `codex.RunPreflight(...)` 投影 `codex.available`、`codex.version`、`codex.support.{cli,model,sandbox}` 与 fixture metadata；`symphony status` / `GET /api/v1/state` 经 `observability.CodexAvailability(...)` 暴露同一类 projection（有 supported fixture 时报真实 version/support/available，无 fixture 时 `available=false` + `warning=unsupported_codex_version`）。当前 diagnostics/state contract 不含 Codex `reason`/`status` 字段，失败分类通过 nullable `last_preflight.failure_*` 与 `warning` 表达。Real Codex dispatch 在 fixtures 缺失时 fail-closed with `unsupported_codex_version`。D5 / R13 release packaging 已随当前 `main` 合入；当前 checkout 包含 `scripts/build-release.sh` 与 `web/package-lock.json`，release artifact 说明见 `docs/RELEASE_NOTES.md`。详见 `docs/productization/D6_DOCS_CLOSE_NOTES.md`。

### 8.5 WORKFLOW.md 与 Prompt

`WORKFLOW.md` 是项目拥有的 workflow contract：

- YAML front matter 是可选配置；缺省时 whole file 作为 prompt body，并使用默认 EffectiveConfig。
- Markdown body 是 agent prompt。
- Config 字段不支持 `{{ ... }}` 插值。
- Config fields 不支持 Liquid 或 partial interpolation；只允许 full-string `$VAR_NAME` 环境变量展开。
- `$VAR_NAME` 只有在环境变量已设置且值非空时才展开；unset 或 empty string 必须成为 workflow validation error，并阻断 dispatch。
- Unknown top-level config keys 只产生 warning，不阻断 dispatch；wrong type、missing required、unsupported enum、env unset-or-empty 等非法配置必须使 workflow invalid 并阻断 dispatch。
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

v1 不提供 commit 自动化、commit API 或 commit CLI。默认 prompt 中的“除非 operator 在本次 run 外明确要求，否则不要 commit”只表示 Codex 不得自行提交；如果 operator 通过 Symphony 之外的上下文明确要求 agent commit，Symphony 仍只把 workspace 当前 tree 作为 review packet 输入，且不会创建、修改、push 或验证 commit。

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

agent 不能通过工具删除 issue、设置 Done、任意状态流转、读取或修改既有非当前 issue、读取 secrets、删除 workspace、push、create PR 或访问 project settings。唯一例外是通过受限 `followup.create` 在当前 issue scope 下创建 follow-up Inbox issue。

`followup.create` 会真实创建一个新的 Inbox issue，并记录 `followup_of` relation；Issue Detail 和 Issue DTO 必须展示 created follow-up 与其来源 issue。它不得把新 issue 设为 Ready/Done，也不得创建 blocker 或 duplicate relation。`handoff.submit.followups` 则只是 review packet 中展示的建议事项，不自动创建 issue，也不与 `followup.create` 自动去重。若 agent 已经通过 `followup.create` 创建了 follow-up issue，handoff 中仍可提到它，但系统只按实际 issue relation 展示真实 follow-up。

### 8.7 Handoff

v1 handoff 是 two-stage：

```text
1. agent 调用 handoff.submit，系统记录 handoff 数据。
2. worker 进入 terminal outcome；如果 workspace 已准备，finally path 必须尝试 after_run hook。
3. 只有 handoff 存在、after_run 已尝试且无更高优先级失败/取消时，finalizer 生成 review packet。
4. finalizer 成功后 issue 才进入 Human Review。
```

workspace 已准备时必须尝试 `after_run`。`after_run` hook failure 本身记录为 event/diagnostics，不因其本身阻断 `Human Review`；只有当它导致 review packet generation failure，或存在更高优先级 terminal outcome 时，issue 才不得进入 `Human Review`。

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

其中 `followups` 必须提供，但可以为空数组。`summary` 必须是 trim 后非空字符串。`changed_files`、`tests`、`risks`、`verification`、`followups` 必须是数组；数组可以为空，以表达“无变更文件上报”“未运行测试”“无已知风险”“无补充验证步骤”或“无建议 follow-up”。数组元素若存在，必须是字符串；空字符串的接受或拒绝以 Tool Gateway input schema 为准。Review Packet UI 对空数组必须显示明确空态，不得把空值解释为成功验证。

字段类型、空字符串/空数组约束、unknown-field rejection 以 `schemas/tools/handoff_submit.input.schema.json` / Tool Gateway input schema 为准。

`handoff.submit` payload 可选字段：

```text
target_state
```

`target_state` 可省略；省略时 accepted / persisted / canonical target_state 默认为 `Human Review`。若提供，必须为 `Human Review`。

同一 run 内首个 handoff wins。后续提交与已记录 handoff 的 canonical payload hash 相同时，视为幂等成功并返回同一 handoff；canonical payload hash 不同时，必须返回 state conflict，且不得推进 `Human Review`。

`handoff.submit` 不自动写入 issue comment。handoff 内容通过 handoff 记录、tool call、event 和 Review Packet 展示；若 agent 需要留下普通讨论评论，必须显式调用 `issue.comment`。

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

`review_packets.status=generated` 必须先通过 critical files 完整性 gate：`review.md`、`review.json`、`changes.patch`、`changed-files.txt`、`untracked-files.json` 必须全部存在且可登记为 artifacts。critical files 完整只是必要条件，不是充分条件；packet 还必须通过 TECH_SPEC 定义的 Review Packet schema、artifact 登记、内容安全/脱敏、生成流程和 finalizer gate，才能视为 `generated`。critical files 缺失的 packet 不得视为 `generated`；`partial` / `failed` packet 可用于诊断查看，但不能满足 Human Review / Mark Done guard。非 critical 文件缺失不得阻断 `generated`，但必须在 review metadata 或 diagnostics 中记录缺失文件名、缺失原因和生成阶段；不得静默遗漏。

`review.json` 必须是文件级 Review Packet schema 的结构化 source of truth，覆盖 issue、run、git、files、handoff、changed_files、untracked_files、approvals、tool_calls、prompt_snapshot 和 failure metadata。handoff 对象必须包含 accepted / canonical `followups` 和固定 `target_state=Human Review`；`changed_files` 由顶层 `changed_files` 字段表达。它不等同于 REST `ReviewPacketSummary`；Review Packet 页面必须通过 Review API 获取 artifact summary，再通过 Artifact API 获取允许展示的内容。

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

`symphony review path` 仅输出 Review API 返回的 metadata/path diagnostics，例如 packet、artifact、path、redacted/content_url 状态和缺失文件诊断；不得读取或打印 packet/raw artifact 内容，也不得绕过 Review API + Artifact API 的 redaction/refusal。

untracked files 必须进入 `changed-files.txt` 和 `untracked-files.json`，默认应进入 `changes.patch`。若因大文件、二进制或策略限制不能写入 patch，必须在 review metadata 中标记 `patch_included=false` 并记录 reason；不能因为 agent 没有 stage 文件或 patch 省略而静默遗漏新文件。

### 8.9 Dashboard

v1 Dashboard 是本地控制面，只通过 REST/SSE API 访问系统，不直接读写 SQLite、Git、filesystem、Codex 或 Tool Gateway。

必须提供页面：

| 页面 | 目的 |
|---|---|
| Overview | workflow status、running runs、pending approvals、failed runs、Human Review count、paused issues、Codex availability、recent events。 |
| Board | 展示 issue 状态列，支持 create、transition、dispatch、打开 review/run。 |
| Issue Detail | 展示 issue facts、comments、blockers、duplicate relations、follow-up relations、workspace、run history、review packets、dispatch pause/resume；对 active duplicate relation 提供 remove action。 |
| Run Detail | 展示 normalized timeline、failure、approval、tool、handoff、review generated；timeline 至少覆盖 workspace prepared、prompt rendered、Codex started、approval requested、tool called、handoff submitted、review generated、failure。 |
| Approval Inbox | 展示 command/file/network approvals、risk level、policy match，并支持 decide。 |
| Review Packet | 通过 Review API 展示 review packet，再通过 Artifact API 读取允许内容，并支持 Send to Rework / Mark Done。 |
| Workflow | 展示 validation、last valid config、warnings/errors、reload、render preview。 |
| Diagnostics | 展示 daemon、project paths、Codex、Git、DB、workflow、redacted export。 |

Approval Inbox 的 decision 枚举为 `approve_once`、`approve_for_run`、`approve_for_session`、`deny`、`cancel_run`。只有 pending approval 可以被决定；`deny` 只拒绝当前 approval action，不等同于取消 run，也不触发 `operator_cancelled` side effects。Codex 接收 decline 后，run 是否继续或以失败终止由 Codex/adapter 的 terminal outcome 决定；如果 denial 导致 terminal policy failure，必须使用对应 canonical `failure_code`，例如 `command_denied`、`network_denied` 或 `protected_path_denied`，并按失败路径 pause dispatch。只有 `cancel_run` 会立即取消当前 run，并触发 `operator_cancelled` side effects。

Dashboard 的 approve 控件必须把 `approve_once`、`approve_for_run`、`approve_for_session` 三个 scope 保持为三个可选择动作；可以分组展示，但不得折叠成单一 approve。Review Packet 页面必须以 `GET /api/v1/reviews/{issue_ref}` 返回的 REST `ReviewPacketSummary` 为入口；该 summary 只包含 artifact metadata 和 summary fields。完整 `review.json` 是 artifact 内容，必须使用返回的 `artifact_id`/`content_url` 通过 Artifact API 获取；不得直接读取 filesystem 中的 packet 文件。

Dashboard 必须为所有页面定义可验收的 loading、empty、error 和 auth/refusal 状态：

```text
loading: command/API 正在执行或 SSE 正在重连时显示非阻塞加载状态。
empty: 无 issue、无 run、无 pending approval、无 review packet、无 diagnostics export 时显示空态和下一步动作。
auth/error: 401/403/CSRF/session expired 必须引导重新打开或重新登录本地 session；不得静默失败。
artifact refusal: raw prompt/raw Codex log/raw secret 等内容只显示 metadata/refusal，不提供直接文件读取入口。
daemon unavailable: Overview/status 显示 daemon 不可用，并提示启动 `symphony serve`。
```

Approval pending 过期时，run 必须以 `approval_timeout` 失败并按失败路径 pause dispatch；Approval Inbox 必须把已过期 approval 与 pending approval 区分展示。对已 resolved、expired、denied 或 auto_denied 的 approval 再次 decide 必须是 state conflict 且 no mutation。

Workflow 页面与 CLI 的产品路径是：validate 只验证当前 filesystem `WORKFLOW.md`，不替换 effective config；render preview 只显示 redacted preview 和 warnings/errors；reload 成功才更新 effective/last-valid workflow。invalid reload 必须保留 last valid config 并阻断后续 dispatch，除非 reload semantics 明确允许使用 last valid config。

### 8.10 REST/SSE API 与 CLI

REST API 服务 dashboard、operator CLI 和未来 desktop shell。SSE 用于 live timeline 和 event replay。

REST/SSE 的可交付合同以 `api/openapi.yaml`、TECH_SPEC 第 12 章和本 PRD 共同冻结；所有业务 JSON API 必须使用 success/error envelope，SSE 不使用 envelope，事件 `id` 必须等于 `run_events.seq`，并支持 `Last-Event-ID` 或 `after_seq` replay。v1 endpoint surface 必须覆盖：

```text
GET /api/v1/health
GET /api/v1/state
GET /api/v1/events
GET /api/v1/events/stream
GET /api/v1/runs/{run_id}/events
GET /api/v1/runs/{run_id}/events/stream
GET /api/v1/issues/{issue_ref}/events/stream
POST /api/v1/auth/exchange
GET /api/v1/auth/session
POST /api/v1/auth/logout
POST /api/v1/auth/open-token
POST /api/v1/auth/cli-token/rotate
GET /api/v1/issues
POST /api/v1/issues
GET /api/v1/issues/{issue_ref}
PATCH /api/v1/issues/{issue_ref}
POST /api/v1/issues/{issue_ref}/transition
POST /api/v1/issues/{issue_ref}/comments
POST /api/v1/issues/{issue_ref}/blockers
DELETE /api/v1/issues/{issue_ref}/blockers/{blocker_issue_ref}
DELETE /api/v1/issues/{issue_ref}/duplicates/{canonical_issue_ref}
POST /api/v1/issues/{issue_ref}/dispatch
POST /api/v1/issues/{issue_ref}/dispatch-pause
POST /api/v1/issues/{issue_ref}/dispatch-resume
GET /api/v1/runs
GET /api/v1/runs/{run_id}
POST /api/v1/runs/{run_id}/cancel
GET /api/v1/approvals
POST /api/v1/approvals/{approval_id}/decide
GET /api/v1/reviews/{issue_ref}
POST /api/v1/reviews/{issue_ref}/send-to-rework
POST /api/v1/reviews/{issue_ref}/mark-done
GET /api/v1/artifacts/{artifact_id}
GET /api/v1/artifacts/{artifact_id}/content
GET /api/v1/workflow
POST /api/v1/workflow/validate
POST /api/v1/workflow/render-preview
POST /api/v1/workflow/reload
GET /api/v1/diagnostics
POST /api/v1/diagnostics/export
```

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

CLI 全局 flags 至少包括 `--project <path>`、`--api-url <url>`、`--json`、`--quiet`、`--no-color`、`--timeout <duration>`；`symphony help`、各 command group help 与实际可执行 subcommand/flags 必须匹配，不得展示未实现或 v1 禁止能力。operator CLI subcommand 必须覆盖 TECH_SPEC 11.2 的 issue create/list/show/update/transition/comment/blocker/duplicate/dispatch/dispatch-pause/dispatch-resume、run list/show/events/cancel、approval list/decide、review/send-to-rework/mark-done/path、workflow validate/reload/show、diagnostics/export；`symphony review path` 只能输出 metadata/path diagnostics，不能读取或打印 packet/raw 内容；`symphony tool ...` 只覆盖 TECH_SPEC 11.4 固定 Tool Gateway registry 映射。`symphony issue dispatch-pause` 与 `symphony issue dispatch-resume` 必须要求 trim 后非空的 `--reason`；active run exists 时必须返回 `issue_already_running` 且不得变更 paused 状态。

CLI exit codes 必须采用 TECH_SPEC 11.1 的 0-9 映射；尤其 daemon/gateway unavailable 为 3，auth failure 为 4，permission/policy denial 为 5，not found 为 6，state conflict 为 7，timeout 为 8，workflow/config error 为 9；API `error.code=approval_not_pending` 必须映射为 7。

`symphony status` 使用 `/api/v1/state` 输出当前 shipped state payload：`project_id`、`repo_root`、`issues`、`runs` 以及 `codex`（Codex availability projection，由 `observability.CodexAvailability(...)` 在每次调用时重新跑 preflight 投影，与 `symphony diagnostics` / `GET /api/v1/diagnostics` 同源）；`codex` 字段覆盖 `available`、`version`、`support.{cli,model,sandbox}`、`metadata`、`fixture_support`、`last_preflight` 与 `warning`（有 supported fixture 时报真实 version/support/available，无 fixture 时 `available=false` + `warning=unsupported_codex_version`）。daemon 不可用时只能降级到 `/health` 可用性信息或报错。daemon/workflow/approval/review/paused/failure 聚合属于后续 Overview/status 产品化扩展，不由当前 `/api/v1/state` 合同承诺。当前 state contract 不含 Codex `reason` / `status` 字段，失败分类通过 nullable `last_preflight.failure_*` 与 `warning` 表达；`status` / `state` 经 `observability.CodexAvailability(...)` 暴露 Codex availability/support projection，但不得额外合成 diagnostics/state 合同未定义的 Codex 字段。

正常 CLI 是 operator 工具，走 REST API。`symphony tool ...` 是 agent 工具入口，走 Tool Gateway 本地传输，不走 REST `/api/v1`；transport 可为 Unix socket、named pipe 或 loopback HTTP，具体以 TECH_SPEC 为准，并且必须 JSON-only 输出。

当 app/project DB schema version 不受当前 v1 binary 支持时，CLI、Dashboard 和 diagnostics 必须只读失败，展示检测到的版本、期望版本、DB 路径，以及 operator 可执行恢复指引：使用兼容 binary、人工恢复备份，或初始化新的 project DB。v1 不自动 migration、rollback、backup 或 restore。

REST 与 CLI 的 approval decide 必须使用相同 decision 语义：`approve_once`、`approve_for_run`、`approve_for_session`、`deny`、`cancel_run`；非 pending approval 不允许被 decide，必须作为 state conflict 失败且不得写入新的 decision 或产生 side effects。Dashboard、REST 与 CLI 必须把 `deny` 呈现为“拒绝当前 action”，把 `cancel_run` 呈现为“取消整个 run”，二者不得共用 side effects。

v1 不得暴露或预留隐藏 CLI/API/stub：publish、PR/create-pr、backup、migrate、audit、workspace-delete、secret、git push/merge/publish、issue delete、arbitrary state mutation、project settings mutation、remote dashboard/RBAC/desktop shell backend bypass。OpenAPI、CLI help、handler registry、dashboard affordance 与 tool registry 都不得包含这些能力。

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
raw secret/content refusal and redacted-only diagnostics export hard enforcement
raw prompt, raw prompt context values, raw secrets, and raw Codex logs are never served through Review Packet, Review API, Artifact API, diagnostics, or dashboard surfaces
diagnostic-only events are observation/reporting, not prevention
```

balanced-secure 是产品安全 baseline；具体 policy knobs（例如 `approvals.mode`、`network.default`、allowlist/review/deny 规则）以 TECH_SPEC 的 config 合同为准，PRD 不引入新的配置项。默认 unknown network request 使用 auto-deny；`approvals.mode=balanced` 不会把 `network.default=deny` 改成 operator review。

Secret detection 与 redaction quality 是 best-effort / non-compliance-grade；v1 不承诺发现所有 secret、证明无敏感信息泄漏，或提供合规级 DLP。Hard enforcement 只覆盖已判定 raw prompt、raw prompt context values、raw secrets、raw Codex logs 等禁止内容时的拒绝暴露、artifact/export containment，以及 diagnostics export 的 redacted-only 合同。

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

Token 生命周期必须可验收：CLI bearer raw token 主路径只写入 project-scoped `~/.symphony/cli-sessions/<project>.json`，legacy `~/.symphony/cli-session.json` 仅作为旧版本 fallback/compatibility；在 OS 支持时权限必须为 current-user only，app DB 只保存 hash；runtime descriptor 不得包含 secret。`symphony login --logout` 必须优先请求 daemon 撤销当前 CLI bearer，确认 revoke、token 不匹配或无本地 bearer 可撤销后才删除本地 session 文件；若 daemon-side revoke 无法确认，必须保留本地文件供重试。Open token 必须短 TTL、one-time、hash-only at rest，成功 exchange 后立即失效，重复使用或过期使用返回 unauthorized。CSRF 只强制用于 cookie-authenticated command APIs；只读 state/health 类接口仍必须满足各自 auth 要求，但不得错误要求 CLI bearer 请求携带 CSRF。Tool token 必须绑定 project/issue/run/workspace/allowed_tools/expiry，只能调用固定 Tool Gateway registry；run terminal、operator run cancel、approval `cancel_run`、reconciliation cancel 或 daemon shutdown 时必须 revoke，且不得赋予 REST operator command 权限。

产品文案必须区分三层安全边界：

- Hard daemon/API enforcement：API auth/CSRF、CLI bearer、open token、Tool Gateway token/scope/cwd/schema/registry、`artifact.attach` protected-path 拒绝、artifact/export containment、raw secret/content refusal、redacted-only diagnostics export。
- Codex-mediated enforcement：command/file/network approvals，以及 Codex 暴露的 network deny、protected-path read/write deny。
- Diagnostic-only：命令已执行后观察到的事件、日志、failure/reporting，不应表述为预防性隔离。

诚实边界：v1 不是 OS-level firewall，也不是完整文件系统沙箱。Codex 活动中的 network deny 和 protected-path file access deny 依赖 Codex approval/sandbox surfacing；daemon/API 入口仍必须对 auth/CSRF、Tool Gateway scope、artifact attach protected-path、artifact/export containment、raw secret/content refusal 与 redacted-only diagnostics export 做 hard enforcement；secret detection / redaction quality 仍是 best-effort / non-compliance-grade。

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
| 验收材料 | `docs/testing/*.md` | 给 test/review agent 执行端到端验证。 |
| Codex 映射 | `docs/codex/*.md` | 固定 Codex app-server protocol fixture gate 和事件映射。 |

这些文件不取代 PRD 对产品事实和产品范围的定义；它们与 TECH_SPEC 只作为 implementation agent 实现、生成类型、写测试和验收的技术合同输入。

默认验收和 CI 必须把这些合同作为阻断项：OpenAPI 可解析且覆盖 v1 endpoint、排除 TECH_SPEC 12.11 禁止 API；SQLite schema 可在空 app/project DB 执行；JSON Schema 与工具输入 schema 可解析并参与 fixture 校验；默认 WORKFLOW 模板、docs/testing、docs/codex 和 docs/agent_work_orders 必须通过 TECH_SPEC 18.7 的合同验证；v1 release 还必须满足 TECH_SPEC 第 20 章 Definition of Done。

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
| Working | Ready/Rework | finalizer/orchestrator | run failure、missing handoff、review packet failure、operator run cancel、startup stale run 等失败路径；回到 `run_attempt.source_issue_state` 并设置 `dispatch_paused=true`，除非当前已因 operator transition 或 agent `issue.block` 转为 Blocked/Cancelled/Duplicate，此时保留当前 state。 |
| Human Review | Rework | operator | reviewer 提供非空 reason；workspace/branch 保留；UI 可将 reason 呈现为 feedback。 |
| Human Review | Done | operator | operator 提供非空 reason；latest review packet generated；latest review_packet.run_id belongs to latest completed handoff run；且无 active run；UI 可将 reason 呈现为 comment，持久化可记录 operator comment。 |
| any non-terminal | Blocked | operator 或 agent tool | 若有 active run，必须 reconciliation cancel 并 pause dispatch；agent `issue.block` 使用 `agent_blocked`，ordinary/non-terminal operator transition 使用 reconciliation canonical code `issue_state_changed`。 |
| any non-terminal | Cancelled | operator | 若有 active run，必须 reconciliation cancel 并 pause dispatch；terminal reconciliation 使用 `canceled_by_reconciliation`。 |
| any non-terminal | Duplicate | operator | 若有 active run，必须 reconciliation cancel 并 pause dispatch；terminal reconciliation 使用 `canceled_by_reconciliation`；如指定 canonical issue，按 Duplicate relation 规则记录或拒绝。 |
| Blocked | Ready | operator | 阻塞解除，active blocker relations 不存在，且必要 issue 字段有效；v1 统一解除到 Ready。 |
| Done/Cancelled/Duplicate | Inbox/Ready | operator | 只允许显式 reopen；reopen 到 `Ready` 要求必要 issue 字段有效，reopen 到 `Inbox` 仍只要求 title；禁止直接 reopen 到 `Working`、`Human Review`、`Rework` 或 `Blocked`；要求无 active run，且不复用旧 run。reopen 时 `completed_at=null`，清除 `dispatch_paused`/reason/paused_at；workspace、history、review packets、duplicate relation 保留。 |

Reopen 到 `Inbox` 表示任务需要重新整理，不会自动 dispatch。Reopen 到 `Ready` 表示下一次 scheduler tick 可按正常 eligibility 新建 run_attempt。旧 latest review packet 仅作为历史保留；新一轮完成必须来自 reopen 后新 run 生成的 latest review packet。若 reopened Duplicate 不再是重复任务，operator 必须通过 duplicate relation remove 入口单独移除或失效 duplicate relation。

通用 `transition` 请求必须提供目标 state；转入 `Blocked`、`Cancelled`、`Duplicate` 时，operator reason 必须 trim 后非空并记录为 comment/event。`Human Review -> Rework/Done` 必须走 Review API，不得通过通用 transition 绕过 review packet guard。重复转入当前 state 默认必须返回 state conflict 且 no mutation；`state=Duplicate` 仅有两个例外：`duplicate_of` 与现有 active duplicate relation 相同，成功 no-op 且不重复写 relation/comment/event；当前没有 active duplicate relation 且提供合法 `duplicate_of` 时，成功创建 relation、记录 reason comment/event，但 issue state 不变。已有不同 active relation、缺少必要字段或其他 same-state transition 仍按 conflict/invalid 处理。issue 级 `Cancelled` 表示取消整个任务；run 级 cancel 只取消当前 active run，必须在 Dashboard/CLI 文案中区分。

Duplicate relation 规则：

```text
转为 Duplicate 时 canonical issue 可选；若提供，不能指向当前 issue，必须能解析到同一 project 内 issue。
同一 source issue 同一时间最多只能有一个 active duplicate canonical relation。
从非 Duplicate 状态转为 Duplicate 且未指定 canonical 时，若已有 active duplicate relation 则沿用该 relation；若没有则只改变 state，不创建 relation；已处于 Duplicate 时省略 `duplicate_of` 仍按 same-state conflict/no mutation 处理。
转为 Duplicate 且指定 canonical 时，若无 active duplicate relation 则创建 relation；若已有相同 active relation 则幂等；若已有不同 active relation，必须返回 state conflict/no mutation，operator 需先通过 remove 入口失效旧 relation。
若 issue 已经处于 Duplicate 且旧 relation 已被 remove 到无 active relation，operator 可再次调用 transition 到 Duplicate 并指定新 canonical 来创建新 relation；该路径用于更正 canonical target。
canonical issue 处于 Done/Cancelled/Duplicate 仍可作为历史指向；v1 不做跨 issue 自动状态同步。
duplicate relation remove 入口只做 soft deactivate：设置 relation.active=false 和 resolved_at；对已 inactive 的同一 relation 返回 success no-op。
reopen Duplicate 不自动移除 duplicate relation；operator 必须通过 remove 入口单独解除或失效 relation。
Issue Detail 必须能从当前 issue 发现 active duplicate canonical target，并提供 remove duplicate relation action；该动作只解除 relation，不自动改变 issue state。
```

Terminal states：

```text
Done
Cancelled
Duplicate
```

`Human Review` 不是 terminal。workspace 在所有状态都保留。

Blocked 与 blocker relation 的关系：

```text
Blocked state 表示当前 issue 被显式阻塞，解除需要 operator transition；v1 有意统一解除为 Blocked -> Ready，即使该 issue 是从 Rework 被阻塞，解除阻塞也不会自动回 Rework，历史 review/rework 上下文仅作为记录保留。
issue_relations.blocks 表示直接依赖型 blocker（direct blocker relation），会影响 dispatch eligibility，不在产品层展开传递依赖；issue_relations 还承载 duplicates 与 followup_of。
direct blocker relation 的 source issue 未处于 terminal blocker states 且 relation.active=true 时，该 relation 为 active blocker relation；terminal blocker states 为 `Done/Cancelled/Duplicate`（即 `Done`、`Cancelled`、`Duplicate`）。
添加 active blocker relation 不自动改变 issue.state，但会使 Ready/Rework issue 不可 dispatch。
relation 移除/失效统一采用 soft deactivate：设置 `active=false` 并写入 `resolved_at`；移除最后一个 active blocker relation 不自动从 Blocked 转 Ready，operator 仍需显式 transition。
agent issue.block 只设置当前 issue state=Blocked 与 dispatch_paused=true，不创建 blocker relation。
```

## 10. Dispatch 与 pause/resume 规则

scheduler dispatch 与 manual dispatch/API/CLI 共用同一个 `DispatchIssue` preflight。manual dispatch 可以指定单个 issue，但不能绕过 workflow、blocker、active run、pause 或 concurrency 检查。

正常 scheduler 只 claim：

```text
Ready
Rework
```

`Working` 仅用于 active run reconciliation，不是普通 tick 自动 redispatch candidate。无 active run 的 `Working` 不会被 scheduler 自动 redispatch；startup stale-run guard 必须把它作为 inconsistent issue 诊断并恢复，或要求 operator 按合法间接状态路径处理。

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
  diagnostics 暴露需要 operator 处理：例如 Working -> Blocked -> Ready，或 Working -> Cancelled/Duplicate 后按 reopen 规则显式 reopen 到 Inbox/Ready
second scan 仅修复或 pause inconsistent issue 并写 diagnostics；不得修改 terminal run_attempt
```

非可恢复 `Working` issue 不允许直接 reopen，也不允许直接转到 `Ready`、`Rework`、`Human Review` 或 `Done`；operator 必须选择上面的合法间接路径或其它已定义的合法 transition。

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

任一 preflight 条件失败时，dispatch 必须失败且不得创建 `run_attempt`，不得改变 `issue.state`。manual dispatch/API/CLI 必须返回明确失败原因，并与 TECH_SPEC 8.7 mapping 对齐：

| 失败条件 | ApiErrorCode |
|---|---|
| issue.state 非 Ready/Rework | `invalid_state_transition` |
| 必要 issue 字段无效 | `invalid_request` |
| dispatch 已暂停 | `issue_dispatch_paused` |
| 存在 active blocker relation | `issue_blocked` |
| 已有 active run | `issue_already_running` |
| workflow 无可用有效配置 | `workflow_invalid` |
| concurrency slot 不足 | `concurrency_limit_reached` |

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
ordinary/non-terminal operator transition triggers active run reconciliation cancel
terminal active run reconciliation cancel
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
| ordinary/non-terminal operator transition triggers active run reconciliation cancel | `issue_state_changed` |
| terminal active run reconciliation cancel | `canceled_by_reconciliation` |
| startup stale active run interruption / inconsistent Working recovery | `daemon_restarted_run_interrupted` |
| review packet failure | `review_packet_failed` |
| command auto-deny 或 terminal command denial | `command_denied` |
| network auto-deny 或 terminal network denial | `network_denied` |
| Codex-mediated protected-path read/write auto-deny 或 terminal denial | `protected_path_denied` |

通用 run failure 若进入自动 pause，`dispatch_pause_reason` 必须等于对应 terminal `failure_code` 的 canonical 值；不得写自由文本。以上规则不改变 Operator 手动 `dispatch-pause` 的 `request.reason` 语义。

Operator 手动 `dispatch-pause`：

```text
允许：no active run，且 any non-terminal issue state
拒绝：missing/blank reason；返回 invalid_request，no mutation
拒绝：Done/Cancelled/Duplicate；返回 invalid_state_transition，需先 reopen 或 transition
拒绝：active run exists；返回 issue_already_running，no mutation
要求：reason trim 后非空
Side effects:
  issue.dispatch_paused = true
  issue.dispatch_pause_reason = request.reason
  issue.dispatch_paused_at = now
  append system event
  append issue comment with operator reason
幂等：
  若已处于 dispatch_paused=true，返回 success no-op；保留原 reason/paused_at，不重复写 comment/event
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
允许：no active run，且 any non-terminal issue state
拒绝：missing/blank reason；返回 invalid_request，no mutation
拒绝：Done/Cancelled/Duplicate；返回 invalid_state_transition，需先 reopen 或 transition
拒绝：active run exists；返回 issue_already_running，no mutation
要求：reason trim 后非空
Side effects:
  clear issue.dispatch_paused
  clear issue.dispatch_pause_reason
  clear issue.dispatch_paused_at
  append system event
  append issue comment with operator reason
幂等：
  若已处于 dispatch_paused=false，返回 success no-op；不写 comment/event
禁止：
  不改变 issue.state
  不改变 title/description/acceptance criteria/labels/priority/workspace/git fields
  不移除 blockers
  不自动切换 Blocked 到 Ready/Rework
  不创建、claim、enqueue 或 start run
```

手动 `dispatch-pause` / `dispatch-resume` 要求 no active run；active run exists 时请求被拒绝为 `issue_already_running` 且 no mutation，因此不参与 PRD §11.2 与 `TECH_SPEC.md` §8.14 Run outcome precedence。

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
handoff.target_state = Human Review（省略提交时按 accepted / persisted / canonical 默认值 Human Review 处理）
hooks.after_run 已在 workspace 存在时被尝试执行
critical review packet files 已写入
review_packets.status = generated
run terminal outcome otherwise successful
```

`after_run` hook failure 本身不阻断 `Human Review`，但必须记录 event/diagnostics；若 failure 导致 review packet generation failure，或存在更高优先级 terminal outcome，则不得进入 `Human Review`。

### 11.2 Run outcome precedence / 结果优先级

当一次 run 同时触发多个结束条件时，产品结果按以下优先级判定；高优先级结果不能被低优先级结果覆盖：

本小节中 active-run-valid states 指 `Ready`/`Working`/`Rework`；其中 `Working` 是 active run 的正常状态，不属于普通 dispatch eligibility。

1. finalizer commit 前，operator run cancel 或 approval `cancel_run` 优先，run 进入 cancelled，并按第 10 章失败或取消路径 pause dispatch。
2. finalizer commit 前，issue 若已离开 active-run-valid states（`Ready`/`Working`/`Rework`），优先于成功 finalizer；普通 operator transition 使用 `cancelled/issue_state_changed`，agent `issue.block` 使用 `cancelled/agent_blocked`，terminal reconciliation 使用 `cancelled/canceled_by_reconciliation`；系统不得再把该 run 作为成功 handoff 推入 `Human Review`。
3. 其余结果按顺序判定：startup stale active run interruption；Codex runner / prompt / workspace failure；allowed continuation 后仍 missing handoff；handoff 存在但 review packet failure；handoff 存在且 review packet generated。
4. finalizer commit 已完成并写入 `issue.state=Human Review`、run completed 后，后续 cancel 必须被拒绝为非 active run。

### 11.3 Send to Rework

Send to Rework 必须满足：

```text
issue.state = Human Review
latest review_packet.status = generated
latest review_packet.run_id belongs to latest completed handoff run
no active run
operator supplies non-empty reason
```

UI 可以把 `reason` 呈现为 feedback，但 API/CLI 字段统一为 `reason`，且 trim 后必须非空。latest review packet 必须按该 issue 的 latest packet row（最高 `packet_no`）判定；该 latest packet 必须是 `generated`，且 `run_id` 属于 latest completed handoff run。这里的 mismatched latest review packet 指 latest packet 与 latest completed handoff run 不匹配；不得查找更早的 `generated` packet 来绕过最新 `failed` / `partial` packet。

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
insert operator comment with reason
emit review.sent_to_rework
keep same workspace, branch, base_sha
```

### 11.4 Rework review packet

Rework 生成新 review packet，旧 packet 不覆盖。diff 是从 workspace `base_sha` 到当前 workspace tree 的累计 diff，不是相对上一个 review packet 的增量 diff。

下一次从 Rework dispatch 的 agent run prompt 必须包含 latest review reason 和 previous review packet summary，帮助 agent 明确返工输入。previous review packet summary 的最小字段为 `packet_no`、`run_id`、`handoff.summary`、`changed_files`、`tests`、`risks`、`verification`、`followups`；这些输入只能使用 redacted/safe metadata，并遵循 prompt snapshot 脱敏和 artifact 访问规则。

### 11.5 Mark Done

Mark Done 必须满足：

```text
issue.state = Human Review
latest review_packet.status = generated
latest review_packet.run_id belongs to latest completed handoff run
no active run
operator supplies non-empty reason
```

`reason` trim 后必须非空。UI 可以把 Mark Done 的 `reason` 呈现为 comment；数据库、事件和 issue comment 可以记录 operator comment，但 API/CLI 输入字段仍为 `reason`。latest review packet 必须按该 issue 的 latest packet row（最高 `packet_no`）判定；该 latest packet 必须是 `generated`，且 `run_id` 属于 latest completed handoff run。mismatched latest review packet 指 latest packet 与 latest completed handoff run 不匹配；不得查找更早的 `generated` packet 来绕过最新 `failed` / `partial` packet，Mark Done 不得使用旧 run 的 packet。

错误语义：

```text
missing/blank reason -> invalid_request, no mutation
issue not in Human Review -> invalid_state_transition, no mutation
missing/non-generated/mismatched latest review packet -> review_packet_required, no mutation
active run exists -> issue_already_running, no mutation
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
2. review packet 完整性可按字段和行为验收：latest `generated` packet 必须包含并可展示 diff、changed files、untracked files、tests、risks、verification、tool history 和 approval history；若 critical files（`review.md`、`review.json`、`changes.patch`、`changed-files.txt`、`untracked-files.json`）缺失，或未通过 schema、artifact、安全/脱敏、生成流程等 gate，packet 不得视为 `generated`，也不得通过 Human Review / Mark Done guard。
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
- `partial` / `failed` review packet 不得让 issue 进入 Human Review，也不得通过 Mark Done guard。
- 默认 `max_handoff_continuations=1` 时，首次 missing handoff 触发同一 session/thread 内的 dedicated handoff continuation；continuation 提交 handoff 后进入正常 review finalizer 路径。
- 默认配置下 dedicated handoff continuation 后仍 missing handoff 时，run `completed_without_handoff/missing_handoff`，issue dispatch paused；若配置 `max_handoff_continuations=0`，首次 missing handoff 直接进入同一终止并 pause 路径。
- Operator run cancel / approval `cancel_run` 后 run `cancelled/operator_cancelled`，不自动 redispatch。
- Approval Inbox / CLI 只允许对 pending approval 使用受支持 decision 枚举。
- Agent `issue.block` 后 issue `Blocked`，run `cancelled/agent_blocked`，dispatch paused。
- Active run 所属 issue 被转出 Ready/Working/Rework 时，run 被取消并标记合适 `failure_code`，workspace 保留，且不会自动 redispatch。
- Daemon startup 发现 stale active run 时，run 标记 `failed/daemon_restarted_run_interrupted`，issue 恢复到来源状态或保留当前状态，dispatch paused。
- Daemon startup 发现 `Working` issue 无 active run 时，不自动 redispatch；若 latest/source run 可恢复则回到 `Ready/Rework` 并 pause dispatch，否则保留 `Working`、pause dispatch，并在 diagnostics 要求 operator 通过合法间接路径处理，例如 `Working -> Blocked -> Ready`，或 `Working -> Cancelled/Duplicate` 后按 reopen 规则回到 `Inbox/Ready`。
- 普通 untracked 文本文件必须包含在 review packet patch；大文件、二进制或策略限制例外必须进入 `changed-files.txt` 和 `untracked-files.json`，并带 `patch_included=false` 与非空 reason。
- Review Packet prompt snapshot 文件只包含 redacted content / safe metadata；Review API 对 raw prompt/raw Codex log/raw secret 类内容返回 metadata 或 `content_url=null`，Artifact API 内容读取必须 refusal/error。
- Rework 复用 workspace，生成新的 immutable cumulative review packet。
- Send to Rework 对 blank/trim 后空 `reason`、非 `Human Review`、存在 active run、missing/non-generated/mismatched latest review packet 必须拒绝且 no mutation。
- 下一次 Rework dispatch 的 prompt snapshot/rendered prompt 必须包含 latest review reason 与 previous review packet summary，并遵守脱敏规则；previous review packet summary 的最小字段为 `packet_no`、`run_id`、`handoff.summary`、`changed_files`、`tests`、`risks`、`verification`、`followups`，且只能使用 redacted/safe metadata。
- Mark Done 对 blank/trim 后空 `reason`、非 `Human Review`、存在 active run、missing/non-generated/mismatched latest review packet 必须拒绝且 no mutation；非 `Human Review` 返回 `invalid_state_transition`。
- Review API 不得 inline raw prompt/raw Codex log/raw secret 内容；Artifact API 对这类 raw content 读取必须 refusal/error。
- Protected paths、artifact containment、raw prompt/raw Codex log/raw secret API refusal、redacted-only diagnostics export、loopback/session/CSRF/tool token、command allow/review/deny、network denied fake request 等安全控制必须通过 security regression tests；redaction golden fixtures 只能验证 best-effort / non-compliance-grade 的脱敏质量，不能被表述为合规级或绝对检测能力；Codex-mediated command/network/protected-path read/write auto-deny 必须写入 approval row `auto_denied`、终止当前 run、设置 `command_denied`/`network_denied`/`protected_path_denied` 并 pause dispatch；默认 unknown network auto-deny 不进入 Approval Inbox，只有 policy 返回 `review` 时才进入 Approval Inbox；Tool Gateway `artifact.attach` protected-path 拒绝必须验证为 failed tool_call + tool error，且不写 approval row、不直接终止 run。
- REST/SSE、dashboard 与 CLI 必须符合 OpenAPI、TECH_SPEC 11-12/15.4：business API envelope 一致、SSE `id=run_events.seq` replay 可用、CLI help/flags/subcommands/exit codes 与实现匹配，Review Packet 只能经 Review API + Artifact API 读取内容，Approval Inbox 必须展示 risk level/policy match 并保留三个 approve scope。
- v1 不得暴露 publish/PR/backup/migrate/audit/workspace-delete/secret、remote dashboard/RBAC、desktop shell backend bypass、隐藏 API/CLI/stub 或 dashboard affordance；OpenAPI、CLI help、handler registry、tool registry 与测试合同必须能证明这些能力不存在。
- 默认 CI 必须执行 TECH_SPEC 18.7 合同验证：OpenAPI、SQL schema、JSON Schema、默认 WORKFLOW、docs/testing、docs/codex、docs/agent_work_orders 与 security regression command manifest 都是阻断项。
- v1 release 必须满足 `TECH_SPEC.md` 第 20 章 Definition of Done：API/DB/CLI/dashboard conform、Codex adapter fixture-gated、real Codex tests opt-in、raw prompt/raw Codex logs 不暴露、single dist/symphony binary builds，且 known limitations documented。

## 14. 后续版本路线

v1.1 建议方向：

```text
schema migration framework
SQLite backup/restore
crash recovery leases and reconciliation
full audit log
API/CLI audit query surface
```

v1.2 建议方向：

```text
supply-chain policy
Git provider publish / PR workflow
Tauri desktop shell planning
remote dashboard / RBAC planning
secret management
```

这些都不得进入 v1，除非重新冻结产品范围。
