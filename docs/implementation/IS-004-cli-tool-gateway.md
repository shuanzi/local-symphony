# IS-004 — CLI Command Spec and Tool Gateway Contract

## Status

Frozen.

## Command groups

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

Normal CLI commands are for operators and use `/api/v1`. Agent tool commands are for Codex runs and use Tool Gateway IPC.

## Global flags

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

The runtime descriptor is the v1 daemon endpoint source of truth. The app DB does not store mutable daemon runtime endpoint metadata in v1.

## Output

Normal CLI default: human-readable. `--json`: machine-readable.

`symphony tool` always outputs JSON only. stdout is JSON; diagnostics go to stderr.

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

## Runtime descriptor

`symphony serve` writes:

```text
~/.symphony/runtime/<project_id>.json
```

The descriptor contains project id, repo root, API URL, Tool Gateway endpoint, daemon pid, and start time. It contains no secrets or session tokens.

## Normal CLI

### init

```bash
symphony init [--name <name>] [--issue-prefix LOC] [--workflow-template default]
```

Allowed to write initial project files and DB.

### serve

```bash
symphony serve [--project <path>] [--host 127.0.0.1] [--port 0] [--open] [--no-open]
```

Starts daemon, writes runtime descriptor, creates/refreshes CLI session.

### open

```bash
symphony open [--project <path>]
```

Opens dashboard using one-time open token. Does not implicitly start daemon.

### issue

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

### run

```bash
symphony run LOC-1
symphony run list
symphony run show run_...
symphony run events run_... --follow
symphony run cancel run_... --reason "..."
```

`run LOC-1` is an alias for issue dispatch.

### approval

```bash
symphony approval list
symphony approval decide appr_... --approve-once
symphony approval decide appr_... --deny --reason "..."
symphony approval decide appr_... --cancel-run --reason "..."
```

### review

```bash
symphony review LOC-1
symphony review send-to-rework LOC-1 --reason "..."
symphony review mark-done LOC-1 --reason "..."
symphony review path LOC-1
```

No publish/PR commands in v1.

### workflow

```bash
symphony workflow validate   # dry-run
symphony workflow reload     # update last valid config if valid
symphony workflow show
```

### diagnostics

```bash
symphony diagnostics
symphony diagnostics export
```

Redacted export only.

## Tool Gateway

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

Transport schemes:

```text
unix://<path>
npipe://<name>
http://127.0.0.1:<port>
```

`SYMPHONY_TOOL_ENDPOINT` is the transport base endpoint. For HTTP it is the base origin, for example `http://127.0.0.1:<port>`, not a path-specific URL. The tool call path is appended by the client.

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

Protocol:

```http
POST {SYMPHONY_TOOL_ENDPOINT}/tool/v1/call
Authorization: Bearer <SYMPHONY_TOOL_TOKEN>
Content-Type: application/json
```

For Unix socket and Windows named pipe transports, the request path is still `/tool/v1/call`; only the transport base changes.

Tool registry:

```text
issue.get
issue.comment
issue.block
artifact.attach
followup.create
handoff.submit
```

`symphony tool ...` shell commands are default-allowed by IS-009 command policy so the agent can reach this gateway, but each tool operation is still authorized by token, scope, cwd, schema, and registry checks.

No tool provides issue delete, Done, arbitrary state, project settings, workspace delete, git push, PR, or secret read.

### Tool schemas and transaction semantics

All tool inputs are JSON objects. Unknown fields are rejected. Successful tools return a JSON object with `success=true`, `tool`, `issue_id`, `issue_identifier`, and tool-specific fields. Failed tools return `success=false`, `error.code`, and `error.message`; attributable failures are persisted in `tool_calls`.

#### issue.get

Input:

```json
{}
```

Returns the current issue as `NormalizedIssue`. The agent cannot request another issue by id.

#### issue.comment

Input:

```json
{
  "body": "What changed or what was discovered."
}
```

Creates an agent-authored comment on the current issue, associated with the current run. Empty or whitespace-only comments are rejected.

#### issue.block

Input:

```json
{
  "reason": "Why the current issue cannot proceed.",
  "details": "Optional supporting detail."
}
```

Blocks only the current issue. It does not create blocker relations. See the dedicated semantics below.

#### artifact.attach

Input:

```json
{
  "path": "relative/path/from/workspace.log",
  "kind": "test_output",
  "description": "Optional short description."
}
```

Rules:

```text
path must resolve under the workspace
absolute paths and path traversal are rejected
protected paths are rejected
size must be <= tools.artifact_max_bytes
artifact row path is project-local relative under .symphony/artifacts
```

#### followup.create

