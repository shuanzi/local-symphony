# Local Symphony App v1 Tech SPEC

**状态**：v1 技术方案合并版
**更新日期**：2026-05-11
**来源**：`local-symphony.zip` 原始文档包经 agent-executable hardening 后更新
**文档权威性**：`PRD.md` 是 Local Symphony App v1 产品事实与产品范围的 source of truth。本文档是字段、表结构、API/schema、状态机、校验规则等技术合同细节的 source of truth；`api/openapi.yaml`、`db/schema/*.sql`、`schemas/*.schema.json`、`docs/agent_work_orders/*.md` 与 `docs/testing/*.md` 是本文的可执行合同与验收材料。本文档与 executable contracts 不得新增或扩大 `PRD.md` 未定义的 v1 产品能力。

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

当 `PRD.md`、本文与 executable contract artifacts 冲突时：

- 产品事实与产品范围以 `PRD.md` 为准。
- 字段、表结构、API/schema、状态机、校验规则、CLI、安全、测试、发布等技术合同细节以本文及 executable contract artifacts 为准。
- 本文与 executable contract artifacts 不得新增或扩大 `PRD.md` 未定义的 v1 产品能力。

实现可以在代码仓库中生成或维护 OpenAPI、SQL schema、CLI help、test manifests 等文件，但这些文件必须与本文合同一致。

### 2.1 Executable contract artifacts

实现 agent MUST 同时消费以下合同文件：

```text
api/openapi.yaml
db/schema/v1_app.sql
db/schema/v1_project.sql
schemas/workflow_config.schema.json
schemas/normalized_issue.schema.json
schemas/run_event.schema.json
schemas/tool_gateway.schema.json
schemas/tools/*.input.schema.json
schemas/review_packet.schema.json
schemas/diagnostics.schema.json
schemas/failure_codes.schema.json
docs/agent_work_orders/*.md
docs/testing/*.md
docs/codex/*.md
```

`docs/agent_work_orders/*.md` 包含 `M0_*.md` 至 `M8_*.md` milestone 任务单，也包含该目录下的 `README.md` 与 `EXECUTION_PROTOCOL.md`；implementation agent MUST 同时消费这些目录级合同说明。

这些文件必须与本文保持一致。若发现冲突，implementation agent MUST 先提交文档/合同修正，再继续实现，不得在代码中自行发明第三套 API、DB 或 JSON shape。

## 3. 实现边界

### 3.1 MUST implement

```text
local SQLite tracker
project/app SQLite DB initialization and version guard backed by db/schema/v1_*.sql
WORKFLOW.md parser and strict prompt renderer
git worktree workspace manager
single-actor orchestrator
fake runner E2E path
Codex app-server adapter with fixture gate
REST API and SSE event stream backed by api/openapi.yaml
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
Tauri desktop shell
production DB migration/rollback framework
automatic SQLite backup/restore
crash recovery beyond startup stale active-run interruption
full audit log
supply-chain deep risk scoring
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

Each run launches one Codex app-server process group, with cwd set to the issue workspace. Operator run cancellation, approval `cancel_run`, reconciliation, shutdown, timeout, or context cancellation MUST terminate the process group gracefully first, then kill if needed.

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

## 4A. Supported platform scope

v1 supported platforms:

```text
macOS arm64/x64
Linux x64
```

Minimum runtime contract:

```text
Go: stable version declared in go.mod and CI matrix
Node.js: active LTS version declared in package/tooling metadata when dashboard assets are built
pnpm/npm: package manager and version declared in packageManager or equivalent repo metadata
SQLite: bundled driver/runtime must support the SQL in db/schema/v1_*.sql on supported platforms
Git: installed git CLI available on PATH; minimum supported version documented in release notes
Codex: real adapter is fixture-gated; unsupported installed Codex versions fail before process launch
```

Release build/test matrix:

| Platform | Build | Default tests | Release blocking |
|---|---|---|---|
| macOS arm64 | `symphony` binary | unit, integration, contract, fake-runner E2E | yes |
| macOS x64 | `symphony` binary | unit, integration, contract, fake-runner E2E | yes |
| Linux x64 | `symphony` binary | unit, integration, contract, fake-runner E2E | yes |
| Windows | optional experimental build only | optional smoke tests only | no |

Real Codex tests remain opt-in with `SYMPHONY_TEST_CODEX=1` and MUST NOT be part of default release blocking CI. Windows support is best-effort only in v1; v1 does not require a Windows binary or Windows blocking CI. If implementation chooses to support Windows, it MUST define and test named pipe transport, process group termination, CRLF patch behavior, and path normalization, and must document unsupported cases in known limitations.

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
unknown top-level config keys warn and do not block dispatch
nested unknown keys under extension-friendly sections warn or ignore according to section rules
wrong type / missing required field / unsupported enum / env unset-or-empty errors block dispatch
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

`$VAR_NAME` is expanded only when the environment variable is set and the value is non-empty. If the variable is unset or expands to an empty string, workflow validation fails and dispatch is blocked. Partial strings such as `/tmp/$VAR_NAME` are not env-expanded config values.

`schemas/workflow_config.schema.json` is the machine contract for known fields and hard constraints. Its top-level `additionalProperties` MUST be `true` so the parser can accept unknown top-level keys, emit warnings, and continue validation of known fields instead of failing schema validation before warnings are produced. Known sections MAY remain locally strict with `additionalProperties: false` where this spec defines them as strict.

### 6.2 EffectiveConfig defaults

```yaml
tracker:
  kind: local
  project: default
  dispatch_candidate_states: [Ready, Rework]
  reconciliation_active_states: [Ready, Working, Rework]
  terminal_states: [Done, Cancelled, Duplicate]

polling:
  interval_ms: 30000

workspace:
  root: <global-workspace-root>
  cleanup:
    enabled: false
    note: "v1 never deletes, resets, cleans, rebases, or removes workspaces automatically"

hooks:
  after_create: null
  before_run: null
  after_run: null
  before_remove: null  # future-compatible only; MUST NOT be executed in v1
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
  require_committed_fixture: true

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
  raw_codex_log_retention_days: 0  # v1 default: do not persist raw Codex logs

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

`workspace.root` is the global workspace root. The daemon/workspace resolver derives the project-scoped root as `<workspace.root>/<project_id>`, and each issue workspace is `<workspace.root>/<project_id>/<issue_identifier>`.

`dispatch_candidate_states` MUST be used for normal scheduler eligibility. `reconciliation_active_states` MUST be used only to decide whether an already-active run is still valid. `Working` MUST NOT be included in normal dispatch candidates.

`git.agent_commit: manual` is a prompt contract flag, not a Symphony commit feature. v1 MUST NOT expose commit APIs/CLI/dashboard actions, MUST NOT create or rewrite commits, and MUST NOT push commits. The default prompt instructs the agent not to commit unless the operator explicitly requested it outside the current run; if such a commit exists, review generation still compares the workspace tree against `base_sha` without mutating the real Git index.

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
git.branch_prefix MUST equal symphony
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

Path rules are evaluated after successful full-string `$VAR_NAME` expansion. Unset or empty env-expanded path values are workflow validation errors and do not silently normalize to empty paths.

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
warning-only reloads are valid reloads and may replace the effective config
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

If main turn completes without handoff and `agent.max_handoff_continuations=1`, v1 sends one dedicated handoff continuation prompt in the same session/thread. It does not resend the full task prompt. If `agent.max_handoff_continuations=0`, the first missing handoff goes directly to the missing-handoff terminal path.

Prompt snapshot files:

```text
prompt/context.json
prompt/rendered_prompt.redacted.md
prompt/prompt_meta.json
prompt/tool_manifest.md
```

### 6.7 Default WORKFLOW prompt contract

`examples/WORKFLOW.default.md` is the normative default WORKFLOW example. Its prompt body MUST satisfy this contract, in addition to passing strict config validation and strict prompt rendering.

The default prompt MUST explicitly tell the agent:

```text
work only inside the current workspace
do not push branches
do not create pull requests
do not mark issues Done
do not commit unless the operator explicitly requested it outside the current run
after completion, submit handoff JSON through stdin
run symphony tool handoff submit --json -
do not leave a handoff.json temporary file in the workspace root
handoff submits data only
Human Review transition is performed by the finalizer after successful handoff processing
```

The default prompt MUST NOT instruct agents to write handoff JSON to a workspace-root file as the primary path. File-based examples, if any, MUST be clearly secondary diagnostics and MUST preserve the no-root-`handoff.json` rule.

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

Each DB MUST have a `schema_meta` table containing exactly one `key = 'schema_version'` row with `value = '1'`.

| Condition | Behavior |
|---|---|
| DB missing | Initialize schema v1. |
| `schema_meta` missing or `schema_version` key missing | Fail with `unsupported_db_version`; do not mutate. |
| `schema_meta['schema_version'] = '1'` | Continue. |
| `schema_meta['schema_version']` parses to a version greater than 1 | Fail with `unsupported_db_version`; do not mutate. |
| `schema_meta['schema_version']` parses to a version less than 1 | Fail with `unsupported_db_version`; no migration in v1. |
| `schema_meta['schema_version']` cannot be parsed | Fail with `unsupported_db_version`; do not mutate. |

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

### 7.4A Executable SQL schema

The normative SQLite DDL is maintained in:

```text
db/schema/v1_app.sql
db/schema/v1_project.sql
```

Implementation MUST initialize databases from these files or from byte-identical embedded copies. Field lists in the subsections below are explanatory; if a field is missing from the SQL DDL, the DDL and this document MUST be fixed before implementation continues.

All timestamps MUST be RFC3339 UTC strings. Boolean values MUST be stored as INTEGER with `CHECK(value IN (0,1))`. JSON columns MUST contain valid JSON text.

### 7.5 App DB contract

Tables:

```text
schema_meta
projects
app_settings
local_sessions
open_tokens
runtime_descriptors
```

`projects` fields:

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

`local_sessions` stores only token/CSRF hashes and local session metadata:

```text
id
project_id
kind ∈ browser|cli|desktop
token_hash UNIQUE
csrf_hash
user_label
created_at
last_seen_at
expires_at
revoked_at
```

`open_tokens` are one-time browser bootstrap tokens stored hash-only. `runtime_descriptors` is a non-secret cache of daemon endpoint metadata; the authoritative runtime descriptor remains the file under `~/.symphony/runtime/`.

### 7.6 Project DB contract

Tables:

```text
schema_meta
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
priority INTEGER 1..5, where 1 is highest and 3 is the default
dispatch_paused 0/1
dispatch_pause_reason
dispatch_paused_at
created_at
updated_at
completed_at
archived_at
```

`dispatch_paused` prevents repeated dispatch after failure, missing handoff, cancellation, block, or startup interruption.

`archived_at` is future-reserved in v1. No v1 API, CLI, dashboard affordance, scheduler guard, or Tool Gateway path may archive an issue or reject a request solely because `archived_at` is non-null; compatible v1 records should keep it null.

