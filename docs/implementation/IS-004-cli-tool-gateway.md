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
app DB runtime metadata
error
```

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

The descriptor contains API URL and tool endpoint. It contains no secrets or session tokens.

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
http://127.0.0.1:<port>/tool
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

Protocol:

```http
POST /tool/v1/call
Authorization: Bearer <SYMPHONY_TOOL_TOKEN>
Content-Type: application/json
```

Tool registry:

```text
issue.get
issue.comment
issue.block
artifact.attach
followup.create
handoff.submit
```

No tool provides issue delete, Done, arbitrary state, project settings, workspace delete, git push, PR, or secret read.

### issue.block semantics

`issue.block` is limited to the current issue and records why agent progress is blocked:

```text
1. Validate token scope and current issue.
2. Set current issue state to Blocked, if allowed by state transition rules.
3. Add a system/agent-visible comment with the block reason.
4. Set dispatch_paused=true with reason=agent_blocked.
5. Emit issue.blocked and tool.call events.
```

`issue.block` must not create arbitrary blocker relations. Blocker relations are operator-controlled through normal issue blocker APIs. If the agent discovers follow-up work, it may use `followup.create` when allowed, but that is separate from blocking the current issue.

## Handoff

Recommended:

```bash
symphony tool handoff --json ./handoff.json
```

Handoff does not directly move issue to Human Review. It writes handoff data. The run finalizer generates review packet and then moves issue to Human Review.

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
| IS4-018 | unified `/tool/v1/call` JSON protocol |
| IS4-019 | v1 tool registry fixed |
| IS4-020 | two-stage handoff |
| IS4-020a | `issue.block` only blocks current issue; it does not create arbitrary blocker relations |
| IS4-021 | token scope validated every call |
| IS4-022 | tool calls persisted if attributable |
| IS4-023 | CLI preflight + daemon final path validation |
| IS4-024 | tool CLI timeout 30s |
| IS4-025 | prompt tool manifest generated from registry |
| G1 | dispatch pause/resume CLI added |
