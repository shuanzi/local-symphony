# MVP 路线图：M0–M8

## M0：项目骨架

目标：建立后端、前端、数据库、CLI 的基本工程结构。

交付：

```text
Go daemon skeleton
React/TypeScript dashboard skeleton
SQLite project DB
OpenAPI schema skeleton
CLI skeleton
WORKFLOW.md parser skeleton
basic health endpoint
```

验收：

```text
symphony init
symphony serve
打开 dashboard
GET /api/v1/health 正常
项目 DB 创建成功
WORKFLOW.md 能 parse / validate
```

## M1：Local Tracker MVP

目标：本地 issue tracker 可用。

交付：

```text
issues table
comments table
labels
state transitions
blockers
issue history
Board UI
Issue Detail UI
issue CLI
```

验收：

```text
可以创建 LOC-1
可以编辑 title / description / acceptance criteria
可以移动 Inbox → Ready
可以添加 blocker
被 blocker 阻塞的 issue 不进入 dispatch candidate
可以添加 comment
Board 正确展示各状态 issue
```

## M2：Workspace + Git MVP

目标：每个 issue 能创建独立 workspace 和 branch。

交付：

```text
project Git repo detection
worktree creation
branch naming
workspace reuse
git preflight
changed files detection
patch generation
Workspace/Git API
```

验收：

```text
Ready issue dispatch 前创建 worktree
branch 名称符合 symphony/LOC-1-...
workspace 位于 ~/.symphony/workspaces/...
同一 issue 第二次 run 复用 workspace
不会修改主 repo working tree
可以生成 changed-files.txt 和 changes.patch
```

## M3：Codex Agent Runner MVP

目标：能从 issue 启动 Codex run。

交付：

```text
codex app-server subprocess manager
stdio JSONL adapter
run_attempts table
run_events table
prompt builder
runtime envelope
context pack
turn timeout / cancel
basic normalized events
Run Detail UI
```

验收：

```text
手动 dispatch LOC-1
daemon 启动 codex app-server
Codex cwd 是 issue workspace
prompt 包含 issue / workspace / git / tool manifest
Run Detail 能看到 run_created / codex_started / turn_started / turn_completed
cancel run 能终止子进程
```

## M4：Tool Gateway + Handoff MVP

目标：agent 可以通过本地工具更新 issue 并交接。

交付：

```text
local IPC endpoint
run-scoped tool token
symphony tool issue get
symphony tool issue comment
symphony tool artifact attach
symphony tool handoff
handoffs table
tool_calls table
handoff continuation
```

验收：

```text
agent run 内能执行 symphony tool issue get
错误 token 无法访问
错 issue token 无法修改其他 issue
symphony tool handoff 能写 comment、attach summary、状态转 Human Review
没有 handoff 时最多追加一次 continuation
仍无 handoff 时 run 标记 completed_without_handoff
```

## M5：Review Packet MVP

目标：Human Review 有完整交付物。

交付：

```text
review packet generator
review.md
review.json
changes.patch
changed-files.txt
commands.jsonl
tool-calls.jsonl
approvals.jsonl
agent-final-message.md
Review Packet UI
Send to Rework
Mark Done
```

验收：

```text
agent handoff 后自动生成 review packet
Issue 进入 Human Review
Review UI 展示 summary、tests、risks、diff、changed files
可以 Send to Rework
可以 Mark Done
没有 review packet 不允许 Done
```

## M6：Approval + Security MVP

目标：默认安全策略可运行。

交付：

```text
balanced-secure mode
command allow/review/deny
network default deny
protected path policy
approval_requests table
Approval Inbox UI
session token
CSRF
redaction utilities
```

验收：

```text
git status / git diff 自动允许
git push 被拒绝或进入不可批准状态
npm install 进入 Approval Inbox
网络请求默认被拒绝或进入审批
agent 不能通过 tool 修改非当前 issue
.env 默认不展示原文
API 无 session token 不可访问 command endpoint
```

## M7：Observability + Diagnostics MVP

目标：能诊断失败，而不是只看到“失败”。

交付：

```text
Overview UI
Diagnostics UI
structured app log
run JSONL log
raw Codex log reference
workflow effective config
codex version check
git status check
diagnostic redacted export
```

验收：

```text
Overview 展示 running / failed / pending approvals
Diagnostics 展示 daemon、Codex、Git、DB 路径
workflow invalid 时阻止新 dispatch 并显示 last valid config
run failure 能定位到 prompt / codex / approval / tool / handoff 类别
可以导出 redacted diagnostic bundle
```

## M8：v1 Release Hardening

目标：v1 可作为本地生产 MVP 使用。

交付：

```text
end-to-end tests
security tests for v1 adopted controls
CLI help
starter WORKFLOW.md
example project
packaging scripts
release notes
known limitations
```

验收：

```text
完整主路径 e2e 通过：
  init → create issue → dispatch → Codex run → handoff → review packet → Human Review → Rework / Done

关键失败路径通过：
  invalid WORKFLOW
  tool token denied
  command denied
  approval timeout
  Codex startup failure
  missing handoff
  workspace already exists
  blocker active

v1 limitation 明确列出：
  no auto backup
  no migration/rollback
  no crash recovery
  no full audit log
  no desktop shell
```