#### issue_relations

Relation directions are fixed:

| relation_type | source_issue_id | target_issue_id | Agent permission |
|---|---|---|---|
| `blocks` | blocker issue | blocked issue | not via Tool Gateway |
| `duplicates` | duplicate issue | canonical issue | not via Tool Gateway |
| `followup_of` | follow-up issue | original/current issue | only through `followup.create` |

An issue is blocked while any active direct blocker relation has `relation.active = true` and its source blocker issue is not terminal. Terminal blocker states:

```text
Done
Cancelled
Duplicate
```

#### workspaces

Normative fields are defined in `db/schema/v1_project.sql`.

```text
id
issue_id UNIQUE
path UNIQUE
branch_name
base_ref_config
base_ref
base_sha
status ∈ prepared|conflict|missing
created_at
updated_at
```

v1 never automatically sets workspace to removed via cleanup. Removed/cleanup states are not part of v1 DDL.

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

Key fields:

```text
id
issue_id
attempt_no
workspace_id
workflow_snapshot_id
status
dispatch_reason ∈ manual|scheduler
source_issue_state ∈ Ready|Rework
runner_kind ∈ fake|codex
base_ref_config
base_ref
base_sha
branch_name
failure_code
failure_message
started_at
ended_at
created_at
updated_at
```

`source_issue_state` is mandatory for dispatched runs and is used to restore the issue to Ready/Rework on failure, cancellation, missing handoff, or review packet failure, unless the issue is already Blocked/Cancelled/Duplicate due to operator transition or agent `issue.block`.

#### run_events

`run_events.seq INTEGER PRIMARY KEY AUTOINCREMENT` is the SSE replay ID. `id` is a business event ID. UI timelines MUST render from durable normalized `run_events`, not raw Codex logs.

Normative fields:

```text
seq
id
project_id
issue_id
run_id
event_type
actor_type ∈ system|operator|agent|codex|hook
data_json
redacted
created_at
```

#### approval_requests

```text
id
issue_id
run_id
kind ∈ command|file_change|network
status ∈ pending|approved_once|approved_for_run|approved_for_session|denied|auto_denied|cancelled|timeout
request_json
decision_json
reason
timeout_ms
expires_at
created_at
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
kind ∈ test_output|patch|changed_files|untracked_files|diffstat|prompt_snapshot|codex_log|review_packet|agent_file|diagnostic|other
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
handoff_id
packet_no
status ∈ generated|partial|failed
root_path
review_md_path
review_json_path
patch_path
changed_files_path
untracked_files_path
diffstat_path
prompt_snapshot_id
failure_code
failure_message
created_at
```

A `generated` row MUST NOT point to missing critical files. `partial` and `failed` rows may omit file-path columns, but cannot satisfy Mark Done guards.

### 7.7 Transaction rules

Create issue transaction:

```text
increment counters.issue_sequence
insert issue
insert issue_state_history
insert issue.created event
```

Transition issue transaction:

```text
validate transition
if transition leaves active states and an active run exists, enqueue reconciliation cancel
update issue state
insert issue_state_history
insert issue.transitioned event
```

Dispatch claim transaction:

```text
shared DispatchIssue preflight for scheduler dispatch and manual dispatch/API/CLI
validate issue.state IN Ready/Rework
validate required issue fields per PRD 8.1
validate not paused
validate no active blockers
validate no active run
validate workflow valid or last valid config available according to reload semantics
validate available concurrency slot
allocate attempt_no
create run_attempt pending
transition issue to Working if needed
insert scheduler.dispatch_claimed event
commit before workspace/token/process/prompt creation
```

If any DispatchIssue preflight check fails, the transaction MUST roll back without creating `run_attempt`, changing `issue.state`, allocating workspace/token/process resources, or rendering prompts.

Handoff tool transaction:

```text
validate run-scoped token
validate running run
insert or idempotently return handoff
insert tool_call
insert handoff.submitted event
```

`handoff.submit` MUST NOT insert an issue comment implicitly. If the agent needs a user-visible discussion comment, it must call `issue.comment` explicitly.

Review finalizer transaction after files are written:

```text
guard handoff exists
guard no higher-precedence cancellation/failure has won
guard after_run attempted if workspace exists
guard critical files written before status=generated
insert artifact rows
insert review_packet status=generated
run.status = completed
issue.state = Human Review
clear dispatch_paused
insert issue_state_history
insert review.packet_generated event
```

Missing handoff after allowed continuation:

```text
run.status = completed_without_handoff
run.failure_code = missing_handoff
if run_attempt.source_issue_state in Ready/Rework and issue is not Blocked/Cancelled/Duplicate due to operator transition or agent issue.block:
  issue.state = run_attempt.source_issue_state
if issue is Blocked/Cancelled/Duplicate due to operator transition or agent issue.block:
  keep current issue state
issue.dispatch_paused = true
dispatch_pause_reason = missing_handoff
system comment
handoff.missing event
```

### 7.8 NormalizedIssue DTO

`NormalizedIssue` is the stable shape used by orchestrator, prompt rendering, API, dashboard, and review metadata.

The normative JSON Schema for this DTO is:

```text
schemas/normalized_issue.schema.json
```

The OpenAPI `Issue` schema MUST expose the same required field set as `schemas/normalized_issue.schema.json`. If prompt aliases, dashboard fields, or tool responses need new issue fields, update `TECH_SPEC.md`, `schemas/normalized_issue.schema.json`, and `api/openapi.yaml` in the same change.

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
      AND r.active = 1
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

Note: `Working` is a reconciliation active state, but not a normal scheduler dispatch candidate.

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
| `Ready` | `Working` | orchestrator | Dispatch claim transaction succeeds and required issue fields are valid. |
| `Rework` | `Working` | orchestrator | Dispatch claim transaction succeeds and required issue fields are valid. |
| `Working` | `Human Review` | run finalizer | Handoff exists and review packet status is `generated`. |
| `Working` | `Ready`/`Rework` | finalizer/orchestrator/startup guard | Failure, cancellation, missing handoff after allowed continuation, review packet failure, or stale-run recovery restores `run_attempt.source_issue_state` when the issue has not already moved to `Blocked`/`Cancelled`/`Duplicate`; see 8.10, 8.11, 8.13, and 8.15. |
| `Human Review` | `Rework` | operator | Reviewer supplies non-empty reason; UI may present it as feedback. |
| `Human Review` | `Done` | operator | Latest review packet status `generated`; latest review_packet.run_id belongs to latest completed handoff run; no active run; operator supplies non-empty reason; UI may present it as comment; sets `completed_at=now` and records history/comment/events. |
| any non-terminal | `Blocked` | operator or agent tool | Active run reconciliation cancels active run and pauses dispatch. Agent tool uses `agent_blocked`; operator transition uses reconciliation failure-code rules. |
| any non-terminal | `Cancelled` | operator | Active run reconciliation cancels active run and pauses dispatch. |
| any non-terminal | `Duplicate` | operator | Active run reconciliation cancels active run and pauses dispatch. |
| `Blocked` | `Ready` | operator | Block resolved; no active blocker relations remain; required issue fields valid. v1 intentionally resolves all `Blocked` issues to `Ready`; prior review/rework context is retained as history, but unblocking does not automatically return to `Rework`. |
| `Done`/`Cancelled`/`Duplicate` | `Inbox`/`Ready` | operator | Explicit reopen only; reopening to `Ready` requires valid required issue fields, while reopening to `Inbox` still requires only title; direct reopen to `Working`, `Human Review`, `Rework`, or `Blocked` is forbidden; requires no active run and does not reuse old runs. Reopen sets `completed_at=null`, clears `dispatch_paused`/reason/paused_at, and preserves workspace, history, review packets, and duplicate relations. |

Required issue fields valid follows the PRD 8.1 product rule: title and description are non-empty after trimming, acceptance_criteria has at least one non-empty trimmed item, and priority is an integer in 1..5. Creating an Inbox issue still requires only title.

Reopen rules:

```text
target_state ∈ Inbox|Ready
no active run
old run_attempts are never reused
reopen to Inbox does not dispatch automatically
reopen to Ready makes the issue eligible for a new run on the next scheduler tick, subject to normal eligibility
latest review packet is retained as history only; completion after reopen requires a latest review packet from a new post-reopen handoff run
duplicate relations are retained; operator must remove or deactivate them separately when the issue is no longer a duplicate
```

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

`DispatchIssue` is the single dispatch entrypoint for both scheduler-selected candidates and manual dispatch requests from API/CLI. Only the orchestrator actor creates run attempts and decides dispatch. Worker goroutines report outcomes to the actor; they do not directly mutate scheduler terminal state.

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
| Operator run cancel | `cancelled` | `operator_cancelled` |
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
7. Claim issues through `DispatchIssue` until slots exhausted.
8. Launch run workers.
9. Emit scheduler events.
```

The slot count from step 4 is an upper bound for scheduler selection only. Each `DispatchIssue` claim MUST re-run the full dispatch preflight, including workflow validity and currently available concurrency slot.

### 8.7 Dispatch eligibility

Normal scheduler candidates:

```text
Ready
Rework
```

`Working` is not a normal scheduler candidate. It is valid only while a run is active and during reconciliation. A `Working` issue with no active run MUST NOT be redispatched automatically.

`DispatchIssue` preflight MUST be shared by normal scheduler dispatch and manual dispatch API/CLI. The scheduler supplies sorted candidates; manual dispatch supplies a single `{issue_ref}`. Both paths use the same claim transaction and error mapping.

Dispatch claim transaction MUST:

```text
1. verify issue.state in Ready/Rework
2. verify required issue fields are valid according to PRD 8.1
3. verify not dispatch_paused
4. verify no active blocker relation
5. verify no active run
6. verify workflow valid or last valid config available according to reload semantics
7. verify available concurrency slot
8. create run_attempt with source_issue_state = current issue.state
9. set issue.state = Working
10. emit run.claimed and issue.state_changed events
```

On any preflight failure, the implementation MUST NOT create a `run_attempt`, MUST NOT change `issue.state`, and MUST NOT launch workspace/token/process/prompt side effects.

Preflight failure mapping:

| Failed condition | `ApiErrorCode` | CLI exit |
|---|---|---:|
| issue.state not `Ready/Rework` | `invalid_state_transition` | 7 |
| required issue fields invalid | `invalid_request` | 2 |
| `dispatch_paused` | `issue_dispatch_paused` | 7 |
| active blocker relation exists | `issue_blocked` | 7 |
| active run exists | `issue_already_running` | 7 |
| no valid workflow or last valid config | `workflow_invalid` | 9 |
| no available concurrency slot | `concurrency_limit_reached` | 7 |

No automatic retry queue/timers exist in v1. Manual redispatch is represented by an operator command after pause is cleared, not by a background retry scheduler.

### 8.8 Active run reconciliation

Active statuses:

```text
pending
preparing_workspace
rendering_prompt
starting_agent
running
```

Reconciliation-valid issue states:

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
startup inconsistent Working issue guard
```

