# Local Symphony App v1 Tech SPEC

**状态**：v1 技术方案合并版  
**生成日期**：2026-05-10  
**来源**：`local-symphony 2.zip` 原始文档包合并精简  
**文档权威性**：本文档是 Local Symphony App v1 的唯一技术规格文档，合并并替代原始 `api/openapi.yaml`、`db/schema/*.sql`、`docs/implementation/*`、`docs/schema/*`、`docs/config/*`、`docs/security/*`、`docs/agent/*`、`docs/backlog/*` 中的技术合同说明。产品目标、用户场景和非技术范围以 `PRD.md` 为准。

---

## 1. 技术摘要

Local Symphony App v1 是一个本地运行的单机控制面：

```text
Go daemon
+ React/TypeScript dashboard
+ SQLite local tracker
+ git worktree workspace manager
+ Codex app-server runner
+ CLI/IPC tool gateway
+ two-stage handoff submit/finalize
+ review packet generator
+ REST/SSE API
+ balanced-secure local security baseline
```

v1 的技术目标是实现一条可测试、可观察、可复核、可中断、可返工的本地 agent engineering workflow。

核心不可变约束：

```text
tracker.kind = local only
SQLite project DB is local tracker source of truth
no Linear dependency
one issue → one git worktree
handoff.submit never transitions issue state directly
review packet generation is required before Human Review
Done is operator-only
run failures pause dispatch
no automatic retry queue/timers
no automatic workspace delete/reset/clean
no automatic push/PR/merge/publish
real Codex tests are opt-in; default tests use fake runner
```

## 2. 规范性约定

本文使用 RFC 2119 风格术语：

```text
MUST      必须实现
MUST NOT  禁止实现
SHOULD    应该实现，除非有明确理由
MAY       可选实现
```

当 `PRD.md` 与本文冲突时：

- 产品意图以 PRD 为准。
- API、DB、状态机、CLI、安全、测试、发布等实现合同以本文为准。

实现可以在代码仓库中生成或维护 OpenAPI、SQL schema、CLI help、test manifests 等文件，但这些文件必须与本文合同一致。

## 3. 实现边界

### 3.1 MUST implement

```text
local SQLite tracker
project/app SQLite DB initialization and version guard
WORKFLOW.md parser and strict prompt renderer
git worktree workspace manager
single-actor orchestrator
fake runner E2E path
Codex app-server adapter with fixture gate
REST API and SSE event stream
operator CLI
tool gateway IPC + fixed registry
run-scoped tool token
handoff canonical payload hash and idempotency
review packet generator
Human Review / Rework / Done workflow
loopback dashboard API security
browser session + CSRF
CLI bearer token
command/network/protected-path policy bridge
redacted diagnostics export
M0-M8 acceptance test coverage
```

### 3.2 MUST NOT implement in v1

```text
Linear adapter, Linear config, Linear credentials, or Linear API calls
automatic retry scheduler/timers
automatic PR creation, git push, merge, publish, or agent commit
automatic workspace delete/reset/clean/rebase
dynamic tools or MCP
remote dashboard or multi-tenant RBAC
production DB migration/rollback framework
automatic SQLite backup/restore
crash recovery beyond startup stale active-run interruption
full audit log
raw prompt or raw Codex log export through v1 API
```

## 4. Architecture

### 4.1 Component overview

```text
CLI / Dashboard
      ↓ REST/SSE
HTTP API Server
      ↓ commands / queries
Application Services
      ↓
Local Tracker Store ← SQLite Project DB
      ↓
Orchestrator Actor
      ↓
Workspace Manager → Git Manager
      ↓
Prompt Builder → Prompt Snapshot
      ↓
Agent Runner → Codex Adapter / Fake Runner
      ↓
Tool Gateway ← symphony tool ...
      ↓
Handoff Store
      ↓
Review Packet Generator
      ↓
Human Review state
```

### 4.2 Process model

v1 builds a single binary:

```text
symphony
```

`symphony serve` starts the daemon, REST/SSE server, dashboard static asset server, Tool Gateway transport, orchestrator actor, and project runtime lock.

Each run launches one Codex app-server process group, with cwd set to the issue workspace. Operator cancellation, approval `cancel_run`, reconciliation, shutdown, timeout, or context cancellation MUST terminate the process group gracefully first, then kill if needed.

### 4.3 Runtime descriptor

`symphony serve` MUST write:

```text
~/.symphony/runtime/<project_id>.json
```

The runtime descriptor contains:

```text
project_id
repo_root
api_url
tool_gateway_endpoint
daemon_pid
started_at
```

It MUST NOT contain secrets, session tokens, tool tokens, or CSRF tokens. Mutable daemon endpoint metadata is discovered from this descriptor, not from app DB.

### 4.4 Single-daemon project ownership

v1 MUST allow at most one active daemon owner per project DB.

Required behavior:

```text
1. resolve project_id and project DB path
2. acquire project runtime lock before accepting API/tool requests
3. write runtime descriptor after successful lock and bind
4. fail fast if another live daemon owns the project
5. validate and remove stale descriptor/lock owner before continuing
6. remove descriptor and release lock on normal shutdown
```

The lock SHOULD be an OS file lock or equivalent single-host mechanism. PID file alone is insufficient.

## 5. Repository and package layout

Recommended implementation repository:

```text
local-symphony/
├── go.mod
├── go.sum
├── cmd/
│   └── symphony/
│       └── main.go
├── internal/
│   ├── app/
│   ├── core/
│   ├── config/
│   ├── db/
│   ├── store/
│   ├── tracker/
│   ├── orchestrator/
│   ├── workspace/
│   ├── gitx/
│   ├── agent/
│   ├── toolgateway/
│   ├── review/
│   ├── httpapi/
│   ├── cli/
│   ├── security/
│   ├── observability/
│   └── platform/
├── api/
├── db/
├── web/
├── docs/
├── examples/
├── testdata/
└── scripts/
```

Package responsibilities:

| Package | Responsibility |
|---|---|
| `cmd/symphony` | Minimal entrypoint; delegates to `internal/cli`. |
| `internal/app` | Composition root, bootstrap, daemon lifecycle. |
| `internal/core` | Pure domain types, enums, errors; stdlib only. |
| `internal/config` | WORKFLOW parser, EffectiveConfig, prompt rendering. |
| `internal/db` | SQLite open/init/schema version/transactions. |
| `internal/store` | Handwritten SQLite repositories returning core types. |
| `internal/tracker/local` | Local issue tracker business logic. |
| `internal/orchestrator` | Single authoritative actor and dispatch loop. |
| `internal/workspace` | Workspace path and lifecycle. |
| `internal/gitx` | All Git command execution. |
| `internal/agent/codex` | Codex app-server adapter. |
| `internal/agent/fake` | Deterministic fake runner for tests. |
| `internal/toolgateway` | Agent tool IPC server/client and registry. |
| `internal/review` | Review packet generation. |
| `internal/httpapi` | REST/SSE translation layer. |
| `internal/cli` | Operator CLI and tool CLI command parsing. |
| `internal/security` | Tokens, CSRF, command policy, path policy, redaction. |
| `internal/observability` | Logs, events, diagnostics, exports. |
| `internal/platform` | OS-specific paths, IPC, process groups. |

Dependency direction:

```text
cmd
 ↓
cli / app
 ↓
httpapi / orchestrator / services
 ↓
tracker / workspace / agent / review / toolgateway
 ↓
store / gitx / security / observability
 ↓
db / platform
 ↓
core
```

Forbidden dependencies:

```text
core → any internal package
store → httpapi
orchestrator → httpapi
agent/codex → tracker/local
web → SQLite/filesystem/Git/Codex
tool CLI → SQLite directly
```

Use one Go module. Use `database/sql + modernc.org/sqlite`. v1 uses handwritten store/repository methods; do not introduce sqlc in v1.

## 6. WORKFLOW.md contract

### 6.1 Format

`WORKFLOW.md` is Markdown with optional YAML front matter:

```markdown
---
tracker:
  kind: local
---
Prompt body here.
```

Rules:

```text
if front matter is absent, whole file is prompt body
front matter root MUST be a map/object
prompt body is trimmed
empty prompt body blocks dispatch
unknown keys warn
wrong type / missing required field / unsupported enum errors block dispatch
```

Supported top-level config keys:

```yaml
tracker: {}
polling: {}
workspace: {}
hooks: {}
git: {}
agent: {}
codex: {}
approvals: {}
tools: {}
security: {}
observability: {}
server: {}
ui: {}
prompt: {}
```

Config fields do **not** support Liquid interpolation. Only full-string `$VAR_NAME` environment variable expansion is allowed, for example:

```yaml
workspace:
  root: "$SYMPHONY_WORKSPACE_ROOT"
```

### 6.2 EffectiveConfig defaults