Input:

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
creates a new issue in Inbox
created_by_type = agent
created_by_run_id = current run
creates relation: new_issue followup_of current_issue
agent cannot set the follow-up to Ready, Working, Human Review, Done, Cancelled, Duplicate, or Blocked
agent cannot create blocks or duplicates relations
```

#### handoff.submit

Input:

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

`target_state` is optional; if present it must equal `Human Review`. The first successful handoff for a run wins. A repeated submission with the same payload hash is idempotent; a different payload after a successful handoff returns a state conflict.

### Relation direction

`issue_relations` directions are fixed:

| relation_type | source_issue_id | target_issue_id | Agent permission |
|---|---|---|---|
| `blocks` | blocker issue | blocked issue | not via tool gateway |
| `duplicates` | duplicate issue | canonical issue | not via tool gateway |
| `followup_of` | follow-up issue | original/current issue | only through `followup.create` |

### issue.block semantics

`issue.block` is limited to the current issue and records why agent progress is blocked:

```text
1. Validate token scope and current issue.
2. Set current issue state to Blocked, if allowed by state transition rules.
3. Add a system/agent-visible comment with the block reason.
4. Set dispatch_paused=true with reason=agent_blocked.
5. Enqueue active run reconciliation for the current run; final run status becomes cancelled with failure_code=agent_blocked.
6. Emit issue.blocked, run.cancelled, and tool.call events.
```

`issue.block` must not create arbitrary blocker relations. Blocker relations are operator-controlled through normal issue blocker APIs. If the agent discovers follow-up work, it may use `followup.create` when allowed, but that is separate from blocking the current issue.

## Handoff

Recommended:

```bash
symphony tool handoff --json ./handoff.json
```

Handoff does not directly move issue to Human Review. It writes handoff data. The run finalizer generates review packet and then moves issue to Human Review.

v1 only supports target state `Human Review`. If handoff input includes a different `target_state`, the gateway returns a state conflict error and records a failed tool call.

Successful `handoff.submit` output must indicate receipt, not final state transition:

```json
{
  "success": true,
  "tool": "handoff",
  "issue_identifier": "LOC-123",
  "handoff_status": "received",
  "handoff_id": "hand_..."
}
```

`handoff_status=received` means the handoff row and tool call were persisted. The issue enters `Human Review` only after the review-packet finalizer records `review_packet.status=generated`.

## Tool validation

Every tool call validates:

```text
token hash
not expired
run.status = running
issue_id and run_id scope
cwd under workspace
allowed tool
daemon-side path containment
```

All attributable tool calls are recorded in `tool_calls` and `run_events`, success or failure.

## Frozen decisions

| ID | Decision |
|---|---|
| IS4-001 | normal CLI and tool CLI share binary but not auth/channel |
| IS4-002 | CLI resolves project then daemon |
| IS4-003 | tool output JSON-only |
| IS4-004 | fixed exit code set |
| IS4-005 | runtime descriptor has no secret |
| IS4-006 | init can write initial DB/files |
| IS4-007 | serve starts daemon and descriptor |
| IS4-008 | open does not start daemon |
| IS4-009 | issue CLI supports id and identifier |
| IS4-010 | run alias dispatch; only cancel mutation |
| IS4-011 | approval CLI fallback |
| IS4-012 | review CLI fallback; no publish/PR |
| IS4-013 | workflow validate dry-run, reload applies |
| IS4-014 | diagnostics redacted only |
| IS4-015 | no backup/migration/audit/publish/destructive/secret CLI |
| IS4-016 | tool gateway internal IPC API |
| IS4-017 | tool endpoint via env |
| IS4-018 | unified `/tool/v1/call` JSON protocol with `SYMPHONY_TOOL_ENDPOINT` as transport base |
| IS4-019 | v1 tool registry fixed |
| IS4-020 | two-stage handoff |
| IS4-020a | `issue.block` only blocks current issue; it does not create arbitrary blocker relations |
| IS4-020b | `issue.block` cancels the current active run through reconciliation with `agent_blocked` |
| IS4-020c | handoff target state is fixed to `Human Review` in v1 |
| IS4-020d | `followup.create` creates an Inbox issue with `new_issue followup_of current_issue`; agents cannot create blocker or duplicate relations |
| IS4-020e | `symphony tool ...` shell commands are command-policy allowed but still tool-gateway authorized |
| IS4-021 | token scope validated every call |
| IS4-022 | tool calls persisted if attributable |
| IS4-023 | CLI preflight + daemon final path validation |
| IS4-024 | tool CLI timeout 30s |
| IS4-025 | prompt tool manifest generated from registry |
| IS4-026 | runtime descriptor, not app DB runtime metadata, is the v1 daemon endpoint discovery source |
| G1 | dispatch pause/resume CLI added |