If an issue with an active run leaves `Ready`/`Working`/`Rework`:

```text
1. send CancelRun to orchestrator actor
2. terminate Codex process group if it exists
3. set run_attempt.status = cancelled
4. set failure_code = issue_state_changed unless a more specific code applies
5. set issues.dispatch_paused = true
6. set issues.dispatch_pause_reason = run_attempt.failure_code
7. set issues.dispatch_paused_at = now
8. set ended_at
9. revoke run-scoped tool tokens
10. emit run.cancelled, scheduler.reconciled, and scheduler.paused events
11. retain workspace without reset/clean/delete
```

Operator transitions that trigger active run reconciliation MUST pause dispatch after canceling the run. The pause reason MUST be the same canonical value as `run_attempt.failure_code`: ordinary non-terminal operator transitions use `issue_state_changed`, terminal reconciliation uses `canceled_by_reconciliation`, and agent `issue.block` uses `agent_blocked`.

Specific codes:

| Trigger | Local status | failure_code |
|---|---|---|
| Operator run cancel | `cancelled` | `operator_cancelled` |
| Approval `cancel_run` | `cancelled` | `operator_cancelled` |
| Agent `issue.block` | `cancelled` | `agent_blocked` |
| Operator moves issue to a non-terminal inactive state | `cancelled` | `issue_state_changed` |
| Reconciliation finds active run for terminal issue | `cancelled` | `canceled_by_reconciliation` |
| Startup finds active DB rows without process ownership | `failed` | `daemon_restarted_run_interrupted` |

Startup may also find `issues.state=Working` with no active `run_attempt`. This is an inconsistent issue state, not a dispatch candidate. The daemon MUST NOT create a new run for it during reconciliation.

### 8.9 Cancellation behavior

Operator run cancellation applies to:

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
issue.state = run_attempt.source_issue_state when source_issue_state is Ready/Rework, unless the issue is already Blocked/Cancelled/Duplicate due to operator transition or agent issue.block
issues.dispatch_paused = true
issues.dispatch_pause_reason = operator_cancelled
issues.dispatch_paused_at = now
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
run_attempt.ended_at = now
if run_attempt.source_issue_state in Ready/Rework and issue is not Blocked/Cancelled/Duplicate due to operator transition or agent issue.block:
  issue.state = run_attempt.source_issue_state
if issue is Blocked/Cancelled/Duplicate due to operator transition or agent issue.block:
  keep current issue state
issues.dispatch_paused = true
issues.dispatch_pause_reason = <code>
issues.dispatch_paused_at = now
run_event = run.failed
system comment with failure summary
```

This state restoration is mandatory. Without it, `dispatch-resume` would leave the issue in `Working`, while the normal scheduler only claims `Ready/Rework`.

When an approval outcome writes `approval_requests.status = auto_denied`, the run failure code (`run_attempt.failure_code`) and `issues.dispatch_pause_reason` MUST use the matching canonical `FailureCode`: `command_denied`, `network_denied`, or `protected_path_denied`.

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
if agent.max_handoff_continuations = 1 and continuation unused:
  send one dedicated handoff continuation in same session/thread
  ↓
if agent.max_handoff_continuations = 0, or still no handoff after allowed continuation:
  run.status = completed_without_handoff
  run.failure_code = missing_handoff
  if run_attempt.source_issue_state in Ready/Rework and issue is not Blocked/Cancelled/Duplicate due to operator transition or agent issue.block:
    issue.state = run_attempt.source_issue_state
  if issue is Blocked/Cancelled/Duplicate due to operator transition or agent issue.block:
    keep current issue state
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
if run_attempt.source_issue_state in Ready/Rework and issue is not Blocked/Cancelled/Duplicate due to operator transition or agent issue.block:
  issue.state = run_attempt.source_issue_state
if issue is Blocked/Cancelled/Duplicate due to operator transition or agent issue.block:
  keep current issue state
issue.dispatch_paused = true
issue.dispatch_pause_reason = review_packet_failed
issue.dispatch_paused_at = now
failure_code = review_packet_failed
```

### 8.14 Run outcome precedence

In this section, active-run-valid states are `Ready`/`Working`/`Rework`.

| Priority | Outcome | Final status/code |
|---:|---|---|
| 1 | Operator run cancel or approval `cancel_run` before finalizer commit | `cancelled/operator_cancelled` |
| 2 | Ordinary operator transition leaves active-run-valid states before finalizer commit | `cancelled/issue_state_changed` |
| 2 | Agent `issue.block` leaves active-run-valid states before finalizer commit | `cancelled/agent_blocked` |
| 2 | Reconciliation finds active run for terminal issue before finalizer commit | `cancelled/canceled_by_reconciliation` |
| 3 | Startup stale active run guard | `failed/daemon_restarted_run_interrupted` |
| 4 | Codex/runner/protocol/workspace/prompt failure | `failed/<canonical code>` |
| 5 | Missing handoff after allowed continuation is exhausted | `completed_without_handoff/missing_handoff` |
| 6 | Handoff exists but review packet fails | `failed/review_packet_failed` |
| 7 | Handoff exists and review packet generated | `completed/null` |

Priority 2 reconciliation outcomes are evaluated only after priority 1, so operator run cancel and approval `cancel_run` MUST keep `operator_cancelled` side effects and MUST NOT be rewritten to reconciliation codes.

Manual `dispatch-pause` / `dispatch-resume` requests are not part of active run outcome precedence; they are rejected with `issue_already_running` while an active run exists.

Once finalizer transaction commits `issue.state=Human Review` and `run.status=completed`, later operator run cancellation must be rejected as not active.

### 8.15 Startup stale-run guard

On startup, before dispatch, run both stale-run scans below in one guarded startup phase. The phase MUST complete before scheduler dispatch can claim new work.

First, scan active `run_attempts.status` rows. For rows owned by previous daemon/process:

```text
status = failed
failure_code = daemon_restarted_run_interrupted
ended_at = now
if run_attempt.source_issue_state in Ready/Rework and issue is not Blocked/Cancelled/Duplicate due to operator transition or agent issue.block:
  issue.state = run_attempt.source_issue_state
  insert issue_state_history from previous issue.state to run_attempt.source_issue_state
if issue is Blocked/Cancelled/Duplicate due to operator transition or agent issue.block:
  keep current issue state
issues.dispatch_paused = true
issues.dispatch_pause_reason = daemon_restarted_run_interrupted
issues.dispatch_paused_at = now
emit system.interrupted or equivalent run event
```

Second, scan `issues.state=Working` where no active `run_attempt` exists for the issue:

```text
do not create, claim, enqueue, or start a run
find the latest run_attempt for the issue with source_issue_state in Ready/Rework, ordered by attempt_no DESC / created_at DESC
if a recoverable source run exists:
  issue.state = latest_run_attempt.source_issue_state
  insert issue_state_history Working -> latest_run_attempt.source_issue_state
  issues.dispatch_paused = true
  issues.dispatch_pause_reason = daemon_restarted_run_interrupted
  issues.dispatch_paused_at = now
  emit system.interrupted or equivalent diagnostic event
  emit issue.state_changed Working -> source_issue_state
if no recoverable source run exists:
  keep issue.state = Working
  issues.dispatch_paused = true
  issues.dispatch_pause_reason = daemon_restarted_run_interrupted
  issues.dispatch_paused_at = now
  emit system.inconsistent_issue or equivalent diagnostic event
  expose diagnostics remediation: operator must use a legal indirect state path, such as Working -> Blocked -> Ready after resolving blockers, or Working -> Cancelled/Duplicate followed by explicit reopen to Inbox/Ready under reopen rules
```

Startup recovery `issue_state_history` rows MUST reflect the actual state restoration from/to values. Their reason/source MAY use `daemon_restarted_run_interrupted` or equivalent startup reconciliation semantics.

This second scan MUST NOT mutate any terminal `run_attempt` status/failure fields, because no active run row exists to interrupt. It only repairs or pauses the inconsistent issue record and emits diagnostics. `dispatch-resume` still MUST NOT change `issue.state`, so a non-recoverable `Working` issue remains blocked from dispatch until an operator performs an explicit valid state transition path. Direct `Working -> Ready`, `Working -> Rework`, `Working -> Human Review`, `Working -> Done`, or direct reopen from `Working` is forbidden.

v1 does not implement crash recovery.

## 9. Workspace and Git

### 9.1 Workspace path

Default:

```text
workspace.root (global workspace root): ~/.symphony/workspaces/
issue workspace path: <workspace.root>/<project_id>/<issue_identifier>/
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

Review packet generation MUST include tracked, deleted, renamed, mode-changed, and untracked files without mutating the real Git index.

Recommended algorithm:

```bash
TMP_INDEX="<artifact-temp-dir>/review.index"
export GIT_INDEX_FILE="$TMP_INDEX"
git read-tree <base_sha>
git add -A -- <workspace-pathspecs>
git diff --cached --binary <base_sha> -- > changes.patch
git diff --cached --name-only <base_sha> -- > changed-files.txt
git diff --cached --numstat <base_sha> -- > diffstat.txt
unset GIT_INDEX_FILE
```

Required guards:

```text
GIT_INDEX_FILE MUST point outside the real repo .git/index.
The real working tree index MUST NOT be staged, reset, cleaned, committed, or mutated.
All paths MUST be workspace-relative in review artifacts.
Absolute paths and path traversal MUST be rejected.
Symlink targets escaping workspace MUST fail review generation.
Protected paths MUST fail review generation with review_packet_failed/protected_path_denied according to source.
Untracked files over artifact_max_bytes, binary files, or files excluded from patch by policy MUST be listed with patch_included=false and a non-empty reason unless binary diff is explicitly allowed by policy.
```

`untracked-files.json` MUST still be written. For each untracked file it MUST include:

```json
{
  "path": "relative/path",
  "size_bytes": 123,
  "sha256": "...",
  "patch_included": true,
  "reason": null
}
```

Patch paths MUST be normalized to Git-style `a/<path>` / `b/<path>` entries.

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

Real Codex adapter support is fixture-gated. Implementation MUST NOT claim a Codex protocol version is supported unless committed fixtures exist under:

```text
internal/agent/codex/testdata/schema/<codex-version>/
internal/agent/codex/testdata/transcripts/<codex-version>/
```

This document package also includes adapter policy docs:

```text
docs/codex/ADAPTER_MAPPING.md
docs/codex/FIXTURE_POLICY.md
```

Startup behavior:

```text
1. detect installed Codex version without launching the long-lived `codex app-server` process
2. resolve committed fixture metadata/static compatibility metadata for that installed Codex version
3. read expected generated protocol/schema version from that committed metadata
4. look up committed fixture for the installed Codex version and expected generated protocol/schema version
5. if no compatible fixture/metadata exists, fail before launching the real Codex process with unsupported_codex_version
6. emit codex.version_checked event
7. only then launch the real adapter
```

The prelaunch fixture gate is based only on the installed Codex version and committed fixture metadata/static compatibility metadata. It MUST NOT depend on starting the real `codex app-server` process to discover the generated protocol/schema version. If the post-launch initialize handshake contradicts the committed compatibility metadata or returns an incompatible schema/protocol shape, the adapter MUST terminate the run through the normal failure path with `codex_protocol_error`.

Default CI MUST use `internal/agent/fake`. Real Codex tests MUST be opt-in through `SYMPHONY_TEST_CODEX=1`.

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
9. if completed without handoff and configured handoff continuation remains available, send one handoff continuation in same thread/session
10. cancel/interrupt on operator run cancel, approval cancel_run, reconciliation, shutdown, timeout, context cancellation
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

Operator `deny` MUST resolve only the current approval action with `approval_requests.status = denied`. It MUST NOT interrupt the run, mark `operator_cancelled`, or pause dispatch by itself. After the decline is written back, the adapter continues from the Codex outcome. If Codex/adapter returns a terminal failure because the declined action cannot proceed, the orchestrator applies normal failure behavior with the matching canonical `FailureCode`, such as `command_denied`, `network_denied`, or `protected_path_denied` for terminal policy denial. Only `cancel_run` applies immediate run cancellation side effects.

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

API errors with `error.code = approval_not_pending` MUST map to CLI exit code 7. Tool Gateway errors with `error.code = handoff_conflict` MUST also map to CLI exit code 7.

`symphony tool` always outputs JSON only. Diagnostics go to stderr.

### 11.2 Operator CLI

```bash
symphony init [--name <name>] [--issue-prefix LOC] [--workflow-template default]
symphony serve [--project <path>] [--host 127.0.0.1] [--port 0] [--open] [--no-open]
symphony open [--project <path>]
symphony status [--json]
```

Init command:

```text
preflight:
  repo_root must resolve to a Git repository
  target project files/directories must be writable
  existing generated files must either match v1 expected content or be left untouched with a clear conflict error