```yaml
tracker:
  kind: local
  project: default
  active_states: [Ready, Working, Rework]
  terminal_states: [Done, Cancelled, Duplicate]

polling:
  interval_ms: 30000

workspace:
  root: <global-workspace-root>/<project_id>
  cleanup:
    done_retention_days: 14
    require_snapshot_before_delete: false

hooks:
  after_create: null
  before_run: null
  after_run: null
  before_remove: null
  timeout_ms: 300000
  max_output_bytes: 65536

git:
  enabled: true
  mode: worktree
  repo_root: .
  base_ref: auto
  branch_prefix: symphony
  agent_commit: manual
  auto_push: false
  auto_rebase: false
  submodules: false
  provider:
    kind: none

agent:
  max_concurrent_agents: 3
  max_turns_per_run: 2
  max_handoff_continuations: 1
  handoff_required: true
  handoff_state: Human Review
  pause_on_missing_handoff: true

codex:
  command: codex app-server
  transport: stdio
  startup_timeout_ms: 60000
  turn_timeout_ms: 3600000
  stall_timeout_ms: 300000
  read_timeout_ms: 5000
  experimental_api: false

approvals:
  mode: balanced
  network:
    default: deny
    allowlist: []
  protected_paths:
    - ".env"
    - ".env.*"
    - "**/*.pem"
    - "**/*.key"
    - "**/*_rsa"
    - "**/*_ed25519"
    - ".ssh/**"
    - ".aws/**"
    - ".gcp/**"
    - ".azure/**"
    - ".kube/**"
    - ".npmrc"
    - ".pypirc"
    - ".netrc"

tools:
  gateway: cli
  require_handoff_tool: true
  allow_dynamic_tools: false
  allow_mcp: false
  agent_can_create_followups: true
  agent_can_set_blocked: true
  agent_can_set_terminal_state: false
  artifact_max_bytes: 10485760

security:
  mode: balanced-secure
  require_loopback_api: true
  allow_remote_api: false
  require_session_token: true
  require_csrf: true

observability:
  structured_logs: true
  log_level: info
  redact_secrets: true
  raw_codex_log_retention_days: 30

server:
  host: 127.0.0.1
  port: 0
  open_browser_on_start: false

ui:
  default_view: overview

prompt:
  max_context_bytes: 200000
  include_previous_runs: true
  previous_run_limit: 3
  include_tool_manifest: true
  save_prompt_snapshot: redacted
```

### 6.3 Hard validation constraints

```text
tracker.kind MUST equal local
agent.handoff_required MUST be true
agent.pause_on_missing_handoff MUST be true
agent.handoff_state MUST equal Human Review
agent.max_handoff_continuations MUST be 0 or 1
agent.max_turns_per_run MUST equal 1 + agent.max_handoff_continuations when explicitly set
tools.allow_dynamic_tools MUST be false
tools.allow_mcp MUST be false
tools.agent_can_set_terminal_state MUST be false
git.auto_push MUST be false
git.auto_rebase MUST be false
security.allow_remote_api MUST be false in v1
```

The loader MAY accept upstream-style `agent.max_turns` as an alias for `agent.max_turns_per_run`. If both are present, `max_turns_per_run` wins and warning is emitted. This alias does not enable upstream continuation semantics.

### 6.4 Path rules

Path fields:

```text
workspace.root
git.repo_root
```

Rules:

```text
~ expands to home
relative paths are relative to WORKFLOW.md directory
paths normalize to absolute paths
workspace.root MUST NOT equal git.repo_root
workspace.root MUST NOT be inside git.repo_root
workspace.root MUST NOT be inside .git
path fields MUST NOT execute shell commands
path fields MUST NOT be URI
path fields MUST NOT contain Liquid interpolation
```

### 6.5 Reload semantics

Reload sources:

```text
daemon startup
file watcher
manual API/CLI reload
before-dispatch revalidate
```

Behavior:

```text
running run attempts keep captured workflow_snapshot_id and EffectiveConfig
new dispatch attempts use latest valid workflow snapshot
invalid reload creates invalid workflow_snapshot row for diagnostics
invalid reload does not replace effective config
if no valid config exists, dispatch is blocked while UI/diagnostics remain available
dry-run validation never replaces effective config
```

### 6.6 Prompt rendering

Prompt body uses strict Liquid-style syntax. Root keys:

```text
issue
attempt
project
workspace
git
run
tools
previous_runs
```

Supported filters:

```text
default
join
json
bullet_list
indent
truncate
slug
short_hash
markdown_quote
```

Unknown variables and filters MUST fail rendering.

Final prompt composition:

```text
Runtime Envelope
+ Tool Manifest
+ rendered WORKFLOW.md prompt body
+ Context Pack
+ Handoff Contract
```

Runtime Envelope and Tool Manifest are generated by Symphony and cannot be overridden by repo prompt.

If main turn completes without handoff and continuation is still available, v1 sends one dedicated handoff continuation prompt in the same session/thread. It does not resend the full task prompt.

Prompt snapshot files:

```text
prompt/context.json
prompt/rendered_prompt.redacted.md
prompt/prompt_meta.json
prompt/tool_manifest.md
```

## 7. Data model and persistence

### 7.1 Database layout

v1 uses two SQLite DBs:

```text
Global App DB: ~/.symphony/app.db
Project DB:    <repo>/.symphony/symphony.db
```

Global App DB stores registered projects, local sessions, and global settings. Project DB stores tracker, runs, events, approvals, tool calls, handoffs, artifacts, review packets, workflow snapshots, and prompt snapshots.

### 7.2 SQLite requirements

Use `database/sql + modernc.org/sqlite`.

Every connection MUST execute or verify:

```sql
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
```

All timestamps are UTC RFC3339 `TEXT`. Booleans are `INTEGER CHECK (field IN (0,1))`. JSON is stored as `TEXT` and validated in Go.

### 7.3 Schema version handling

Each DB MUST have exactly one `schema_version` row with `version = 1`.

| Condition | Behavior |
|---|---|
| DB missing | Initialize schema v1. |
| `schema_version` missing | Fail with `unsupported_db_version`; do not mutate. |
| `schema_version.version = 1` | Continue. |
| `schema_version.version > 1` | Fail with `unsupported_db_version`; do not mutate. |
| `schema_version.version < 1` | Fail with `unsupported_db_version`; no migration in v1. |

v1 has no production migration or rollback flow.

### 7.4 ID generation

IDs are opaque strings with stable prefixes:

| Entity | Prefix |
|---|---|
| Project | `proj_` |
| Issue | `iss_` |
| Run attempt | `run_` |
| Run event | `evt_` |
| Workspace | `ws_` |
| Approval | `appr_` |
| Tool token row | `tok_` |
| Tool call | `tc_` |
| Handoff | `hand_` |
| Artifact | `art_` |
| Review packet | `rp_` |
| Workflow snapshot | `wf_` |
| Prompt snapshot | `ps_` |
| Comment | `cmt_` |
| Relation | `rel_` |
| State history | `hist_` |
| Session | `sess_` |

Implementation MAY use ULID, UUIDv7, or another collision-resistant sortable ID. IDs MUST NOT encode secrets, absolute paths, or raw prompt content.

### 7.5 App DB contract

Tables:

```text
schema_version
registered_projects
app_settings
local_sessions
```

`registered_projects` fields:

```text
id
name
repo_root UNIQUE
project_db_path
workflow_path
issue_prefix DEFAULT LOC
created_at
updated_at
last_opened_at
```

`local_sessions` stores only token hashes:

```text
id
kind ∈ browser|cli|desktop
token_hash UNIQUE
created_at
last_seen_at
expires_at
revoked_at
```

### 7.6 Project DB contract

Tables:

```text
schema_version
project_info
project_settings
counters
workflow_snapshots
issues
issue_labels
workspaces
run_attempts
issue_comments
issue_relations
issue_state_history
run_events
approval_requests
run_tool_tokens
tool_calls
handoffs
artifacts
prompt_snapshots
review_packets
```

Key table contracts:

#### issues

```text
id PRIMARY KEY
identifier UNIQUE
sequence_no UNIQUE
title
description
acceptance_criteria_json
state ∈ Inbox|Ready|Working|Rework|Blocked|Human Review|Done|Cancelled|Duplicate
priority INTEGER 1..4, where 1 is highest
dispatch_paused 0/1
dispatch_pause_reason
dispatch_paused_at
created_at
updated_at
completed_at
archived_at
```

`dispatch_paused` prevents repeated dispatch after failure, missing handoff, cancellation, block, or startup interruption.

#### issue_relations

Relation directions are fixed:

| relation_type | source_issue_id | target_issue_id | Agent permission |
|---|---|---|---|
| `blocks` | blocker issue | blocked issue | not via Tool Gateway |
| `duplicates` | duplicate issue | canonical issue | not via Tool Gateway |
| `followup_of` | follow-up issue | original/current issue | only through `followup.create` |

An issue is blocked while any direct blocker is not terminal. Terminal blocker states:

```text
Done
Cancelled
Duplicate
```

#### workspaces

```text
id
issue_id UNIQUE
path UNIQUE
branch_name
base_ref              # resolved ref
base_ref_config       # configured value, e.g. auto
base_sha
status ∈ planned|creating|ready|in_use|error|cleanup_pending|removed
created_at
updated_at
last_used_at
removed_at
```

v1 never automatically sets workspace to removed via cleanup. Cleanup statuses are future-compatible.

#### run_attempts

Statuses:

```text
pending
preparing_workspace
rendering_prompt
starting_agent
running
completed
completed_without_handoff
failed
cancelled
```

Active statuses:

```text
pending
preparing_workspace
rendering_prompt
starting_agent
running
```

Terminal statuses:

```text
completed
completed_without_handoff
failed
cancelled
```

Other key fields:

```text
attempt_no UNIQUE per issue
dispatch_reason ∈ manual|scheduler|retry|rework
agent_runtime = codex
codex_command
codex_version
process_pid
process_group_id
thread_id
turn_id
session_id
workflow_snapshot_id
failure_code
failure_message
started_at
ended_at
created_at
updated_at
```

#### run_events

`run_events.seq INTEGER PRIMARY KEY AUTOINCREMENT` is the SSE replay ID. `id` is a business event ID. UI timelines MUST render from durable normalized `run_events`, not raw Codex logs.

#### approval_requests

```text
id
issue_id
run_id
type ∈ command|file_change|network
status ∈ pending|auto_approved|auto_denied|approved|denied|cancelled|expired
risk_level ∈ low|medium|high|critical
command/cwd OR file_path/file_action OR network_host/protocol/port
reason
policy_match
decision
decision_reason
decided_by
requested_at
timeout_ms DEFAULT 1800000
expires_at
resolved_at
```

#### run_tool_tokens

