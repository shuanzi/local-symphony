# MVP Backlog — M0 to M8

## Status

Frozen.

## M0 — Contracts and project skeleton

Goal: establish executable contracts, backend, frontend, DB, API, and CLI skeleton.

Backend:

```text
Go module
cmd/symphony/main.go
internal/app bootstrap
basic HTTP server
health endpoint
SQLite init from db/schema/app_v1.sql and db/schema/project_v1.sql
WORKFLOW parser skeleton
api/openapi.yaml validation and generated types
store contract tests from IS-014
Codex fixture-gate fake adapter scaffold from IS-015
```

Frontend:

```text
React + TypeScript app
basic layout
API client skeleton
Overview placeholder
```

CLI:

```text
symphony init
symphony serve
symphony open
symphony status placeholder
```

Acceptance:

```text
symphony init works
symphony serve starts localhost API
GET /api/v1/health works
dashboard opens
project DB created from db/schema/project_v1.sql
app DB created from db/schema/app_v1.sql
WORKFLOW.md parsed/validated
api/openapi.yaml validates and can generate frontend types
unsupported DB version fails safely
```

## M1 — Local Tracker MVP

Backend:

```text
issues
comments
labels
relations
state history
issue CRUD API
state transition API
blocker eligibility
```

Frontend:

```text
Board
Issue Detail
create issue form
transition action
comment panel
```

CLI:

```text
issue create/list/show/update/transition/comment/blocker
```

Acceptance:

```text
Create LOC-1
Move Inbox → Ready
Add blocker
Blocked issue not dispatchable
Board shows states
```

## M2 — Workspace + Git MVP

Backend:

```text
repo detection
project registration
workspace path resolver
branch generator
worktree create/reuse
base_ref auto resolver
git preflight
changed files and patch generation
```

Frontend:

```text
workspace/git panel
workspace path
branch
changed files
```

Acceptance:

```text
Dispatch prepares worktree
Branch uses symphony/LOC-...
Workspace under ~/.symphony/workspaces
Same issue reuses workspace
Main repo not modified
```

## M3 — Codex Agent Runner MVP

Backend:

```text
fake runner E2E harness
Codex process manager scaffold
version-aware Codex protocol parser fixture gate
run_attempts
run_events
prompt builder
runtime envelope
context pack
cancel/timeout
```

Frontend:

```text
Run Detail
normalized timeline
cancel button
```

Acceptance:

```text
Manual dispatch starts fake runner by default in tests
Real Codex path is opt-in and fixture-gated
Codex cwd is issue workspace
Prompt includes issue/workspace/git/tools
Run Detail shows events
Cancel terminates subprocess and pauses dispatch without automatic redispatch
```

## M4 — Tool Gateway + Handoff MVP

Backend:

```text
local IPC server
run-scoped token
tool registry
tool call persistence
issue.get/comment/block
artifact.attach
followup.create
handoff.submit
missing handoff continuation
```

CLI:

```text
symphony tool issue get
symphony tool issue comment
symphony tool issue block
symphony tool artifact attach
symphony tool followup create
symphony tool handoff
```

Acceptance:

```text
Correct token reads current issue
Wrong token denied
Agent cannot edit unrelated issue
Handoff is persisted with canonical payload_hash
`symphony tool handoff` is command-policy allowed only with valid run token
No handoff triggers at most one continuation
```

## M5 — Review Packet MVP

Backend:

```text
review packet generator after after_run
review.md
review.json
changes.patch
changed-files.txt
commands/tool-calls/approvals exports
finalizer transition to Human Review
```

Frontend:

```text
Review Packet page
diff viewer
tests/risks/verification
Send to Rework
Mark Done
```

Acceptance:

```text
Handoff generates review packet
Issue enters Human Review after packet generated
Review UI shows diff and summary
Untracked new files appear in changes.patch and changed-files.txt
Can Send to Rework; rework packet is cumulative from base_sha
Can Mark Done only with generated packet
```

## M6 — Approval + Security MVP

Backend:

```text
browser session token
CSRF
CLI bearer token
command allow/review/deny
network default deny
protected paths
approval_requests
redaction utilities
artifact containment
```

Frontend:

```text
Approval Inbox
security indicators
approval decision UI
```

Acceptance:

```text
git status auto allowed
git push denied
npm install enters review
network default denied/reviewed
protected path access fails with protected_path_denied when terminal
.env not shown raw
unauthenticated command API rejected
```

## M7 — Observability + Diagnostics MVP

Backend:

```text
Overview state API
Diagnostics API
structured app log
run event log
raw Codex log reference
workflow effective config
redacted diagnostics export
```

Frontend:

```text
Overview
Workflow
Diagnostics
SSE event integration
```

Acceptance:

```text
Overview shows running/failed/approvals
Workflow invalid blocks dispatch
Diagnostics shows Codex/Git/DB state
Run failure classified
Redacted diagnostic bundle export works
```

## M8 — v1 release hardening

Testing:

```text
full E2E main path
invalid WORKFLOW
tool token denied
command denied
network denied
protected path denied
run cancel no redispatch
approval cancel_run no redispatch
approval timeout
missing handoff
workspace already exists
blocker active
stale run startup guard
redaction
```

Docs:

```text
README
Quickstart
security model
agent implementation guide
acceptance tests
definition of done
known limitations
CLI help
starter WORKFLOW.md
example project
```

Acceptance:

```text
init → create issue → Ready → dispatch → Codex/fake run → handoff → review packet → Human Review → Rework/Done full path passes
```

## v1.1 suggested order

```text
1. schema migration framework
2. SQLite backup/restore
3. crash recovery leases and reconciliation
4. full audit log
```

## v1.2 suggested order

```text
supply-chain policy
Git provider publish
Tauri desktop shell planning
```