side effects:
  create or validate project DB using db/schema/v1_project.sql
  create or validate app DB registration as needed
  create default WORKFLOW.md from examples/WORKFLOW.default.md when missing
  create required local metadata directories
idempotency:
  rerunning init on an already initialized project returns success no-op when existing files are compatible
failure:
  non-Git repo, permission denied, unsupported DB version, or conflicting existing WORKFLOW.md returns a structured error and no partial mutation beyond already-compatible files
success output:
  print next-step commands for serve/open/create issue
```

Status command:

```text
primary API: GET /api/v1/state
daemon unavailable: CLI MAY fall back to GET /api/v1/health only to report daemon availability; if health is unavailable too, exit 3
human output: concise status for daemon, project, workflow, running runs, pending approvals, Human Review, paused issues, Codex availability, and recent failure summary
--json output: envelope-unwrapped stable object with daemon, project, workflow, running_runs, pending_approvals, human_review, paused_issues, codex, and recent_failures fields
```

`symphony status --json` MUST return the state object directly, not an API envelope. It MUST NOT invent data from diagnostics; unavailable fields from `/api/v1/state` are `null`, empty lists, or `unknown` according to the field type.
`GET /api/v1/state` `AppState` uses these same field names; legacy `active_runs` remains a compatibility alias for `running_runs` count.

Issue commands:

```bash
symphony issue create --title <title> [--description <text>] [--acceptance <item>]... [--priority 1..5] [--label <label>]...
symphony issue list [--state <state>] [--label <label>] [--query <text>] [--paused true|false] [--limit <n>] [--cursor <cursor>] [--sort priority|updated|identifier]
symphony issue show LOC-1
symphony issue update LOC-1 [--title <title>] [--description <text>] [--acceptance <item>]... [--priority 1..5] [--label <label>]...
symphony issue transition LOC-1 <state> [--reason "..."] [--duplicate-of LOC-2]
symphony issue comment LOC-1 --body "..."
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

`symphony issue dispatch LOC-1` and `symphony run LOC-1` call `POST /api/v1/issues/{issue_ref}/dispatch`. On success they print the envelope-unwrapped dispatch data as JSON. On preflight failure they preserve the API error code in JSON diagnostics and use the preflight semantics in TECH_SPEC 8.7 and CLI exit code mapping defined in TECH_SPEC 11.1.

Issue list output MUST preserve the API pagination metadata. Empty lists return success with an empty `items` array and pagination metadata, not an error.

Approval commands:

```bash
symphony approval list
symphony approval decide appr_... --approve-once
symphony approval decide appr_... --approve-for-run
symphony approval decide appr_... --approve-for-session
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

`symphony review path` is a diagnostics command. It MUST call the Review API and print only review packet metadata/path diagnostics such as packet id, packet_no, run_id, artifact kind, artifact_id, stored path, redacted flag, content_url presence/null, and missing/omitted-file diagnostics. It MUST NOT read packet files from the filesystem, MUST NOT print raw packet or artifact bytes, and MUST NOT bypass Review API plus Artifact API redaction/refusal. Disallowed raw prompt, raw prompt context values, raw secrets, and raw Codex logs remain metadata/refusal-only.

`dispatch-pause`, `dispatch-resume`, `send-to-rework`, and `mark-done` require `--reason` to be present and non-empty after trimming. CLI validation failures exit with code 2 before sending the request; API validation failures return `invalid_request`.

Workflow and diagnostics:

```bash
symphony workflow validate
symphony workflow reload
symphony workflow show
symphony diagnostics
symphony diagnostics export
```

`symphony workflow validate` calls `POST /api/v1/workflow/validate` with an empty body or `{"dry_run": true}`. It validates the current filesystem `WORKFLOW.md` only, prints the envelope-unwrapped validation result, and MUST NOT replace the effective config, update last-valid workflow state, render prompts, dispatch runs, or write review artifacts. Candidate workflow validation/rendering belongs to `POST /api/v1/workflow/render-preview`, not this CLI command.

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

Tool Gateway registry names are dot-separated. The agent-facing CLI uses grouped subcommands and MUST map them exactly as follows:

| Registry tool | CLI command | Input schema |
|---|---|---|
| `issue.get` | `symphony tool issue get` | `schemas/tools/issue_get.input.schema.json` |
| `issue.comment` | `symphony tool issue comment --json <file>` | `schemas/tools/issue_comment.input.schema.json` |
| `issue.block` | `symphony tool issue block --json <file>` | `schemas/tools/issue_block.input.schema.json` |
| `artifact.attach` | `symphony tool artifact attach --json <file>` | `schemas/tools/artifact_attach.input.schema.json` |
| `followup.create` | `symphony tool followup create --json <file>` | `schemas/tools/followup_create.input.schema.json` |
| `handoff.submit` | `symphony tool handoff submit --json <file>` 或 `symphony tool handoff submit --json -` | `schemas/tools/handoff_submit.input.schema.json` |

所有 `--json <file>` tool command 都必须支持 `--json -`，并从 stdin 读取 JSON payload，再按同一个 input schema 校验。默认 WORKFLOW prompt 应使用 stdin 提交 `handoff.submit`，避免 agent 在 workspace 根目录创建会被 review packet 当作 untracked content 收集的 `handoff.json` 临时文件。

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

Normative tool input schemas are maintained under `schemas/tools/*.input.schema.json`; `schemas/tool_gateway.schema.json` defines the `{tool,input}` call envelope and embeds the same input constraints for lightweight validation.

`issue.get` input:

```json
{}
```

Returns current issue as `NormalizedIssue` validated against `schemas/normalized_issue.schema.json`. Agent cannot request another issue.

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
protected paths rejected by daemon hard deny
size <= tools.artifact_max_bytes
artifact row path is project-local relative under .symphony/artifacts
```

Protected-path rejection for `artifact.attach` MUST record the tool call as failed and return a tool error to the agent. It MUST NOT create an `approval_requests` row and MUST NOT directly terminate the run; the agent may continue, or the run may later terminate through normal agent failure handling if the task cannot be completed.

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

`target_state` is optional; if omitted, the accepted, persisted, and canonical target_state defaults to `Human Review`; if present it MUST equal `Human Review`. Successful response indicates receipt only:

```json
{
  "success": true,
  "tool": "handoff.submit",
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
| exists | different hash | reject with Tool Gateway error `handoff_conflict` |

Conflict response:

```json
{
  "error": {
    "code": "handoff_conflict",
    "message": "handoff already exists for this run with a different payload hash",
    "details": {
      "run_id": "run_...",
      "handoff_id": "hand_...",
      "existing_payload_hash": "lowercase_sha256_hex",
      "incoming_payload_hash": "lowercase_sha256_hex"
    }
  }
}
```

On `handoff_conflict`, the daemon MUST record the Tool Gateway call as failed, MUST NOT insert or replace the existing handoff, MUST NOT emit a new `handoff.submitted` event, MUST NOT generate a review packet from the conflicting payload, and MUST NOT transition the issue to `Human Review`. The agent-facing CLI command `symphony tool handoff submit` MUST print the JSON error and exit with code 7. If this conflict occurs while the run is still active, the run terminal failure path MUST use `run.failure_code = tool_gateway_failed` unless a higher-precedence cancellation/failure has already won.

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
POST /api/v1/auth/open-token
POST /api/v1/auth/cli-token/rotate
```

Auth bootstrap rules:

```text
1. symphony init creates no browser session and no raw browser token.
2. symphony serve creates or rotates a CLI token for the current OS user when no valid token exists.
3. raw CLI token is written to ~/.symphony/cli-session.json with owner-only permissions where supported.
4. token hash is stored in app DB local_sessions.
5. runtime descriptor never contains secrets.
6. symphony open reads the CLI bearer token and calls POST /api/v1/auth/open-token.
7. serve --open may create an open token in-process after successful daemon startup.
8. React exchanges the one-time open token through POST /api/v1/auth/exchange.
```

Open URL:

```text
http://127.0.0.1:<port>/?open_token=<token>
```

Browser uses HttpOnly SameSite=Lax cookie plus `X-Symphony-CSRF` for command APIs. CLI uses bearer token. Open token is one-time, short TTL, hash-only at rest, and reuse returns `401 unauthorized`.

Unauthenticated access is limited to bootstrap endpoints. `GET /api/v1/health` MAY be unauthenticated. `POST /api/v1/auth/exchange` is unauthenticated at the session layer but MUST require a valid one-time open token. `POST /api/v1/auth/open-token` and `POST /api/v1/auth/cli-token/rotate` require an authenticated local operator credential. All project state, command APIs, SSE streams, artifacts, diagnostics, and Tool Gateway operations require the actor-specific authorization defined in 13.1.

If `~/.symphony/cli-session.json` is missing but a daemon is running, user MUST run an explicit local login/rotate command from the same OS account. Implementation MUST NOT print existing raw tokens from DB because only hashes are stored.

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

Issue responses MUST use `NormalizedIssue` and therefore validate against both the OpenAPI `Issue` schema and `schemas/normalized_issue.schema.json`.

Resource refs:

```text
{issue_ref} accepts internal id iss_... or human identifier LOC-1
{blocker_issue_ref} follows same rule
server resolves refs before auth/state/transaction checks
ambiguous/missing/malformed refs return not_found or invalid_request with no partial mutation
responses always include both id and identifier
```

List contract:

```text
GET /api/v1/issues
query:
  state: optional issue state, repeatable or comma-separated
  label: optional label, repeatable or comma-separated
  q: optional text query against identifier/title/description
  dispatch_paused: optional boolean
  limit: optional integer, default 50, max 200
  cursor: optional opaque cursor returned by previous page
  sort: optional enum priority|updated|identifier, default priority
sort semantics:
  priority = priority ASC, updated_at DESC, identifier ASC
  updated = updated_at DESC, identifier ASC
  identifier = sequence ASC
response: standard success envelope; data shape { items: NormalizedIssue[], page: { limit, next_cursor, has_more } }
empty result: success with items=[] and has_more=false
invalid query/filter/sort/cursor: invalid_request, no mutation
```

Mutation request contracts:

```text
POST /api/v1/issues
request body:
  title: string, required, trim non-empty
  description: string, optional, default ""
  acceptance_criteria: string[], optional, default []
  priority: integer 1..5, optional, default 3
  labels: string[], optional, default []
effect: create Inbox issue only

PATCH /api/v1/issues/{issue_ref}
request body may include title, description, acceptance_criteria, priority, labels
PATCH cannot change state, dispatch pause, workspace, git, run, review, or relation fields
labels are trim-non-empty strings normalized to lowercase and sorted in responses; duplicate labels for one issue are de-duplicated

POST /api/v1/issues/{issue_ref}/comments
request body: { "body": string, trim non-empty }

POST /api/v1/issues/{issue_ref}/transition
request body:
  state: target issue state, required
  reason: string, required and trim non-empty when target is Blocked, Cancelled, or Duplicate
  duplicate_of: optional issue ref, only valid when target is Duplicate
same-state transition: invalid_state_transition with no mutation
Human Review -> Rework/Done MUST use the Review API, not generic transition
Duplicate duplicate_of must resolve within the same project and must not equal the current issue
duplicate relation creation is idempotent for the same active pair

POST /api/v1/issues/{issue_ref}/blockers
request body: { "blocked_by": issue_ref }
DELETE /api/v1/issues/{issue_ref}/blockers/{blocker_issue_ref}
blocker refs must resolve within the same project and must not equal the current issue
removing an existing blocker relation soft-deactivates it; removing an already inactive relation is success no-op
```

All invalid mutation request bodies return `invalid_request`; invalid state/guard failures return the state-specific ApiErrorCode. Failed mutations MUST be transactionally no-op.

Rules:

```text
PATCH cannot change state
state changes use /transition
dispatch uses /dispatch
blockers use blocker command endpoints
transition leaving Ready/Working/Rework with active run enqueues reconciliation cancel and returns side_effects metadata
```

Manual dispatch contract:

```text
POST /api/v1/issues/{issue_ref}/dispatch
request body: empty object or omitted
transaction: submit `DispatchIssue` for the resolved issue_ref and execute the shared preflight/claim transaction from 8.7
success: claim was created and worker launch may continue asynchronously; this is not run completion
response: standard success envelope; data shape { issue: NormalizedIssue, run_attempt, side_effects }
failure: return the 8.7 preflight `ApiErrorCode`; do not create run_attempt, do not change issue.state, do not launch workspace/token/process/prompt side effects
```

Dispatch pause/resume contract:

```text
POST /api/v1/issues/{issue_ref}/dispatch-pause
request body: { "reason": string, non-empty after trimming }
allowed states: no active run, and any non-terminal issue state
missing/blank reason is rejected with invalid_request and no mutation
terminal states Done/Cancelled/Duplicate are rejected with invalid_state_transition; reopen/transition first if dispatch control is needed
active run exists is rejected with issue_already_running and no mutation
transaction:
  if issues.dispatch_paused = true: return success no-op, keep existing dispatch_pause_reason/dispatch_paused_at, and do not append a duplicate system event or issue comment
  set issues.dispatch_paused = true
  set issues.dispatch_pause_reason = request.reason
  set issues.dispatch_paused_at = now
  append system event and issue comment with the operator reason
response: IssueDispatchControlEnvelope; data shape { issue: NormalizedIssue, side_effects } when comments/events are created

POST /api/v1/issues/{issue_ref}/dispatch-resume
request body: { "reason": string, non-empty after trimming }
allowed states: no active run, and any non-terminal issue state
missing/blank reason is rejected with invalid_request and no mutation
terminal states Done/Cancelled/Duplicate are rejected with invalid_state_transition; reopen/transition first if dispatch control is needed
active run exists is rejected with issue_already_running and no mutation
transaction:
  if issues.dispatch_paused = false: return success no-op and do not append a system event or issue comment
  clear issues.dispatch_paused
  clear issues.dispatch_pause_reason
  clear issues.dispatch_paused_at
  append system event and issue comment with the operator reason
must not change issue.state
must not edit title/description/acceptance criteria/labels/priority/workspace/git fields
must not add/remove blockers or auto-transition Blocked to Ready/Rework
must not create, claim, enqueue, or start a run
response: IssueDispatchControlEnvelope; data shape { issue: NormalizedIssue, side_effects } when comments/events are created
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

Only pending approvals can be decided. `deny` declines the current approval action and records `approval_requests.status = denied`; it does not cancel the run or apply `operator_cancelled` side effects. `cancel_run` cancels the run and applies `operator_cancelled` side effects. Approval responses must expose `requested_at`, `timeout_ms`, `expires_at`, `resolved_at`.

If a pending approval reaches `expires_at` while the run is waiting, the adapter/orchestrator MUST mark that approval resolved as expired or timed out, fail the run with `failure_code=approval_timeout`, restore the issue to `run_attempt.source_issue_state` when applicable, set `issues.dispatch_paused=true`, set `issues.dispatch_pause_reason=approval_timeout`, and set `issues.dispatch_paused_at=now`. Expired approvals MUST no longer be decidable; later decide attempts return `approval_not_pending` with no mutation.

`GET /api/v1/approvals` and approval decision responses MUST expose `risk_level`, `policy_match`, and `action_summary` for every approval row. If the storage layer keeps approval payloads as opaque `request_json`, the API handler MUST derive these fields from `request_json` and policy evaluation before returning the response. `action_summary` is the stable Dashboard display string and MUST NOT require the UI to parse opaque request JSON.

If the addressed approval is not currently `pending`, `POST /api/v1/approvals/{approval_id}/decide` MUST return `409 Conflict` with `error.code = approval_not_pending`. The request MUST be transactionally read-only: it MUST NOT update `approval_requests.status`, `decision_json`, `resolved_at`, or any run/issue state; MUST NOT write a new decision; MUST NOT write back to Codex; MUST NOT cancel or pause a run; and MUST NOT emit approval decision or cancellation side effects. The CLI MUST preserve `approval_not_pending` in JSON diagnostics and exit with code 7.

### 12.8 Review API

```http
GET  /api/v1/reviews/{issue_ref}
POST /api/v1/reviews/{issue_ref}/send-to-rework
POST /api/v1/reviews/{issue_ref}/mark-done
```

`GET /api/v1/reviews/{issue_ref}` returns the latest REST `ReviewPacketSummary`.
The summary MUST include an `artifacts[]` list for review packet files the dashboard may display:

```text
kind
path
artifact_id
redacted
content_url
```

`kind` identifies the packet file role, for example `review_md`, `review_json`, `patch`, `changed_files`, `untracked_files`, `diffstat`, `test_output`, `tool_calls`, `approvals`, and prompt metadata. `artifact_id` references the Artifact API metadata row. `content_url` is the relative content endpoint for allowed content, usually `/api/v1/artifacts/{artifact_id}/content`; it is `null` when content is unavailable or must not be served. Raw prompt content, raw prompt context values, raw secrets, and raw Codex logs MUST be represented only as metadata/refusal entries with `content_url=null`. Path fields on the review packet are diagnostics/metadata only. Dashboard MUST NOT read review packet files from the filesystem.

The Review Packet page obtains artifact ids from this API, then fetches file contents through the Artifact API as needed. File-level `review.json` is the Review Packet schema source of truth for summary, tests, risks, verification, changed files, tool calls, and approvals, but it is not the REST `ReviewPacketSummary` schema. `review.md`, `changes.patch`, and other packet files are fetched by `artifact_id` for rendered review and detail panes. Review API MUST NOT expose raw prompt/log bytes inline or provide a `content_url` that bypasses Artifact API redaction and refusal rules.

The CLI `symphony review path` command uses this endpoint for metadata/path diagnostics only. Its output MUST NOT include packet file contents, raw artifact bytes, raw prompt content, raw prompt context values, raw secrets, or raw Codex logs.

Review action request bodies:

```text
POST /api/v1/reviews/{issue_ref}/send-to-rework
request body: { "reason": non-empty string }

POST /api/v1/reviews/{issue_ref}/mark-done
request body: { "reason": non-empty string }
```

For Send to Rework, UI labels MAY call `request.reason` feedback. For Mark Done, UI labels MAY call `request.reason` comment. API and CLI request fields are always `reason`, and `request.reason` MUST be non-empty after trimming. Transactions may persist it as an operator comment/event payload, but that persisted comment is not a request field.

Mark Done guards:

```text
issue.state = Human Review
latest review_packet.status = generated
review_packet.run_id belongs to latest completed handoff run
no active run
request.reason is non-empty after trimming
```

In review action guards, latest review packet MUST be selected by the issue's latest packet row (highest `packet_no`). That latest packet MUST have `status=generated`, and its `run_id` MUST belong to the latest completed handoff run. A mismatched latest review packet means the latest packet does not match the latest completed handoff run. Guards MUST NOT search for an earlier `generated` packet to bypass a newer `failed` or `partial` packet.

Mark Done transaction:

```text
validate guards in one transaction
set issues.state = Done
set issues.completed_at = now
insert issue_state_history Human Review → Done
insert operator comment with request.reason
append review.marked_done event
append issue.completed event
preserve workspace row, branch, base_sha, run_attempts, handoffs, and review_packets
do not commit, push, merge, create PR, delete workspace, or mutate review packet files
```

Mark Done error semantics:

```text
missing/blank reason -> invalid_request, no mutation
issue not in Human Review -> invalid_state_transition, no mutation
missing/non-generated/mismatched latest review packet -> review_packet_required, no mutation
active run exists -> issue_already_running, no mutation
```

### 12.9 Artifact API

```http
GET /api/v1/artifacts/{artifact_id}
GET /api/v1/artifacts/{artifact_id}/content
```

Review Packet callers use `artifact_id` values returned by the Review API. `GET /api/v1/artifacts/{artifact_id}` returns metadata; `GET /api/v1/artifacts/{artifact_id}/content` returns bytes only when allowed.

Content access MUST enforce containment under `.symphony/artifacts` or `.symphony/exports`. v1 MUST reject raw prompt, raw prompt context values, raw secrets, and raw Codex log content access. Redacted or disallowed artifacts may still appear in metadata and review packet artifact lists, but their Review API `content_url` MUST be `null` when content must not be served, and direct content access MUST return the existing refusal/error response rather than bypassing containment or redaction policy.

### 12.10 Workflow and diagnostics API

```http
GET  /api/v1/workflow
POST /api/v1/workflow/validate
POST /api/v1/workflow/render-preview
POST /api/v1/workflow/reload
GET  /api/v1/diagnostics
POST /api/v1/diagnostics/export
```

`dry_run=true` validates without replacing effective config. Invalid reload preserves last valid config. If no valid config exists, dispatch is blocked.

Workflow validate contract:

```text
POST /api/v1/workflow/validate
request body:
  empty object, omitted body, or { dry_run: true }
response data:
  source: current_filesystem
  workflow_path: path to the validated WORKFLOW.md
  validation: { valid: boolean, warnings: [], errors: [] }
  side_effects:
    effective_config_replaced: false
    last_valid_config_updated: false
    prompt_rendered: false
    run_dispatched: false
    review_artifacts_written: false
```

`/workflow/validate` validates the current filesystem `WORKFLOW.md` at request time. It does not validate candidate input and MUST reject `candidate_workflow_md`, `candidate_config`, `render_context`, unknown fields, malformed JSON, or `dry_run=false` with `invalid_request`. Validation failures in the current file are successful HTTP responses with `validation.valid=false` and populated `errors`; they MUST NOT replace the effective config or last-valid config.

Render preview contract:

```text
POST /api/v1/workflow/render-preview
request body:
  source: effective|candidate (optional; default effective)
  candidate_workflow_md: optional string
  candidate_config: optional object
  render_context: optional object
response data:
  source: effective|candidate
  rendered_prompt_preview: redacted string|null
  validation: { valid: boolean, warnings: [], errors: [] }
  redactions_applied: []
```

`source` defaults to `effective`. `source=effective` or an empty body renders from the current/latest valid workflow. If no current/latest valid workflow config and prompt body exist, the endpoint MUST fail with `workflow_invalid` rather than returning an empty successful preview; this means the system has no effective workflow to render, distinct from candidate validation failure.

`source=candidate` requires at least one of `candidate_workflow_md` or `candidate_config`. If neither is provided, the endpoint MUST return `workflow_validation_failed`. When both are provided, the parsed front matter/config and body from `candidate_workflow_md` form the base candidate; `candidate_config` overrides only config fields, and the body remains the body from `candidate_workflow_md`. When only `candidate_config` is provided, the endpoint uses the latest valid prompt body as the body and applies `candidate_config` as config overrides. If no latest valid prompt body exists for that case, the endpoint MUST return `workflow_validation_failed`.

This endpoint is dry-run and redacted-only: it MUST NOT replace the effective config, update the last valid config, dispatch a run, write review artifacts, persist rendered prompt content, or return raw prompt/log/artifact content.

The response MUST apply the same prompt/log/artifact redaction policy used elsewhere. It MUST return only a redacted rendered prompt preview plus validation warnings/errors, and MUST NOT expose raw secrets, raw environment values, raw prompt artifacts, or raw Codex logs.

Diagnostics export is redacted only. `include_raw_logs=true` returns `raw_log_access_not_supported`.

### 12.11 Excluded APIs

Do not expose:

```http
POST /api/v1/git/:issue_ref/push
POST /api/v1/git/:issue_ref/publish
POST /api/v1/git/:issue_ref/pr
POST /api/v1/git/:issue_ref/create-pr
POST /api/v1/db/backup
POST /api/v1/db/restore
POST /api/v1/db/migrate
GET  /api/v1/audit
POST /api/v1/workspaces/:issue_ref/delete
POST /api/v1/workspaces/:issue_ref/reset
POST /api/v1/workspaces/:issue_ref/clean
POST /api/v1/workspaces/:issue_ref/rebase
POST /api/v1/workspace-delete
POST /api/v1/secrets
PATCH /api/v1/secrets/*
PATCH /api/v1/projects/:project_id/settings
DELETE /api/v1/issues/:issue_ref
PATCH /api/v1/state/*
```

The Excluded APIs list is the executable guard for the PRD forbidden surface. It MUST also reject aliases or hidden/future routes for publish/PR/create-pr, backup/restore, migrate, audit, workspace delete/reset/clean/rebase, secret or project settings mutation, issue delete, arbitrary state mutation, remote dashboard control, RBAC/admin management, or desktop shell backend bypass.

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
concurrency_limit_reached
workspace_conflict
workspace_prepare_failed
after_create_failed
before_run_failed
codex_startup_failed
unsupported_codex_version
codex_protocol_error
turn_timeout
stall_timeout
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
daemon_restarted_run_interrupted
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
hard daemon enforcement: API auth, CSRF, Tool Gateway token/scope/cwd/schema/registry, artifact.attach protected-path rejection, raw secret/content refusal, artifact/export containment, redacted-only diagnostics export
Codex-mediated enforcement: shell command approvals, file change approvals, network approvals, Codex-surfaced protected-path read/write approvals or denies
detection/diagnostic only: events observed after a command already executed
```

Do not describe Codex-mediated network deny, protected-path file access deny, or command deny as OS-level isolation in v1. Daemon/API hard checks are application-level enforcement, not a full filesystem sandbox or packet firewall.

Protected-path denial has two distinct v1 semantics: Codex-mediated protected-path read/write deny follows the approval auto-deny failure path; Tool Gateway `artifact.attach` protected-path rejection is daemon hard enforcement for that tool call only.

### 13.1.1 v1 authorization matrix

v1 has no multi-tenant RBAC. Authorization is fixed by local actor class and credential type:

| Actor / entrypoint | Accepted credential | Authority | Explicitly not allowed |
|---|---|---|---|
| local operator browser | loopback `symphony_session` cookie plus `X-Symphony-CSRF` for command APIs | Full operator command authority over the local project through REST/SSE. | No direct SQLite, Git, filesystem, Codex, or Tool Gateway access from the dashboard. |
| operator CLI | CLI bearer token from `~/.symphony/cli-session.json` | Same full operator command authority as authenticated browser for normal `symphony ...` REST commands. | No unauthenticated command execution; no Tool Gateway authority unless invoking `symphony tool ...` with a run-scoped tool token. |
| future desktop shell | authenticated local operator session or equivalent local token | Not implemented in v1; if later added as a local UI wrapper, it has the same operator command authority and backend checks as browser/CLI. | No bypass around REST auth/CSRF, policy checks, or backend containment rules. |
| agent Tool Gateway | run-scoped tool token with `project_id`, `issue_id`, `run_id`, `workspace_path`, `allowed_tools`, and expiry | Only the fixed Tool Gateway registry entries allowed for the current run scope. | No REST `/api/v1` operator command APIs, no arbitrary tools outside registry, no Done transition, no cross-run/issue/workspace access. |
| unauthenticated | none, invalid, expired, or wrong token | Bootstrap only: unauthenticated health and one-time open-token exchange when the presented open token is valid. | No project state, commands, SSE streams, artifacts, diagnostics, dashboard session APIs requiring a session, CLI rotation/open-token APIs, or Tool Gateway calls. |

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
created by symphony open or serve --open
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

Tool tokens do not create browser sessions, CLI bearer authority, or operator command authority. They authorize only the fixed Tool Gateway registry entries allowed by the token scope for the current run.

Revoke on:

```text
run terminal outcome
operator run cancel
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

v1 command classification MUST be layered:

```text
1. command verb/prefix classification
2. argument and path extraction where feasible
3. protected-path override
4. network-policy override
5. final allow/review/deny decision
```

Protected-path override wins over generic allow. For example, `cat .env`, `grep -R AWS_SECRET_ACCESS_KEY .`, and `find . -name "*.pem" -exec cat {} \;` MUST NOT be allowed merely because `cat`, `grep`, or `find` appeared in a broad allow list.

Default allow:

```text
git status
git diff
git log
rg with protected-path exclusion applied
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
symphony tool handoff submit
```

Default review:

```text
cat
grep
find
ls
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

`symphony tool ...` is allowed only as the shell entrypoint; operation authority still comes from Tool Gateway token/scope/cwd/schema/registry checks.

v1 uses pattern/prefix classification plus path/protected-path extraction, not deep supply-chain analysis.

Policy outcomes mapping for Codex-mediated policy bridge outcomes:

| Policy outcome | Approval row | Terminal failure code |
|---|---|---|
| command auto-denied, or terminal command denial after operator decline | `auto_denied`, or `denied` plus terminal runner failure | `command_denied` |
| network auto-denied, or terminal network denial after operator decline | `auto_denied`, or `denied` plus terminal runner failure | `network_denied` |
| protected path read/write auto-denied, or terminal protected-path denial after operator decline | `auto_denied`, or `denied` plus terminal runner failure | `protected_path_denied` |

Codex-mediated security auto-deny MUST terminate the current run in v1. Operator `deny` MUST decline only the current approval action and write `approval_requests.status = denied`; it MUST NOT cancel the process group, mark `operator_cancelled`, revoke run tokens, or pause dispatch by itself. After the decline, the adapter MUST continue reading Codex until Codex continues or returns a terminal outcome. If the declined action causes terminal policy failure, the run MUST use the matching canonical failure code above and pause through the normal failure path. Operators must use `cancel_run` when they intend to stop the whole run immediately. This mapping does not apply to daemon hard-denied Tool Gateway calls such as `artifact.attach` protected-path rejection.

### 13.5 Network policy

Default:

```text
network.default = deny
allowlist = []
```

Network policy is evaluated before `approvals.mode` fallback behavior. `approvals.mode = balanced` does not convert `network.default = deny` into review.

Decision table:

| Match | `network.default` | Outcome | Approval row / Inbox |
|---|---|---|---|
| Request matches `allowlist` | any | allow | no pending Approval Inbox item |
| Request does not match `allowlist` | `deny` | auto-deny and terminate current run with `network_denied` | `auto_denied`; not operator-actionable |
| Request does not match `allowlist` | `review` | create pending operator decision | pending Approval Inbox item |

Therefore the default config (`approvals.mode = balanced`, `network.default = deny`, empty `allowlist`) MUST auto-deny unknown network requests. Network requests enter Approval Inbox only when the network policy explicitly returns review, for example `network.default = review` or a future more-specific review rule.

v1 does not implement packet firewall, egress accounting, or dependency-origin attribution.

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
Codex-mediated write protected path → auto-deny approval row, terminate run, failure_code=protected_path_denied, pause dispatch
Codex-mediated read protected path → deny or approval according to policy mode; default deny for known secret patterns; deny uses the same protected_path_denied terminal path
Tool Gateway artifact.attach protected path → daemon hard deny tool call, failed tool_call + tool error, no approval row, no direct run termination
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

Preserve safe metadata such as hash, length category, field name, and safe summary. Prompt snapshot artifacts, including `prompt/context.json` and `prompt/rendered_prompt.redacted.md`, MUST contain only redacted content or safe metadata; they MUST NOT contain raw secrets, raw rendered prompt content, raw prompt context values, or raw Codex logs. Redaction is best effort and not compliance-grade.

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
protected path read/write denied creates approval auto_denied, terminates run, sets protected_path_denied, pauses dispatch
artifact.attach protected path denied creates failed tool_call and tool error, without approval row or direct run termination
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
diffstat.txt
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

For the file-existence gate, only these critical files block `review_packets.status=generated`. A `generated` row MUST NOT point to missing critical files. Non-critical files MAY be absent without preventing `generated`, but each omission MUST be recorded in review metadata or diagnostics with `path`, `reason`, and `generation_phase`. Silent omission is forbidden. Critical file presence is necessary but not sufficient: the packet MUST also pass the Review Packet schema, artifact registration, security, redaction, generation, and finalizer gates before it may be marked `generated`.

Prompt snapshot files are redacted review artifacts, not raw prompt archives. `prompt/context.json`, `prompt/rendered_prompt.redacted.md`, `prompt/prompt_meta.json`, and `prompt/tool_manifest.md` MUST follow 13.7 and 12.9: safe metadata/redacted content only; raw prompt, raw context values, raw secrets, and raw Codex logs are disallowed content.

### 14.3 Generation sequence

```text
1. ensure artifact dir exists
2. validate workspace containment and protected-path policy
3. create temporary Git index outside the real .git/index
4. read base tree into temporary index
5. add current workspace contents to temporary index
6. generate changes.patch with git diff --cached --binary
7. generate changed-files.txt with git diff --cached --name-only
8. generate diffstat.txt with git diff --cached --numstat
9. generate untracked-files.json, even when empty
10. export test-output.txt from after_run hook/test output summary or diagnostics capture
11. export agent-final-message.md from runner/adapter final message capture
12. export commands.jsonl from command/tool execution log and approval/command policy events
13. read handoff
14. export tool calls
15. export approvals
16. export redacted run events
17. copy/generate prompt snapshot files as redacted content or safe metadata only; never copy raw prompt, raw prompt context values, raw secrets, or raw Codex logs
18. write review.json
19. write review.md
20. insert artifacts rows
21. insert immutable review_packets row
22. emit review.packet_generated
```

If any non-critical file from 14.2 cannot be produced during its corresponding generation step/phase, including steps 8, 10, 11, 12, and 14-17, the generator MUST keep the packet eligible for `status=generated` only after recording that omission in review metadata or diagnostics with the file path, reason, and failed generation step/phase. If any critical file is missing, the generator MUST NOT insert a `generated` review packet.

Artifact kinds:

| File | artifact.kind | review_packets column |
|---|---|---|
| `review.md` / `review.json` | `review_packet` | `review_md_path` / `review_json_path` |
| `changes.patch` | `patch` | `patch_path` |
| `changed-files.txt` | `changed_files` | `changed_files_path` |
| `untracked-files.json` | `untracked_files` | `untracked_files_path` |
| `diffstat.txt` | `diffstat` | `diffstat_path` |
| `prompt/*` | `prompt_snapshot` | `prompt_snapshot_id` via `prompt_snapshots` |

`review_packets` MUST be immutable. Rework creates a new packet with `packet_no = previous packet_no + 1` for the issue and `UNIQUE(run_id)`.

### 14.4 review.json shape

`review.json` is a file-level review packet document and MUST validate against `schemas/review_packet.schema.json`. It is not the same shape as the REST `ReviewPacketSummary`.

It is the structured source of truth for the Review Packet and MUST cover issue, run, git, files, handoff, changed_files, untracked_files, approvals, tool_calls, prompt_snapshot, and failure metadata.

```json
{
  "id": "rp_...",
  "packet_no": 1,
  "status": "generated",
  "issue": {
    "id": "iss_...",
    "identifier": "LOC-1",
    "title": "...",
    "acceptance_criteria": []
  },
  "run": {
    "id": "run_...",
    "status": "completed",
    "source_issue_state": "Ready"
  },
  "git": {
    "branch_name": "symphony/LOC-1-...",
    "base_ref": "origin/main",
    "base_ref_config": "auto",
    "base_sha": "...",
    "head_sha": "...",
    "dirty": true
  },
  "files": {
    "review_md_path": "review.md",
    "review_json_path": "review.json",
    "patch_path": "changes.patch",
    "changed_files_path": "changed-files.txt",
    "untracked_files_path": "untracked-files.json",
    "diffstat_path": "diffstat.txt"
  },
  "handoff": {
    "summary": "...",
    "tests": [],
    "risks": [],
    "verification": [],
    "followups": [],
    "target_state": "Human Review"
  },
  "changed_files": [],
  "untracked_files": [],
  "approvals": [],
  "tool_calls": [],
  "prompt_snapshot": {
    "id": "ps_...",
    "rendered_prompt_hash": "...",
    "tool_manifest_path": "prompt/tool_manifest.md"
  },
  "failure_code": null,
  "failure_message": null,
  "created_at": "2026-05-11T00:00:00Z"
}
```

The file-level `review.json` handoff object MUST include the accepted/canonical `followups` array and `target_state: "Human Review"`. `changed_files` is represented by the top-level `changed_files` field and is not duplicated as a required handoff field in the Review Packet schema.

### 14.5 review.md sections

`review.md` MUST contain these fixed sections:

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

A review packet with untracked files is `generated` only if every untracked file is listed in `changed-files.txt` and `untracked-files.json`. Untracked file contents SHOULD be represented in `changes.patch`; when omitted because of size, binary content, or policy limits, `patch_included` MUST be `false` and `reason` MUST explain the omission in review metadata.

`untracked-files.json` shape:

```json
[
  {
    "path": "src/new-file.ts",
    "size_bytes": 1234,
    "sha256": "...",
    "patch_included": true,
    "reason": null
  }
]
```

Protected paths, path traversal, or files outside workspace MUST fail review generation with `review_packet_failed`; they must not be silently omitted.

### 14.7 Human Review transition

Finalizer transitions issue to `Human Review` only when:

```text
handoff exists for run
handoff.target_state = Human Review, using the canonical default when omitted
after_run attempted if workspace exists
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
operator supplies non-empty reason
```

`reason` MUST be non-empty after trimming. UI may present Mark Done `reason` as comment. The transaction may persist it as an operator comment/event payload, but API and CLI input remains `reason`. The latest review packet MUST be selected by the issue's latest packet row (highest `packet_no`). That latest packet MUST have `status=generated`, and its `run_id` MUST belong to the latest completed handoff run. A mismatched latest review packet means the latest packet does not match the latest completed handoff run. Mark Done MUST NOT search for an earlier `generated` packet to bypass a newer `failed` or `partial` packet.

Partial/failed review packets can be viewed but cannot Mark Done.

Successful `review mark-done` is a single transaction:

```text
issue.state = Done
issue.completed_at = now
insert issue_state_history Human Review → Done
insert operator comment with reason
emit review.marked_done
emit issue.completed
keep same workspace, branch, base_sha, handoffs, and review packets
do not commit, push, merge, create PR, delete workspace, or rewrite review packet artifacts
```

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
review_packet.run_id belongs to latest completed handoff run
no active run
operator supplies non-empty reason
```

UI labels MAY call Send to Rework `reason` feedback, but API and CLI fields are `reason`, and the value MUST be non-empty after trimming. The latest review packet MUST be selected by the issue's latest packet row (highest `packet_no`). That latest packet MUST have `status=generated`, and its `run_id` MUST belong to the latest completed handoff run. A mismatched latest review packet means the latest packet does not match the latest completed handoff run. Send to Rework MUST NOT search for an earlier `generated` packet to bypass a newer `failed` or `partial` packet.

Send-to-rework error semantics match Mark Done for shared guards: invalid state returns `invalid_state_transition`; missing/blank reason returns `invalid_request`; missing, non-generated, or mismatched latest review packet returns `review_packet_required`; active run returns `issue_already_running`. All failures are no mutation.

Side effects:

```text
issue.state = Rework
issues.dispatch_paused = false
clear dispatch pause reason/timestamp
insert issue_state_history Human Review → Rework
insert operator comment with reason
emit review.sent_to_rework
```

Dispatch from Rework:

```text
run_attempt.source_issue_state = Rework
dispatch_reason = scheduler or manual according to trigger
same workspace row reused
same branch reused
same base_sha retained
before_run hook runs
prompt includes latest review reason and previous review packet summary
```

The `previous review packet summary` included in a Rework prompt MUST be the minimal safe summary below:

```text
packet_no
run_id
handoff.summary
changed_files
tests
risks
verification
followups
```

These fields MUST come only from redacted/safe review metadata and accepted handoff metadata. The prompt MUST NOT include raw packet file contents, raw prompt content, raw prompt context values, raw secrets, or raw Codex logs.

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

Authenticated dashboard users are local operators with the authorization defined in 13.1.1. Dashboard UI affordances MUST NOT imply per-user roles, tenant scopes, or permissions that v1 does not enforce.

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

Board actions:

```text
create issue
transition issue
dispatch eligible issue
open Issue Detail
open Run Detail
open Review Packet
```

Board actions MUST call REST command APIs. The dashboard MUST NOT directly read or write backend resources such as SQLite, Git, filesystem, Codex, or Tool Gateway.

Issue Detail shows issue facts, comments, blockers, workspace, run history, review packets, and dispatch paused state. In states allowed by the Issue API dispatch control guards, it MUST expose dispatch pause when not paused and dispatch resume when paused. These actions MUST call `POST /api/v1/issues/{issue_ref}/dispatch-pause` and `POST /api/v1/issues/{issue_ref}/dispatch-resume`, and MUST honor active-run, terminal-state, and state guards instead of mutating state locally.

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

Approval Inbox shows command/file/network approvals, action summary, risk level, policy match, and approve/deny/cancel controls. The UI MUST render `action_summary` from the Approval API and MUST NOT parse opaque approval request JSON for stable display text. The UI MUST support all Approval API decisions from 12.7: `approve_once`, `approve_for_run`, `approve_for_session`, `deny`, and `cancel_run`. UI control mapping MUST be explicit: approve once -> `approve_once`; approve for run -> `approve_for_run`; approve for session -> `approve_for_session`; deny current action -> `deny`; cancel run -> `cancel_run`. If approve actions are grouped in a menu or segmented control, all three approval scopes MUST remain separately selectable; a single generic approve control MUST NOT silently collapse them. The UI MUST distinguish `deny` as declining the current approval action from `cancel_run` as cancelling the whole run.

Review Packet page shows summary, acceptance criteria, handoff, changed files, diff, tests, risks, verification, approvals, tool calls, git, How to Continue, Send to Rework, and Mark Done. It MUST treat `review.json` as the structured source of truth, load the latest packet through `GET /api/v1/reviews/{issue_ref}`, use returned `artifact_id`/`content_url` entries to fetch contents through the Artifact API, and MUST NOT read packet files directly from the filesystem.

Workflow page shows current validation, last valid config, warnings/errors, reload, and render preview. The validate action MUST call `POST /api/v1/workflow/validate` with an empty body or `{"dry_run": true}` and display the returned current-filesystem validation result without implying that effective config changed. Render preview MUST call `POST /api/v1/workflow/render-preview` and display only the redacted preview and validation warnings/errors returned by that API.

Diagnostics page shows daemon, project paths, Codex, Git, DB, workflow, and redacted export.

All pages MUST implement these shared states:

```text
loading: visible while initial query, command mutation, artifact fetch, or SSE reconnect is in progress
empty: visible for empty issue lists, no pending approvals, no review packet, no run history, and no diagnostics export
auth error: 401/403/CSRF/session expired shows local re-auth/open instructions and does not retry command mutations silently
daemon unavailable: Overview/status surfaces unavailable daemon state and suggests `symphony serve`
artifact refusal: raw prompt, raw Codex log, raw secret, or other disallowed content shows metadata/refusal only
command error: display API error.code and stable message; never infer mutation success before API/SSE confirmation
```

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
unsupported DB version remediation when schema guard fails
workflow validation and last valid config metadata
daemon pid/uptime/runtime descriptor
Codex availability/version/support status
Git repo/worktree status
redaction enabled state
warnings
inconsistent issues, including Working issues with no active run and required operator remediation
```

Diagnostics export is redacted-only. `include_raw_logs=true` MUST return `raw_log_access_not_supported`.

When DB schema version is unsupported, diagnostics and CLI errors MUST be read-only and include the detected version, expected v1 version, affected DB path, and operator guidance to use a compatible binary, restore a manual backup, or initialize a new project DB. v1 MUST NOT attempt automatic migration, rollback, or backup/restore.

## 17. Upstream-vs-Local resolution table

| Topic | Local v1 implementation rule | Required test |
|---|---|---|
| Tracker | `tracker.kind: local`; no Linear API surface. | Issue CRUD and dispatch without Linear config. |
| Reconciliation | Active run leaving `Ready/Working/Rework` is cancelled, dispatch is paused with the reconciliation `failure_code`, and workspace is retained. | Transition active Working issue to inactive state; then `Blocked -> Ready` remains idle until operator `dispatch-resume`. |
| Operator run cancel | `cancelled/operator_cancelled`, tool tokens revoked, dispatch paused. | Next tick does not redispatch until resume. |
| Retry | No automatic retry timers/queue. | Failure pauses and stays idle after waiting. |
| Continuation | Default is one main turn plus one handoff continuation; config may set zero continuations. | Default: first missing handoff continues, missing after continuation pauses. With `max_handoff_continuations=0`, first missing handoff pauses. |
| Hooks | `after_create`, then `before_run` on first run; `before_run` on every run; `after_run` finally. | Hook order tests. |
| NormalizedIssue | Keep top-level git/workspace aliases. | `issue.branch_name` and `git.branch_name` both render. |
| Workspace key | Replace invalid chars with `-`; max 80. | Slash/space/unicode/long text tests. |
| Handoff target | Only `Human Review`; submit does not transition. | `agent.handoff_state: Done` fails validation. |
| Review finalizer | Review packet success required for Human Review; untracked files included. | Review failure blocks Human Review. |
| Terminal run states | Compact status + canonical failure_code. | Timeout/stall/cancel/reconcile code tests. |
| Cleanup | Never auto delete/reset/clean workspaces. | Terminal issue keeps workspace. |
| Tool CLI policy | `symphony tool ...` allowed but gateway-authorized. | Wrong token denied. |
| Issue refs | Path refs accept `iss_...` and `LOC-...`. | Both return same issue. |
| Codex fixtures | Unsupported version fails before launching the real Codex process, records `unsupported_codex_version`, and pauses dispatch. | Unsupported fake version test. |
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
go test ./internal/security/...
go test ./internal/e2e -run TestSecurityRegression
```

The default security regression commands MUST use local fakes/fixtures only. They MUST NOT require real Codex, external network access, or `SYMPHONY_TEST_CODEX=1`.

The default security regression suite MUST cover at least the PRD 13 security acceptance scenarios:

```text
protected path read/write denial
Tool Gateway artifact.attach protected-path denial
artifact containment, traversal, and symlink escape
redaction fixtures and raw prompt/raw Codex log/raw secret API refusal
loopback origin, session token, CSRF token, CLI token, and tool token rejection cases
command allow/review/deny policy classifications
network denied fake request and unknown-network auto-deny
Codex-mediated command/network/protected-path auto-deny writes approval auto_denied, terminates run, sets canonical failure_code, and pauses dispatch
network policy review path enters Approval Inbox
artifact.attach protected-path denial records failed tool_call + tool error without approval row or direct run termination
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
default WORKFLOW prompt contract golden rendering
path normalization
branch naming
workspace key sanitization
command policy
protected path matching
redaction
split ApiErrorCode vs FailureCode mapping
agent turn-count/handoff constraints
handoff canonical payload hashing
handoff payload hash conflict returns Tool Gateway `handoff_conflict`, CLI exit 7, and does not advance Human Review
run outcome precedence and finalizer/cancel race handling
```

### 18.4 Integration tests

Required coverage:

```text
SQLite schema init
init non-Git repo / permission denied / conflicting WORKFLOW.md fails without partial incompatible state
unsupported DB version refusal
foreign key enforcement
issue create transaction
issue sequence concurrent allocation
issue list filtering, sorting, pagination, empty result, and invalid query behavior
attempt_no monotonic allocation
blocker eligibility query
worktree create/reuse
hook lifecycle
Tool Gateway token validation
handoff.submit persists handoff/tool_call/event but does not create an implicit issue comment
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
missing handoff after allowed continuation → dispatch_paused
max_handoff_continuations=0 missing handoff → dispatch_paused
invalid tool token
handoff payload hash conflict → Tool Gateway `handoff_conflict`, CLI exit 7, no Human Review
approval pending → approve → continue
approval pending → deny current action without operator_cancelled side effects
approval pending → expires_at reached → approval_timeout failure + dispatch_paused
command denied
network denied
protected path denied
operator run cancel → no redispatch
approval cancel_run → no redispatch
workflow invalid → dispatch blocked
workspace conflict
review packet failure → no Human Review
untracked file created by agent → patch includes file content
Rework dispatch prompt snapshot/rendered prompt includes latest review reason + previous review packet summary with redaction rules
active run issue transition → reconciliation cancel
Working -> Blocked -> Ready after reconciliation cancel stays dispatch_paused and is not scheduler-redispatched until operator dispatch-resume
Working issue with no active run and dispatch_paused=true is not scheduler-redispatched
startup recoverable Working issue with no active run → source Ready/Rework + dispatch_paused/daemon_restarted_run_interrupted
startup non-recoverable Working issue with no active run → remains Working + dispatch_paused/daemon_restarted_run_interrupted + diagnostics remediation
agent issue.block → Blocked + cancelled/agent_blocked
stale running run on startup → failed/daemon_restarted_run_interrupted + dispatch_paused
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
approval deny records denied without cancel_run side effects
approval responses require risk_level, policy_match, and action_summary
SSE id equals run_events.seq
Last-Event-ID / after_seq replay works
artifact content enforces containment and rejects raw prompt/raw Codex log
Review API returns `content_url=null` for raw prompt/raw Codex log/raw secret artifacts and does not inline disallowed bytes
workflow render preview is schema-covered, dry-run only, and redacts secrets in success and validation-error responses
workflow validate is schema-covered, validates current filesystem WORKFLOW.md only, returns dry-run side effects, and rejects candidate fields
```

### 18.7 Contract artifact validation

Default CI MUST validate:

```text
JSON schemas parse as valid JSON Schema documents
SQLite DDL executes on empty app/project databases
OpenAPI document parses and contains every non-excluded v1 route listed in TECH_SPEC.md
OpenAPI document MUST NOT include routes in 12.11 Excluded APIs
OpenAPI document includes POST /api/v1/workflow/render-preview with request and response schemas
CLI help snapshot MUST NOT expose commands for 12.11 Excluded APIs or hidden/future aliases
handler route inventory MUST match OpenAPI non-excluded routes and MUST NOT include 12.11 Excluded APIs
dashboard action inventory MUST map only to documented command APIs and MUST NOT expose hidden/future actions
Tool Gateway registry inventory MUST include only documented v1 tools and MUST NOT bypass REST policy/auth boundaries
OpenAPI Issue schema required fields match schemas/normalized_issue.schema.json
RunEvent schema requires seq for SSE replay IDs
example WORKFLOW.default.md passes strict config validation
example WORKFLOW.default.md rendered golden includes every Default WORKFLOW prompt contract requirement from 6.7
contract validation fails if the default prompt omits any required no-push/no-PR/no-Done/no-automatic-commit/stdin-handoff/no-root-handoff-json/finalizer-Human-Review constraint
example handoff/followup payloads pass their standalone Tool Gateway input schemas and wrapped Tool Gateway call schemas
docs/agent_work_orders/*.md reference only v1 in-scope capabilities
default CI/test command manifest includes the 18.2 security regression commands and keeps real Codex behind SYMPHONY_TEST_CODEX=1
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
init in non-Git repo, permission denied, or conflicting WORKFLOW.md fails safely without partial incompatible state
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
Issue list supports empty result, pagination, state/label/query/paused filters, stable sorting, and invalid query rejection
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
operator run cancel and approval cancel_run do not redispatch
startup stale active runs fail with daemon_restarted_run_interrupted
```

### M4 — Tool gateway and handoff

Deliver IPC server, run-scoped tokens, fixed registry, tool persistence, issue.get/comment/block, artifact.attach, followup.create, handoff.submit, canonical payload hash, missing handoff continuation.

Acceptance:

```text
tool token validates run/issue/cwd/tool scope
handoff idempotent by payload_hash
handoff.submit does not create an implicit issue comment
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
Approval timeout appears as `approval_timeout`, pauses dispatch, and cannot be decided afterwards
git push denied or unapprovable
network default denied; review-mode network request appears in Approval Inbox
protected path access denied
```

### M7 — Codex adapter

Deliver fixture-gated Codex adapter, process manager, protocol parser, approval bridge, timeout/cancel mapping, real Codex opt-in tests.

Acceptance:

```text
unsupported Codex version fails before launching the real Codex process
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
fake-agent E2E and 18.2 default security regression commands pass
real Codex tests are opt-in
loopback/session/CSRF/tool-token protections work
raw prompt/raw Codex logs not exposed by v1 API
single dist/symphony binary builds
known limitations are documented
```