```text
id
run_id
issue_id
token_hash UNIQUE
scope_json
created_at
expires_at
last_used_at
revoked_at
```

Only hashes are stored.

#### tool_calls

All attributable tool calls are persisted, success or failure:

```text
id
issue_id
run_id
tool_name
status ∈ started|succeeded|failed
input_hash
input_json_redacted
output_hash
output_json_redacted
error_code
error_message
started_at
ended_at
```

#### handoffs

```text
id
issue_id
run_id UNIQUE
payload_hash
payload_json_redacted
summary
changed_files_json
tests_json
risks_json
verification_json
followups_json
target_state CHECK target_state = Human Review
submitted_at
```

The first successful handoff for a run wins. Handoff idempotency source is `handoffs.payload_hash`, not `tool_calls.input_hash`.

#### artifacts

```text
id
issue_id
run_id
kind ∈ test_output|patch|changed_files|untracked_files|prompt_snapshot|codex_log|review_packet|agent_file|diagnostic|other
path             # project-local relative only
mime_type
size_bytes
sha256
redacted 0/1
created_at
```

Artifact paths MUST resolve under `.symphony/artifacts` or `.symphony/exports`.

#### prompt_snapshots

```text
id
run_id
workflow_snapshot_id
runtime_envelope_version
tool_manifest_version
context_hash
rendered_prompt_hash
context_json_path
redacted_prompt_path
prompt_meta_json_path
tool_manifest_path
created_at
```

Prompt snapshot files MUST be durable before inserting `prompt_snapshots`.

#### review_packets

```text
id
issue_id
run_id
status ∈ generated|partial|failed
root_path
review_md_path
review_json_path
patch_path
changed_files_path
untracked_files_path
handoff_id
prompt_snapshot_id
failure_code
failure_message
created_at
```

A `generated` row MUST NOT point to missing critical files.

### 7.7 Transaction rules

Create issue transaction:

```text
increment counters.issue_sequence
insert issue
insert state_history
insert issue.created event
```

Transition issue transaction:

```text
validate transition
if transition leaves active states and an active run exists, enqueue reconciliation cancel
update issue state
insert state_history
insert issue.transitioned event
```

Dispatch claim transaction:

```text
validate active state
validate not paused
validate no active blockers
validate no active run
allocate attempt_no
create run_attempt pending
transition issue to Working if needed
insert scheduler.dispatch_claimed event
commit before workspace/token/process/prompt creation
```

Handoff tool transaction:

```text
validate run-scoped token
validate running run
insert or idempotently return handoff
insert issue comment
insert tool_call
insert handoff.submitted event
```

Review finalizer transaction after files are written:

```text
insert artifact rows
insert review_packet status=generated
run.status = completed
issue.state = Human Review
clear dispatch_paused
insert state_history
insert review.packet_generated event
```

Missing handoff after allowed continuation:

```text
run.status = completed_without_handoff
issue.dispatch_paused = true
dispatch_pause_reason = missing_handoff
system comment
handoff.missing event
```

### 7.8 NormalizedIssue DTO

`NormalizedIssue` is the stable shape used by orchestrator, prompt rendering, API, dashboard, and review metadata.

Required fields:

```text
id
identifier
sequence_no
title
description
acceptance_criteria
priority
state
url
labels
blocked_by
blocks
dispatch_paused
dispatch_pause_reason
dispatch_paused_at
branch_name
workspace_path
base_ref
base_ref_config
base_sha
workspace
git
latest_run / active_run_id
latest_review_packet / latest_review_packet_id
created_at
updated_at
completed_at
archived_at
```

Rules:

```text
labels lowercased and sorted
url is null or local dashboard URL
workspace null until workspace row exists
latest_run null until run exists
latest_review_packet null until review packet exists
branch_name/workspace_path/base_ref/base_ref_config/base_sha are top-level compatibility aliases
git mirrors workspace git fields for prompt ergonomics
base_ref is resolved Git ref
base_ref_config preserves configured value such as auto
created_at/updated_at are RFC3339 UTC
```

Required prompt aliases:

```liquid
{{ issue.branch_name }}
{{ git.branch_name }}
```

### 7.9 Eligibility query

Implementation MUST enforce equivalent eligibility:

```sql
SELECT i.*
FROM issues i
WHERE i.state IN ('Ready', 'Rework')
  AND i.dispatch_paused = 0
  AND NOT EXISTS (
    SELECT 1
    FROM issue_relations r
    JOIN issues blocker ON blocker.id = r.source_issue_id
    WHERE r.target_issue_id = i.id
      AND r.relation_type = 'blocks'
      AND blocker.state NOT IN ('Done', 'Cancelled', 'Duplicate')
  )
  AND NOT EXISTS (
    SELECT 1
    FROM run_attempts r
    WHERE r.issue_id = i.id
      AND r.status IN ('pending','preparing_workspace','rendering_prompt','starting_agent','running')
  )
ORDER BY i.priority ASC, i.created_at ASC, i.identifier ASC;
```

Note: `Working` is active-dispatch-eligible for reconciliation, but not a normal scheduler candidate.

### 7.10 File and DB atomicity

For artifact-producing flows:

```text
1. write files into temporary directory under .symphony/tmp
2. fsync/close when supported
3. rename temporary directory/files into final path under .symphony/artifacts or .symphony/exports
4. insert artifact/review/prompt DB rows in one DB transaction
5. if DB transaction fails, leave files as orphan candidates and emit diagnostic on next scan
6. if file write fails, do not insert success rows
```

## 8. Issue state machine and run lifecycle

### 8.1 Issue states

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

Terminal states:

```text
Done
Cancelled
Duplicate
```

`Human Review` is not terminal.

### 8.2 Allowed transitions

| From | To | Actor | Guard / side effect |
|---|---|---|---|
| `Inbox` | `Ready` | operator | Required issue fields valid. |
| `Ready` | `Working` | orchestrator | Dispatch claim transaction succeeds. |
| `Rework` | `Working` | orchestrator | Dispatch claim transaction succeeds. |
| `Working` | `Human Review` | run finalizer | Handoff exists and review packet status is `generated`. |
| `Human Review` | `Rework` | operator | Reviewer supplies reason/feedback comment. |
| `Human Review` | `Done` | operator | Latest review packet status `generated`; no active run. |
| any non-terminal | `Blocked` | operator or agent tool | Active run reconciliation cancels active run. Agent tool also pauses dispatch. |
| any non-terminal | `Cancelled` | operator | Active run reconciliation cancels active run. |
| any non-terminal | `Duplicate` | operator | Active run reconciliation cancels active run. |
| `Blocked` | `Ready` | operator | Block resolved; blockers inactive or removed. |
| `Done`/`Cancelled`/`Duplicate` | non-terminal | operator | Explicit reopen only; does not reuse old active runs. |

### 8.3 Orchestrator actor

Use one authoritative actor:

```text
one goroutine
one command queue
one in-memory running map
durable run_attempts and run_events
```

Commands:

```text
Tick
DispatchIssue
CancelRun
ApprovalResolved
AgentRunCompleted
WorkflowReloaded
Shutdown
```

Only the orchestrator actor creates run attempts and decides dispatch. Worker goroutines report outcomes to the actor; they do not directly mutate scheduler terminal state.

### 8.4 Run statuses

```text
pending
preparing_workspace
rendering_prompt
starting_agent
running
completed
completed_without_handoff
failed
cancelled
```

Run flow:

```text
pending
  ↓
preparing_workspace
  ↓
rendering_prompt
  ↓
starting_agent
  ↓
running
  ├── completed
  ├── completed_without_handoff
  ├── failed
  └── cancelled
```

Upstream-to-local outcome mapping:

| Concept | Local status | `failure_code` / reason |
|---|---|---|
| Succeeded with handoff | `completed` | null |
| Succeeded without handoff | `completed_without_handoff` | `missing_handoff` |
| Failed | `failed` | canonical failure code |
| Timed out | `failed` | `turn_timeout` |
| Stalled | `failed` | `stall_timeout` |
| Canceled by reconciliation | `cancelled` | `issue_state_changed` or `canceled_by_reconciliation` |
| Operator cancel | `cancelled` | `operator_cancelled` |
| Approval `cancel_run` | `cancelled` | `operator_cancelled` |
| Agent blocked current issue | `cancelled` | `agent_blocked` |

### 8.5 Worker lifecycle

```text
1. status → preparing_workspace
2. WorkspaceManager.Prepare(issue)
3. status → rendering_prompt
4. PromptBuilder.Build(run)
5. Create run-scoped tool token
6. status → starting_agent
7. CodexRunner.Start()
8. status → running
9. Wait for turn complete/fail/cancel
10. If missing handoff, run at most one continuation
11. Run hooks.after_run in finally path if workspace exists
12. If handoff exists and no higher-precedence outcome won, generate review packet
13. Return outcome to actor
```

### 8.6 Tick loop

Each tick:

```text
1. Reconcile active runs against current issue state and process liveness.
2. If workflow invalid and no last valid config, skip dispatch.
3. Before-dispatch revalidate WORKFLOW.md.
4. Compute available concurrency slots.
5. Query eligible issues.
6. Sort by priority ASC, created_at ASC, identifier ASC.
7. Claim issues until slots exhausted.
8. Launch run workers.
9. Emit scheduler events.
```

### 8.7 Dispatch eligibility

Normal scheduler candidates:

```text
Ready
Rework
```

`Working` is active-dispatch-eligible for reconciliation only. A `Working` issue with no active run MUST NOT be redispatched automatically unless an explicit recovery path records `dispatch_reason=retry` or operator manually dispatches after clearing pause.

No automatic retry queue/timers exist in v1. `dispatch_reason=retry` is reserved for operator-initiated redispatch of a previously failed/paused issue.

### 8.8 Active run reconciliation

Active statuses:

```text
pending
preparing_workspace
rendering_prompt
starting_agent
running
```

Active-dispatch-eligible issue states for reconciliation:

```text
Ready
Working
Rework
```

Reconciliation triggers:

```text
orchestrator tick
issue transition command
operator run cancel
agent issue.block tool
startup stale-run guard
```

If an issue with an active run leaves `Ready`/`Working`/`Rework`:

```text
1. send CancelRun to orchestrator actor
2. terminate Codex process group if it exists
3. set run_attempt.status = cancelled
4. set failure_code = issue_state_changed unless a more specific code applies
5. set ended_at
6. emit run.cancelled and scheduler.reconciled events
7. retain workspace without reset/clean/delete
```

Specific codes:

| Trigger | Local status | failure_code |
|---|---|---|
| Operator cancel | `cancelled` | `operator_cancelled` |
| Approval `cancel_run` | `cancelled` | `operator_cancelled` |
| Agent `issue.block` | `cancelled` | `agent_blocked` |
| Operator moves issue inactive/terminal | `cancelled` | `issue_state_changed` |
| Reconciliation finds active run for terminal issue | `cancelled` | `canceled_by_reconciliation` |
| Startup finds active DB rows without process ownership | `failed` | `daemon_restarted_run_interrupted` |

### 8.9 Cancellation behavior

Operator cancellation applies to:

```text
POST /api/v1/runs/{run_id}/cancel
symphony run cancel run_...
approval decision = cancel_run
```

Required side effects:

```text
run_attempt.status = cancelled
run_attempt.failure_code = operator_cancelled
run_attempt.failure_message = reason
run_attempt.ended_at = now
issues.dispatch_paused = true
issues.dispatch_pause_reason = operator_cancelled
issues.dispatch_paused_at = now
issue.state remains unchanged unless separate transition occurs
revoke run-scoped tool tokens
emit run.cancelled
emit scheduler.paused
insert system comment
```

Cancellation MUST NOT automatically redispatch on next tick.

### 8.10 Failure behavior

On run failure:

```text
run_attempt.status = failed
run_attempt.failure_code = <code>
run_attempt.failure_message = <message>
issues.dispatch_paused = true
issues.dispatch_pause_reason = <code>
run_event = run.failed
system comment with failure summary
```

Canonical `FailureCode`:

```text
workflow_invalid
workflow_validation_failed
prompt_render_failed
workspace_prepare_failed
workspace_conflict
after_create_failed
before_run_failed
codex_startup_failed
unsupported_codex_version
codex_protocol_error
turn_timeout
stall_timeout
approval_timeout
command_denied
network_denied
protected_path_denied
tool_gateway_failed
missing_handoff
review_packet_failed
operator_cancelled
agent_blocked
issue_state_changed
canceled_by_reconciliation
daemon_restarted_run_interrupted
```

### 8.11 Missing handoff

```text
main turn completes
  ↓
no handoff
  ↓
if continuation unused: send one handoff continuation
  ↓
still no handoff:
  run.status = completed_without_handoff
  run.failure_code = missing_handoff
  issue.dispatch_paused = true
  dispatch_pause_reason = missing_handoff
  system comment
  handoff.missing event
```

### 8.12 after_run hook guarantee

If a workspace was prepared, worker MUST attempt `hooks.after_run` in a finally path for all terminal worker outcomes before any successful handoff review packet is generated.

Covers:

```text
completed
completed_without_handoff
failed
cancelled
timeout-derived failure
review-packet-failure path
```

`after_run` failure is recorded as hook events and diagnostics. It does not hide the original run outcome and does not automatically move the issue to Human Review.

Events:

```text
hook.after_run.started
hook.after_run.completed
hook.after_run.failed
hook.after_run.timeout
```

### 8.13 Handoff finalizer

If handoff exists and no higher-precedence cancellation/failure has won:

```text
1. Confirm after_run attempted if workspace exists.
2. Generate review packet from current workspace state.
3. Insert review_packet.status = generated.
4. run.status = completed.
5. issue.state → Human Review.
6. issue.dispatch_paused = false.
7. Insert issue_state_history.
8. Emit review.packet_generated.
```

If review packet fails:

```text
run.status = failed
issue remains not Human Review
issue.dispatch_paused = true
failure_code = review_packet_failed
```

### 8.14 Run outcome precedence

| Priority | Outcome | Final status/code |
|---:|---|---|
| 1 | Operator cancel or approval `cancel_run` before finalizer commit | `cancelled/operator_cancelled` |
| 2 | Issue leaves active-dispatch-eligible state before finalizer commit | `cancelled/issue_state_changed` or `agent_blocked` |
| 3 | Startup stale active run guard | `failed/daemon_restarted_run_interrupted` |
| 4 | Codex/runner/protocol/workspace/prompt failure | `failed/<canonical code>` |
| 5 | Missing handoff after continuation | `completed_without_handoff/missing_handoff` |
| 6 | Handoff exists but review packet fails | `failed/review_packet_failed` |
| 7 | Handoff exists and review packet generated | `completed/null` |

Once finalizer transaction commits `issue.state=Human Review` and `run.status=completed`, later operator cancellation must be rejected as not active.

### 8.15 Startup stale-run guard

On startup, before dispatch, scan active `run_attempts.status` rows. For rows owned by previous daemon/process:

```text
status = failed
failure_code = daemon_restarted_run_interrupted
ended_at = now
issues.dispatch_paused = true
issues.dispatch_pause_reason = daemon_restarted_run_interrupted
issues.dispatch_paused_at = now
emit system.interrupted or equivalent run event
```

v1 does not implement crash recovery.

## 9. Workspace and Git

### 9.1 Workspace path

Default:

```text
~/.symphony/workspaces/<project_id>/<issue_identifier>/
```

Rules:

```text
issue_identifier is sanitized
workspace path MUST be under workspace.root
workspace.root MUST NOT equal repo root
workspace.root MUST NOT be inside repo root
workspace.root MUST NOT be inside .git
existing path MUST belong to same issue workspace
```

Sanitization:

```text
allow A-Z a-z 0-9 . _ -
replace other characters with -
collapse repeated -
trim leading/trailing - when possible
fallback to issue id when result is empty
max length 80
```

### 9.2 Base ref resolver

Default:

```yaml
git:
  base_ref: auto
```

Auto resolution order:

```text
origin/main
origin/master
main
master
HEAD
```

Explicit base ref MUST resolve successfully or workspace preparation fails.

### 9.3 Branch name

Format:

```text
symphony/<issue_identifier>-<title_slug>-<short_hash>
```

Example:

```text
symphony/LOC-1-add-local-tracker-a1b2c3
```

Validation:

```text
max length 96
no spaces
not ending with /
no ..
no @{
passes git check-ref-format
```

### 9.4 WorkspaceManager.Prepare

For new workspace:

```text
1. Resolve repo root.
2. Resolve base_ref_config, resolved base_ref, base_sha.
3. Generate branch name.
4. Create worktree with new branch.
5. Insert/update workspace row.
6. Run after_create hook if configured.
7. Run before_run hook if configured.
```

For reused workspace:

```text
1. Check workspace path exists.
2. Check path belongs to same issue.
3. Check branch matches DB.
4. Preserve base_ref_config, resolved base_ref, base_sha.
5. Do not reset.
6. Do not clean.
7. Do not rebase.
8. Run before_run hook if configured.
```

`before_run` runs before every run attempt, including the first run immediately after `after_create`.

### 9.5 Hooks

Hook environment:

```text
cwd = workspace_path
minimal env + safe project metadata
timeout = hooks.timeout_ms
stdout/stderr truncated to hooks.max_output_bytes
```

Failure mapping:

| Failure | failure_code |
|---|---|
| `after_create` failed | `after_create_failed` |
| `before_run` failed | `before_run_failed` |
| workspace ownership/path/branch conflict | `workspace_conflict` |
| general workspace failure before hooks | `workspace_prepare_failed` |
| `after_run` failed | event/diagnostic only, primary outcome unchanged |

`before_remove` is unused in v1 because destructive cleanup is deferred.

### 9.6 Git execution

All Git commands MUST go through `internal/gitx` with controls:

```text
timeout
cwd
stdout/stderr capture
redaction
path validation
structured errors
```

Forbidden outside `internal/gitx`:

```go
exec.Command("git", ...)
```

### 9.7 Diff and patch generation

Review packet generation uses:

```text
git status --porcelain=v1
git diff --binary <base_sha> -- .
git diff --name-only <base_sha> -- .
git ls-files --others --exclude-standard
```

Untracked files MUST be included:

```text
1. collect untracked files
2. validate under workspace and not protected
3. add each path to changed-files.txt
4. record path, size, sha256, patch_included=true in untracked-files.json
5. append binary-safe new-file patch for each untracked file to changes.patch
6. do not stage, commit, reset, clean, or mutate index
```

Patch paths MUST be normalized to workspace-relative `a/<path>` / `b/<path>` entries.

## 10. Codex adapter and fake runner

### 10.1 Process launch

Command:

```text
codex app-server
```

Transport:

```text
stdio
```

cwd:

```text
issue workspace path
```

Injected environment:

```text
SYMPHONY_TOOL_ENDPOINT
SYMPHONY_TOOL_TOKEN
SYMPHONY_PROJECT_ID
SYMPHONY_ISSUE_ID
SYMPHONY_ISSUE_IDENTIFIER
SYMPHONY_RUN_ID
SYMPHONY_WORKSPACE_PATH
```

Minimal host environment SHOULD be used. Stderr is diagnostic only unless the selected Codex protocol fixture documents otherwise.

### 10.2 Fixture-gated support

Implementation MUST NOT infer support for arbitrary Codex app-server versions at runtime.

A Codex version is supported only when repo contains committed fixtures:

```text
internal/agent/codex/testdata/schema/<codex-version>/
internal/agent/codex/testdata/ts/<codex-version>/
internal/agent/codex/testdata/transcripts/<codex-version>/
```

Unsupported installed versions fail before dispatch:

```text
run_attempts.status = failed
failure_code = unsupported_codex_version
issues.dispatch_paused = true
```

Fixture generation commands:

```bash
codex app-server generate-json-schema --out internal/agent/codex/testdata/schema/<codex-version>/
codex app-server generate-ts --out internal/agent/codex/testdata/ts/<codex-version>/
```

Representative transcripts:

```text
initialize_success.jsonl
turn_success_with_handoff.jsonl
approval_command_pending.jsonl
approval_file_change_pending.jsonl
approval_network_pending.jsonl
turn_failed.jsonl
malformed_event.jsonl
```

Fixtures MUST NOT contain secrets, absolute user paths, raw private prompts, or real access tokens.

### 10.3 Runner interface

The orchestrator sees a minimal runner interface:

```go
type Runner interface {
    Run(ctx context.Context, req RunRequest) (*RunResult, error)
    Cancel(ctx context.Context, runID core.RunID) error
}
```

Codex protocol internals MUST remain inside `internal/agent/codex`.

### 10.4 Minimum logical flow

```text
1. launch codex app-server over stdio
2. complete initialize handshake within codex.startup_timeout_ms
3. start/create thread/session
4. start main turn with rendered prompt
5. read notifications/requests until terminal result/cancel/timeout/protocol error
6. bridge command/file/network approval requests
7. write approval decision back to Codex
8. detect turn completed/failed/stalled/timed out
9. if completed without handoff and continuation unused, send one handoff continuation in same thread/session
10. cancel/interrupt on operator cancel, approval cancel_run, reconciliation, shutdown, timeout, context cancellation
11. terminate process group if graceful shutdown fails
```

### 10.5 Event normalization

Required normalized events:

```text
agent.process_started
agent.handshake_completed
agent.thread_started
agent.turn_started
agent.turn_progress
approval.requested
approval.resolved
agent.turn_completed
agent.turn_failed
agent.protocol_error
agent.process_exited
```

Examples:

| Codex-side event | Symphony event |
|---|---|
| thread/session created | `agent.thread_started` |
| turn started | `agent.turn_started` |
| command approval requested | `approval.requested` |
| file change requested | `approval.requested` |
| network approval requested | `approval.requested` |
| turn completed | `agent.turn_completed` |
| turn failed | `agent.turn_failed` |
| tool call observed | `tool.called` / `tool.failed` |

Raw payloads are not the UI timeline. Large/raw payloads use redacted or raw-ref artifacts per security model.

### 10.6 Approval bridge

Flow:

```text
1. Codex sends approval request.
2. Adapter normalizes request.
3. Security policy evaluates auto approve / auto deny / pending.
4. Store approval_requests row.
5. Emit run_event.
6. If pending, wait for UI/CLI decision, timeout, or cancel.
7. Send mapped decision to Codex.
```

Decision mapping:

| Symphony decision | Codex writeback semantics |
|---|---|
| `approve_once` | approve only current request |
| `approve_for_run` | approve current run; emulate locally if Codex lacks run scope |
| `approve_for_session` | Codex session-level approval if supported; otherwise emulate for process session |
| `deny` | decline request |
| `cancel_run` | interrupt/cancel run and apply `operator_cancelled` side effects |

### 10.7 Timeout mapping

| Condition | Failure code |
|---|---|
| app-server binary missing or launch failed | `codex_startup_failed` |
| startup handshake timeout | `codex_startup_failed` |
| schema/framing mismatch | `codex_protocol_error` |
| unsupported installed fixture | `unsupported_codex_version` |
| whole turn timeout | `turn_timeout` |
| no protocol progress beyond stall timeout | `stall_timeout` |
| approval expired while run waits | `approval_timeout` |

All terminal timeout/failure cases pause issue dispatch.

### 10.8 Fake runner

Default CI MUST use fake runner and fixture replay tests. Real Codex tests are opt-in only:

```bash
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex/...
```

Required fake scenarios:

```text
success_with_handoff
missing_handoff_then_handoff
missing_handoff_twice
approval_requested
command_denied
network_denied
protected_path_denied
operator_cancel_no_redispatch
approval_cancel_run_no_redispatch
codex_startup_failed
turn_timeout
stall_timeout
malformed_event
unsupported_codex_version
startup_handshake_timeout
review_packet_failure
active_run_reconciliation_cancel
```

## 11. Tool Gateway and CLI

### 11.1 CLI command groups

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

Normal CLI commands are operator commands and use `/api/v1`. Agent tool commands use Tool Gateway IPC.

Global flags:

```text
--project <path>
--api-url <url>
--json
--quiet
--no-color
--timeout <duration>
```

Project resolution:

```text
--project
current directory Git root + .symphony
last opened project
error
```

Daemon resolution:

```text
--api-url
runtime descriptor
error
```

Exit codes:

| Code | Meaning |
|---:|---|
| 0 | success |
| 1 | generic error |
| 2 | CLI argument error |
| 3 | daemon/gateway unavailable |
| 4 | auth failure |
| 5 | permission/policy denial |
| 6 | resource not found |
| 7 | state conflict |
| 8 | timeout |
| 9 | workflow/config error |

`symphony tool` always outputs JSON only. Diagnostics go to stderr.

### 11.2 Operator CLI

```bash
symphony init [--name <name>] [--issue-prefix LOC] [--workflow-template default]
symphony serve [--project <path>] [--host 127.0.0.1] [--port 0] [--open] [--no-open]
symphony open [--project <path>]
```

Issue commands:

```bash
symphony issue create ...
symphony issue list ...
symphony issue show LOC-1
symphony issue update LOC-1 ...
symphony issue transition LOC-1 Ready
symphony issue comment LOC-1 ...
symphony issue blocker add LOC-2 --blocked-by LOC-1
symphony issue blocker remove LOC-2 --blocked-by LOC-1
symphony issue dispatch LOC-1
symphony issue dispatch-pause LOC-1 --reason "..."
symphony issue dispatch-resume LOC-1 --reason "..."
```

Run commands:

```bash
symphony run LOC-1
symphony run list
symphony run show run_...
symphony run events run_... --follow
symphony run cancel run_... --reason "..."
```

`run LOC-1` is an alias for issue dispatch.

Approval commands:

```bash
symphony approval list
symphony approval decide appr_... --approve-once
symphony approval decide appr_... --deny --reason "..."
symphony approval decide appr_... --cancel-run --reason "..."
```

Review commands:

```bash
symphony review LOC-1
symphony review send-to-rework LOC-1 --reason "..."
symphony review mark-done LOC-1 --reason "..."
symphony review path LOC-1
```

Workflow and diagnostics:

```bash
symphony workflow validate
symphony workflow reload
symphony workflow show
symphony diagnostics
symphony diagnostics export
```

Do not implement publish/PR/backup/migrate/audit/workspace-delete/secret CLI commands in v1.

### 11.3 Tool Gateway transport

Agent flow:

```text
Codex shell command
  ↓
symphony tool ...
  ↓
Tool Gateway client
  ↓
local IPC
  ↓
daemon Tool Gateway server
  ↓
scope validation
  ↓
tool registry
```

Transports:

```text
unix://<path>
npipe://<name>
http://127.0.0.1:<port>
```

`SYMPHONY_TOOL_ENDPOINT` is the transport base endpoint. For HTTP it is the base origin. Request path is appended by client.

Protocol:

```http
POST {SYMPHONY_TOOL_ENDPOINT}/tool/v1/call
Authorization: Bearer <SYMPHONY_TOOL_TOKEN>
Content-Type: application/json
```

For Unix socket and Windows named pipe transports, request path is still `/tool/v1/call`.

Injected environment:

```text
SYMPHONY_TOOL_ENDPOINT
SYMPHONY_TOOL_TOKEN
SYMPHONY_PROJECT_ID
SYMPHONY_ISSUE_ID
SYMPHONY_ISSUE_IDENTIFIER
SYMPHONY_RUN_ID
SYMPHONY_WORKSPACE_PATH
```

### 11.4 Tool registry

Fixed tools:

```text
issue.get
issue.comment
issue.block
artifact.attach
followup.create
handoff.submit
```

No tool provides issue delete, Done, arbitrary state, project settings, workspace delete, git push, PR, secret read, or remote publish.

Every tool call validates:

```text
token hash
not expired
not revoked
run.status = running
issue_id and run_id scope
cwd under workspace
allowed tool
daemon-side path containment
input schema and unknown-field rejection
```

### 11.5 Tool schemas

`issue.get` input:

```json
{}
```

Returns current issue as `NormalizedIssue`. Agent cannot request another issue.

`issue.comment` input:

```json
{
  "body": "What changed or what was discovered."
}
```

Creates agent-authored comment on current issue associated with current run. Empty comments rejected.

`issue.block` input:

```json
{
  "reason": "Why the current issue cannot proceed.",
  "details": "Optional supporting detail."
}
```

Semantics:

```text
set current issue state to Blocked if allowed
add system/agent-visible comment
set dispatch_paused=true reason=agent_blocked
enqueue active run reconciliation
final run status cancelled with failure_code=agent_blocked
emit issue.blocked, run.cancelled, tool.call events
```

`issue.block` MUST NOT create blocker relations.

`artifact.attach` input:

```json
{
  "path": "relative/path/from/workspace.log",
  "kind": "test_output",
  "description": "Optional short description."
}
```

Rules:

```text
path resolves under workspace
absolute paths rejected
path traversal rejected
protected paths rejected
size <= tools.artifact_max_bytes
artifact row path is project-local relative under .symphony/artifacts
```

`followup.create` input:

```json
{
  "title": "Follow-up work title",
  "description": "Optional description",
  "acceptance_criteria": ["Optional criterion"],
  "labels": ["optional-label"],
  "priority": 3
}
```

Semantics:

```text
creates new issue in Inbox
created_by_type = agent
created_by_run_id = current run
creates relation: new_issue followup_of current_issue
agent cannot set follow-up to Ready/Working/Human Review/Done/Cancelled/Duplicate/Blocked
agent cannot create blocks or duplicates relations
```

`handoff.submit` input:

```json
{
  "summary": "What was completed.",
  "changed_files": ["workspace-relative/path.ts"],
  "tests": ["Test command and result"],
  "risks": ["Known risk or empty"],
  "verification": ["Manual verification step"],
  "followups": ["Optional follow-up summary"],
  "target_state": "Human Review"
}
```

`target_state` is optional; if present it MUST equal `Human Review`. Successful response indicates receipt only:

```json
{
  "success": true,
  "tool": "handoff",
  "issue_identifier": "LOC-123",
  "handoff_status": "received",
  "handoff_id": "hand_..."
}
```

### 11.6 Handoff idempotency

Canonical payload hash:

```text
validate input schema first
canonicalize accepted JSON with sorted object keys
no insignificant whitespace
arrays preserve order
omit absent optional fields
include explicit nulls only if accepted by schema
payload_hash = lowercase hex SHA-256(canonical JSON bytes)
persist payload_hash and redacted accepted payload in handoffs
```

Idempotency:

| Existing handoff for run | New payload hash | Result |
|---|---|---|
| none | any valid hash | insert handoff |
| exists | same hash | return existing handoff as idempotent success |
| exists | different hash | reject state conflict |

## 12. REST API and SSE contract

### 12.1 Server

API prefix:

```text
/api/v1
```

All business JSON APIs use envelopes. SSE uses `RunEvent` schema and `run_events.seq` replay IDs.

### 12.2 Envelopes

Success:

```json
{
  "data": {},
  "meta": {
    "request_id": "req_...",
    "server_time": "2026-05-08T02:30:00Z"
  }
}
```

Error:

```json
{
  "error": {
    "code": "workflow_validation_failed",
    "message": "WORKFLOW.md has invalid YAML front matter.",
    "details": {},
    "request_id": "req_..."
  }
}
```

HTTP status communicates protocol result; `error.code` communicates product semantics.

### 12.3 Auth API

```http
POST /api/v1/auth/exchange
GET  /api/v1/auth/session
POST /api/v1/auth/logout
```

`symphony open` generates a one-time open token and opens:

```text
http://127.0.0.1:<port>/?open_token=<token>
```

React exchanges it for browser session. Browser uses HttpOnly SameSite cookie plus CSRF header for command APIs. CLI uses bearer token.

### 12.4 Health/state/events

```http
GET /api/v1/health
GET /api/v1/state
GET /api/v1/events
GET /api/v1/events/stream
GET /api/v1/runs/{run_id}/events
GET /api/v1/runs/{run_id}/events/stream
GET /api/v1/issues/{issue_ref}/events/stream
```

SSE:

```text
id = run_events.seq
event = event type
data = JSON RunEvent
```

Reconnect uses `Last-Event-ID`. Events list API MAY use `after_seq`.

### 12.5 Issue API

```http
GET    /api/v1/issues
POST   /api/v1/issues
GET    /api/v1/issues/{issue_ref}
PATCH  /api/v1/issues/{issue_ref}
POST   /api/v1/issues/{issue_ref}/transition
POST   /api/v1/issues/{issue_ref}/comments
POST   /api/v1/issues/{issue_ref}/blockers
DELETE /api/v1/issues/{issue_ref}/blockers/{blocker_issue_ref}
POST   /api/v1/issues/{issue_ref}/dispatch
POST   /api/v1/issues/{issue_ref}/dispatch-pause
POST   /api/v1/issues/{issue_ref}/dispatch-resume
```

Resource refs:

```text
{issue_ref} accepts internal id iss_... or human identifier LOC-1
{blocker_issue_ref} follows same rule
server resolves refs before auth/state/transaction checks
ambiguous/missing/malformed refs return not_found or invalid_request with no partial mutation
responses always include both id and identifier
```

Rules:

```text
PATCH cannot change state
state changes use /transition
dispatch uses /dispatch
blockers use blocker command endpoints
transition leaving Ready/Working/Rework with active run enqueues reconciliation cancel and returns side_effects metadata
```

Transition side effects example:

```json
{
  "data": {
    "issue": {},
    "side_effects": {
      "active_run_cancelled": true,
      "run_id": "run_...",
      "failure_code": "issue_state_changed"
    }
  }
}
```

### 12.6 Run API

```http
GET  /api/v1/runs
GET  /api/v1/runs/{run_id}
GET  /api/v1/runs/{run_id}/events
POST /api/v1/runs/{run_id}/cancel
```

No arbitrary run mutation endpoint. Cancel applies `operator_cancelled` side effects and pauses redispatch.

### 12.7 Approval API

```http
GET  /api/v1/approvals
POST /api/v1/approvals/{approval_id}/decide
```

Decisions:

```text
approve_once
approve_for_run
approve_for_session
deny
cancel_run
```

Only pending approvals can be decided. Approval responses must expose `requested_at`, `timeout_ms`, `expires_at`, `resolved_at`.

### 12.8 Review API

```http
GET  /api/v1/reviews/{issue_ref}
POST /api/v1/reviews/{issue_ref}/send-to-rework
POST /api/v1/reviews/{issue_ref}/mark-done
```

Mark Done guards:

```text
issue.state = Human Review
latest review_packet.status = generated
no active run
```

### 12.9 Artifact API

```http
GET /api/v1/artifacts/{artifact_id}
GET /api/v1/artifacts/{artifact_id}/content
```

Content access MUST enforce containment under `.symphony/artifacts` or `.symphony/exports`. v1 MUST reject raw prompt and raw Codex log content access.

### 12.10 Workflow and diagnostics API

```http
GET  /api/v1/workflow
POST /api/v1/workflow/reload
GET  /api/v1/diagnostics
POST /api/v1/diagnostics/export
```

`dry_run=true` validates without replacing effective config. Invalid reload preserves last valid config. If no valid config exists, dispatch is blocked.

Diagnostics export is redacted only. `include_raw_logs=true` returns `raw_log_access_not_supported`.

### 12.11 Excluded APIs

Do not expose:

```http
POST /api/v1/git/:issue_ref/push
POST /api/v1/git/:issue_ref/create-pr
POST /api/v1/db/backup
POST /api/v1/db/migrate
GET  /api/v1/audit
POST /api/v1/workspaces/:issue_ref/delete
POST /api/v1/secrets
```

### 12.12 Core API enums

`IssueState`:

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

`RunStatus`:

```text
pending
preparing_workspace
rendering_prompt
starting_agent
running
completed
completed_without_handoff
failed
cancelled
```

`ApiErrorCode` includes:

```text
unauthorized
forbidden
csrf_required
invalid_request
not_found
unsupported_db_version
workflow_invalid
workflow_validation_failed
prompt_render_failed
invalid_state_transition
issue_blocked
issue_dispatch_paused
issue_already_running
workspace_conflict
workspace_prepare_failed
after_create_failed
before_run_failed
codex_startup_failed
unsupported_codex_version
codex_protocol_error
approval_not_pending
approval_timeout
review_packet_required
review_packet_failed
tool_token_invalid
tool_gateway_failed
operator_cancelled
agent_blocked
issue_state_changed
canceled_by_reconciliation
command_denied
network_denied
protected_path_denied
raw_log_access_not_supported
internal_error
```

Policy/API errors that terminate runs MUST map to canonical `FailureCode` where applicable.

## 13. Security model

### 13.1 Enforcement boundary

Implementations MUST distinguish:

```text
hard daemon enforcement: API auth, CSRF, Tool Gateway scope, artifact containment, diagnostics export
Codex-mediated enforcement: shell command approvals, file change approvals, network approvals
detection/diagnostic only: events observed after a command already executed
```

Do not describe network deny, protected-path deny, or command deny as OS-level isolation in v1.

### 13.2 Session and CLI tokens

Browser:

```text
HttpOnly SameSite=Lax cookie
cookie name: symphony_session
CSRF header: X-Symphony-CSRF
CSRF required for cookie-authenticated command APIs
```

CLI:

```text
Bearer token
stored in ~/.symphony/cli-session.json
token hash stored in app DB
```

Token requirements:

```text
at least 128 bits entropy; 256 bits preferred
store only SHA-256 or stronger hashes
constant-time presented-token hash comparison
CLI token file readable only by current user where OS supports modes
```

Open token:

```text
created by symphony open
one-time
short TTL, recommended max 5 minutes
stored as hash only
consumed by POST /api/v1/auth/exchange
reuse returns unauthorized
```

### 13.3 Tool token

Scope:

```text
project_id
issue_id
run_id
workspace_path
allowed_tools
expires_at
```

Revoke on:

```text
run terminal outcome
operator cancel
approval cancel_run
reconciliation cancel
daemon shutdown
```

Expiry MUST NOT exceed run timeout plus small cleanup grace.

### 13.4 Command policy

Categories:

```text
allow
review
deny
```

Default allow:

```text
git status
git diff
git log
rg
grep
find
ls
cat
go test ./...
pytest
npm test
pnpm test
cargo test
symphony tool issue get
symphony tool issue comment
symphony tool issue block
symphony tool artifact attach
symphony tool followup create
symphony tool handoff
```

`symphony tool ...` is allowed only as the shell entrypoint; operation authority still comes from Tool Gateway token/scope/cwd/schema/registry checks.

Default review:

```text
npm install
pnpm install
yarn install
pip install
go mod download
cargo fetch
make
docker build
```

Default deny:

```text
git push
git push --force
gh pr create
gh pr merge
sudo
rm -rf /
rm -rf ~
curl | sh
wget | sh
ssh
scp
docker run --privileged
```

v1 uses pattern/prefix classification, not deep supply-chain analysis.

Policy outcomes mapping:

| Policy outcome | Approval row | Terminal failure code |
|---|---|---|
| command auto-denied/denied | `auto_denied` or `denied` | `command_denied` |
| network denied | `auto_denied` or `denied` | `network_denied` |
| protected path denied | `auto_denied` or `denied` | `protected_path_denied` |

### 13.5 Network policy

Default:

```text
network.default = deny
allowlist = []
```

Requests are denied or converted to Approval Inbox items unless allowlisted. v1 does not implement packet firewall, egress accounting, or dependency-origin attribution.

### 13.6 Protected paths

Default protected patterns:

```text
.env
.env.*
**/*.pem
**/*.key
**/*_rsa
**/*_ed25519
.ssh/**
.aws/**
.gcp/**
.azure/**
.kube/**
.npmrc
.pypirc
.netrc
```

Rules:

```text
write protected path → deny
artifact attach protected path → deny
read protected path → deny or approval according to policy mode; default deny for known secret patterns
```

### 13.7 Redaction

Apply redaction to:

```text
run_events.data_json
tool_calls input/output
prompt snapshots
review packets
diagnostic exports
UI log surfaces
```

Preserve safe metadata such as hash, length category, field name, and safe summary. Redaction is best effort and not compliance-grade.

### 13.8 Artifact and export containment

Served artifact paths must resolve under:

```text
<repo>/.symphony/artifacts
<repo>/.symphony/exports
```

Reject:

```text
absolute path input
path traversal
symlink escape
protected path attachment
raw prompt content request
raw Codex log content request
```

### 13.9 Security regression suite

Required tests:

```text
loopback required bind validation
browser session invalid/expired/revoked
CSRF missing on command API
CLI bearer invalid/expired
open token one-time use
tool token wrong run/issue/cwd/tool/expired/revoked
command allow/review/deny classifications
network denied fake request
protected path read/write/attach denied
artifact path traversal and symlink escape
redaction golden fixtures
raw prompt/raw Codex log API refusal
```

## 14. Review packet and rework

### 14.1 Review generator inputs

Review generator runs after `hooks.after_run` has been attempted when workspace exists. Inputs:

```text
issue
run_attempt
workspace
handoff
git diff/status
approval_requests
tool_calls
run_events
prompt_snapshot metadata
agent final message
```

### 14.2 Output directory and files

Output directory:

```text
<repo>/.symphony/artifacts/<issue_identifier>/run_<run_id>/
```

Files:

```text
review.md
review.json
changes.patch
changed-files.txt
untracked-files.json
test-output.txt
agent-final-message.md
commands.jsonl
tool-calls.jsonl
approvals.jsonl
codex-events.redacted.jsonl
prompt/context.json
prompt/rendered_prompt.redacted.md
prompt/prompt_meta.json
prompt/tool_manifest.md
```

Critical files for `status=generated`:

```text
review.md
review.json
changes.patch
changed-files.txt
untracked-files.json
```

Non-critical files may be absent without preventing generation if failure is recorded appropriately.

### 14.3 Generation sequence

```text
1. ensure artifact dir exists
2. collect git status
3. collect tracked changed files and untracked files
4. generate changed-files.txt
5. generate changes.patch with tracked diffs + untracked new-file patches
6. write untracked-files.json, even when empty
7. read handoff
8. export tool calls
9. export approvals
10. export redacted run events
11. copy prompt snapshot metadata files
12. write review.json
13. write review.md
14. insert artifacts rows
15. insert review_packets row
16. emit review.packet_generated
```

Artifact kinds:

| File | artifact.kind | review_packets column |
|---|---|---|
| `review.md` / `review.json` | `review_packet` | `review_md_path` / `review_json_path` |
| `changes.patch` | `patch` | `patch_path` |
| `changed-files.txt` | `changed_files` | `changed_files_path` |
| `untracked-files.json` | `untracked_files` | `untracked_files_path` |
| `prompt/*` | `prompt_snapshot` | `prompt_snapshot_id` via `prompt_snapshots` |

### 14.4 review.json shape

```json
{
  "issue": {
    "id": "iss_...",
    "identifier": "LOC-1",
    "title": "..."
  },
  "run": {
    "id": "run_...",
    "status": "completed"
  },
  "git": {
    "branch_name": "symphony/LOC-1-...",
    "base_ref": "origin/main",
    "base_ref_config": "auto",
    "base_sha": "...",
    "head_sha": "...",
    "dirty": true
  },
  "handoff": {
    "summary": "...",
    "tests": [],
    "risks": [],
    "verification": []
  },
  "changed_files": [],
  "untracked_files": [],
  "approvals": [],
  "tool_calls": [],
  "prompt_snapshot": {
    "id": "ps_...",
    "rendered_prompt_hash": "...",
    "tool_manifest_path": "prompt/tool_manifest.md"
  }
}
```

### 14.5 review.md sections

```markdown
# LOC-1 Review Packet

## Summary
## Acceptance Criteria
## Handoff
## Changed Files
## Tests
## Risks
## Verification Steps
## Approvals
## Tool Calls
## Git
## How to Continue
```

### 14.6 Untracked file guarantee

A review packet with untracked files is not `generated` unless untracked file contents are represented in `changes.patch`.

`untracked-files.json` shape:

```json
[
  {
    "path": "src/new-file.ts",
    "size_bytes": 1234,
    "sha256": "...",
    "patch_included": true
  }
]
```

Protected paths, path traversal, or files outside workspace MUST fail review generation with `review_packet_failed`; they must not be silently omitted.

### 14.7 Human Review transition

Finalizer transitions issue to `Human Review` only when:

```text
handoff exists for run
handoff.target_state = Human Review
critical review packet files are written
review_packets.status = generated
run terminal outcome is otherwise successful
```

Any other handoff target is invalid in v1.

### 14.8 Mark Done gating

`review mark-done` requires:

```text
issue.state = Human Review
latest review_packet.status = generated
review_packet.run_id belongs to latest completed handoff run
no active run
```

Partial/failed review packets can be viewed but cannot Mark Done.

### 14.9 Rework

Send-to-rework entrypoints:

```http
POST /api/v1/reviews/{issue_ref}/send-to-rework
```

```bash
symphony review send-to-rework LOC-1 --reason "..."
```

Guards:

```text
issue.state = Human Review
latest review_packet.status = generated
no active run
operator supplies non-empty reason or feedback comment
```

Side effects:

```text
issue.state = Rework
issues.dispatch_paused = false
clear dispatch pause reason/timestamp
insert state_history Human Review → Rework
insert operator/system comment with feedback
emit review.sent_to_rework
```

Dispatch from Rework:

```text
dispatch_reason = rework
same workspace row reused
same branch reused
same base_sha retained
before_run hook runs
prompt includes latest review feedback and previous review packet summary
```

Review packets are immutable. Rework creates a new packet. All review packets are cumulative from workspace `base_sha` to current workspace tree, not incremental from previous packet.

## 15. Dashboard technical requirements

### 15.1 Routes

```text
/                  Overview
/issues            Board
/issues/:issueId   Issue Detail
/runs/:runId       Run Detail
/approvals         Approval Inbox
/reviews/:issueId  Review Packet
/workflow          Workflow
/diagnostics       Diagnostics
```

Dashboard is a control surface only. It MUST NOT directly access SQLite, Git, filesystem, Codex, or Tool Gateway.

### 15.2 API client

Generated:

```text
web/src/api/generated/*
```

Handwritten:

```text
web/src/api/client.ts
web/src/api/events.ts
web/src/api/errors.ts
```

Responsibilities:

```text
base URL
session/CSRF
envelope unwrap
error.code mapping
SSE reconnect
Last-Event-ID
query invalidation
```

### 15.3 State model

```text
server state: query cache
live updates: SSE reducer + query invalidation
form state: local component state
global UI state: minimal preferences only
```

Do not mirror orchestrator state in frontend. UI mutations call command APIs and refetch/consume SSE to confirm.

### 15.4 Page responsibilities

Overview shows:

```text
workflow status
running runs
pending approvals
failed runs
Human Review count
dispatch paused issues
Codex availability
recent events
```

Board columns:

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

Issue Detail shows issue facts, comments, blockers, workspace, run history, review packets, and dispatch paused state. It MUST expose dispatch resume when paused.

Run Detail shows normalized timeline:

```text
workspace prepared
prompt rendered
Codex started
approval requested
tool called
handoff submitted
review generated
failure
```

Approval Inbox shows command/file/network approvals, risk level, policy match, and approve/deny/cancel controls.

Review Packet page shows summary, tests, risks, verification, changed files, diff, tool calls, approvals, Send to Rework, and Mark Done.

Workflow page shows current validation, last valid config, warnings/errors, reload, and render preview.

Diagnostics page shows daemon, project paths, Codex, Git, DB, workflow, and redacted export.

## 16. Observability and diagnostics

### 16.1 Events

Durable normalized events are stored in `run_events`. UI and CLI timeline views use normalized events, not raw Codex protocol messages.

Required event families include:

```text
scheduler.*
issue.*
run.*
agent.*
approval.*
tool.*
handoff.*
review.*
hook.*
system.*
```

### 16.2 Logs

v1 SHOULD emit structured app logs and run JSONL logs. Raw Codex log references MAY be stored for local diagnostics but MUST NOT be exposed through v1 API as raw content.

### 16.3 Diagnostics API

Diagnostics MUST show at least:

```text
project_id
repo_root
DB paths and version status
workflow validation and last valid config metadata
daemon pid/uptime/runtime descriptor
Codex availability/version/support status
Git repo/worktree status
redaction enabled state
warnings
```

Diagnostics export is redacted-only. `include_raw_logs=true` MUST return `raw_log_access_not_supported`.

## 17. Upstream-vs-Local resolution table

| Topic | Local v1 implementation rule | Required test |
|---|---|---|
| Tracker | `tracker.kind: local`; no Linear API surface. | Issue CRUD and dispatch without Linear config. |
| Reconciliation | Active run leaving `Ready/Working/Rework` is cancelled and workspace retained. | Transition active Working issue to inactive state. |
| Operator cancel | `cancelled/operator_cancelled`, tool tokens revoked, dispatch paused. | Next tick does not redispatch until resume. |
| Retry | No automatic retry timers/queue. | Failure pauses and stays idle after waiting. |
| Continuation | One main turn plus at most one handoff continuation. | Missing handoff once continues; twice pauses. |
| Hooks | `after_create`, then `before_run` on first run; `before_run` on every run; `after_run` finally. | Hook order tests. |
| NormalizedIssue | Keep top-level git/workspace aliases. | `issue.branch_name` and `git.branch_name` both render. |
| Workspace key | Replace invalid chars with `-`; max 80. | Slash/space/unicode/long text tests. |
| Handoff target | Only `Human Review`; submit does not transition. | `agent.handoff_state: Done` fails validation. |
| Review finalizer | Review packet success required for Human Review; untracked files included. | Review failure blocks Human Review. |
| Terminal run states | Compact status + canonical failure_code. | Timeout/stall/cancel/reconcile code tests. |
| Cleanup | Never auto delete/reset/clean workspaces. | Terminal issue keeps workspace. |
| Tool CLI policy | `symphony tool ...` allowed but gateway-authorized. | Wrong token denied. |
| Issue refs | Path refs accept `iss_...` and `LOC-...`. | Both return same issue. |
| Codex fixtures | Unsupported version fails before dispatch. | Unsupported fake version test. |
| Workflow reload | Active runs keep snapshot; invalid reload preserves last valid config. | Invalid reload does not crash active run. |
| OpenAPI/DB | API/DB contracts are implemented from this Tech SPEC. | Contract tests and schema init tests. |
| Rework | Same workspace/branch/base_sha; cumulative packet. | Human Review → Rework → Human Review creates immutable cumulative packet. |
| Security boundary | Hard/Codex-mediated/detection-only controls distinguished. | Security regression suite. |

## 18. Testing strategy

### 18.1 Test layers

```text
unit
integration
fake-agent E2E
real-Codex opt-in
frontend component
API contract
security regression
```

Default CI MUST NOT run real Codex tests.

### 18.2 Default test commands

The final repo should expose equivalent commands:

```bash
go test ./...
pnpm --dir web typecheck
pnpm --dir web test
go test ./internal/e2e -run TestMainPathFakeAgent
go test ./internal/e2e -run TestMissingHandoffThenContinuation
go test ./internal/e2e -run TestMissingHandoffTwicePausesDispatch
go test ./internal/e2e -run TestUntrackedFileIncludedInReviewPatch
go test ./internal/e2e -run TestOperatorCancelNoRedispatch
go test ./internal/e2e -run TestApprovalCancelRunNoRedispatch
go test ./internal/e2e -run TestActiveRunReconciliationCancel
go test ./internal/e2e -run TestAgentIssueBlockCancelsRun
go test ./internal/e2e -run TestStartupStaleRunInterrupted
```

Real Codex:

```bash
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex/...
```

### 18.3 Unit tests

Required coverage:

```text
core state transitions
workflow parser
effective config defaults
strict prompt rendering
path normalization
branch naming
workspace key sanitization
command policy
protected path matching
redaction
split ApiErrorCode vs FailureCode mapping
agent turn-count/handoff constraints
handoff canonical payload hashing
run outcome precedence and finalizer/cancel race handling
```

### 18.4 Integration tests

Required coverage:

```text
SQLite schema init
unsupported DB version refusal
foreign key enforcement
issue create transaction
issue sequence concurrent allocation
attempt_no monotonic allocation
blocker eligibility query
worktree create/reuse
hook lifecycle
Tool Gateway token validation
artifact attach containment
review packet generation after after_run
untracked new-file content in changes.patch
SSE replay from run_events.seq
single-daemon project lock refusal
review packet generated row cannot point to missing files
```

### 18.5 Fake-agent E2E scenarios

```text
init → create issue → Ready → dispatch → fake handoff → review → Done
missing handoff → continuation → handoff
missing handoff twice → dispatch_paused
invalid tool token
approval pending → approve → continue
command denied
network denied
protected path denied
operator run cancel → no redispatch
approval cancel_run → no redispatch
workflow invalid → dispatch blocked
workspace conflict
review packet failure → no Human Review
untracked file created by agent → patch includes file content
active run issue transition → reconciliation cancel
Working issue with no active run and dispatch_paused=true is not scheduler-redispatched
agent issue.block → Blocked + cancelled/agent_blocked
stale running run on startup → interrupted + dispatch_paused
Rework after Human Review → same workspace and cumulative packet
```

### 18.6 API contract tests

```text
all success responses use {data, meta}
all errors use {error: {code, message, details, request_id}}
OpenAPI generated from/consistent with this spec validates
handlers conform to API schemas
frontend generated types compile
{issue_ref} accepts iss_... and LOC-...
run cancel side effects schema-covered
approval cancel_run side effects schema-covered
SSE id equals run_events.seq
Last-Event-ID / after_seq replay works
artifact content enforces containment and rejects raw prompt/raw Codex log
```

## 19. Implementation phases M0–M8

### M0 — Contracts and scaffolding

Deliver:

```text
Go module and cmd/symphony
basic HTTP server and health endpoint
React/TypeScript dashboard skeleton
SQLite init for app/project DB
WORKFLOW parser skeleton
API contract generation/validation from this spec
store contract tests
Codex fixture-gate fake adapter scaffold
acceptance harness
```

Acceptance:

```text
symphony init works
symphony serve starts localhost API
GET /api/v1/health works
dashboard opens
project/app DB created
WORKFLOW.md parsed/validated
unsupported DB version fails safely
```

### M1 — Local tracker and store

Deliver issue CRUD, comments, labels, relations, state history, transition API, blocker eligibility, Board/Issue Detail, issue CLI.

Acceptance:

```text
Create LOC-1
Move Inbox → Ready
Add blocker
Blocked issue not dispatchable
Board shows states
```

### M2 — Workflow, prompt, and workspace

Deliver EffectiveConfig, strict prompt rendering, workspace path resolver, branch generator, worktree create/reuse, base_ref auto resolver, hook lifecycle, git preflight.

Acceptance:

```text
WORKFLOW validates Human Review handoff target
prompt includes issue/workspace/git/tools
new workspace after_create then before_run
reuse before_run only
main repo not modified
```

### M3 — Orchestrator and fake runner

Deliver single actor scheduler, claim transaction, active run reconciliation, cancellation, failure pause, fake-runner E2E.

Acceptance:

```text
single actor owns dispatch/outcomes
failure pauses dispatch
operator cancel and approval cancel_run do not redispatch
startup stale active runs interrupted
```

### M4 — Tool gateway and handoff

Deliver IPC server, run-scoped tokens, fixed registry, tool persistence, issue.get/comment/block, artifact.attach, followup.create, handoff.submit, canonical payload hash, missing handoff continuation.

Acceptance:

```text
tool token validates run/issue/cwd/tool scope
handoff idempotent by payload_hash
issue.block cancels current run with agent_blocked
followup.create creates Inbox issue and followup_of relation
```

### M5 — Review packet and Human Review gate

Deliver review packet generator after after_run, critical files, exports, finalizer transition, Review Packet UI, Send to Rework, Mark Done.

Acceptance:

```text
review packet includes untracked file content
after_run output captured before review packet generation
review packet failure prevents Human Review
rework packet cumulative from base_sha
```

### M6 — API, CLI, dashboard, approval, security

Deliver REST/SSE handlers, CLI clients, dashboard pages, browser session, CSRF, CLI bearer token, command/network/protected-path policy, Approval Inbox, redaction, artifact containment.

Acceptance:

```text
handlers conform to API contract
CLI matches API side effects
dashboard can review/approve/cancel/pause/resume/diagnose
git push denied or unapprovable
network default denied/reviewed
protected path access denied
```

### M7 — Codex adapter

Deliver fixture-gated Codex adapter, process manager, protocol parser, approval bridge, timeout/cancel mapping, real Codex opt-in tests.

Acceptance:

```text
unsupported Codex version fails before dispatch
approval bridge maps decisions correctly
timeout and cancellation semantics match run lifecycle
real Codex tests opt-in only
```

### M8 — Release hardening

Deliver full test suite, security regression, release binary, docs/help alignment, known limitations.

Acceptance:

```text
Definition of Done satisfied
security regressions pass
single dist/symphony binary built
Quickstart and CLI help match implementation
```

## 20. Release and Definition of Done

Release build output:

```text
dist/symphony
```

Build steps:

```text
frontend typecheck
build React static assets
embed static assets
go test ./...
fake-agent E2E
build single symphony binary
```

A v1 implementation is done only when:

```text
local tracker works without Linear
main path reaches Done through review packet
handoff target fixed to Human Review
handoff is two-stage
failure/cancel/missing handoff/agent block pause dispatch
workspaces retained in all terminal/failure cases
rework uses same workspace and cumulative diff
API/DB/CLI/dashboard conform to this spec
Codex adapter is fixture-gated
fake-agent E2E and security regression pass
real Codex tests are opt-in
loopback/session/CSRF/tool-token protections work
raw prompt/raw Codex logs not exposed by v1 API
single dist/symphony binary builds
known limitations are documented
```
