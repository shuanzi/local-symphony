# IS-014 — Store Contract and Persistence Rules

## Status

Frozen.

## Goal

Define persistence semantics that are not fully expressed by SQL DDL: IDs, transaction boundaries, file/DB atomicity, schema version handling, daemon ownership, and artifact consistency.

## Source of truth

The executable schema files are authoritative:

```text
db/schema/app_v1.sql
db/schema/project_v1.sql
```

Markdown schema files explain the schema. If Markdown and SQL conflict, update Markdown to match SQL.

## ID generation

IDs are opaque strings with stable prefixes:

| Entity | Prefix | Example |
|---|---|---|
| Project | `proj_` | `proj_01hx...` |
| Issue | `iss_` | `iss_01hx...` |
| Run attempt | `run_` | `run_01hx...` |
| Run event | `evt_` | `evt_01hx...` |
| Workspace | `ws_` | `ws_01hx...` |
| Approval | `appr_` | `appr_01hx...` |
| Tool token row | `tok_` | `tok_01hx...` |
| Tool call | `tc_` | `tc_01hx...` |
| Handoff | `hand_` | `hand_01hx...` |
| Artifact | `art_` | `art_01hx...` |
| Review packet | `rp_` | `rp_01hx...` |
| Workflow snapshot | `wf_` | `wf_01hx...` |
| Prompt snapshot | `ps_` | `ps_01hx...` |
| Comment | `cmt_` | `cmt_01hx...` |
| Relation | `rel_` | `rel_01hx...` |
| State history | `hist_` | `hist_01hx...` |

Implementation may use ULID, UUIDv7, or another collision-resistant sortable ID. IDs must not encode secrets, absolute paths, or raw prompt content.

## Time format

All persisted timestamps are RFC3339 UTC strings. Store UTC only. UI may localize.

## SQLite connection requirements

Every connection must execute or verify:

```sql
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
```

`journal_mode` may be set during DB open/init. Tests must verify foreign keys are enforced.

## Schema version handling

Each DB must have exactly one `schema_version` row with `version = 1`.

Startup behavior:

| Condition | Behavior |
|---|---|
| DB missing | Initialize from the matching SQL file. |
| `schema_version` missing | Fail with `unsupported_db_version`; do not mutate. |
| `schema_version.version = 1` | Continue. |
| `schema_version.version > 1` | Fail with `unsupported_db_version`; do not mutate. |
| `schema_version.version < 1` | Fail with `unsupported_db_version`; no migration in v1. |

v1 has no production migration or rollback flow.

## App DB vs project DB

The app DB stores registered projects and local sessions. The project DB stores tracker, orchestration, approvals, artifacts, review packets, and workflow snapshots.

The runtime descriptor, not either DB, is the source for mutable daemon endpoint metadata:

```text
~/.symphony/runtime/<project_id>.json
```

The descriptor contains no secrets.

## Project daemon ownership

v1 allows at most one active daemon owner per project DB.

Required behavior:

```text
1. serve resolves project_id and project DB path
2. acquire project runtime lock before accepting API/tool requests
3. write runtime descriptor after successful lock and bind
4. if another live daemon owns the project, fail fast with a clear error
5. if descriptor/lock owner PID is stale, remove stale descriptor after validation and continue
6. on normal shutdown, remove descriptor and release lock
```

The lock may be an OS file lock under `.symphony/runtime` or another single-host mechanism. Do not rely only on PID files.

## Issue sequence allocation

`counters.key='issue_sequence'` is incremented in the same transaction that inserts an issue.

```text
next_value = current value + 1
identifier = <issue_prefix>-<next_value>
sequence_no = next_value
```

Concurrent issue creation must not allocate duplicate identifiers.

## Attempt numbers

`run_attempts.attempt_no` starts at `1` per issue and increments monotonically. It is allocated in the dispatch claim transaction.

## Dispatch claim transaction

The claim transaction in IS-006 is mandatory. No run worker may start before the pending `run_attempts` claim row commits.

If the claim transaction fails, no workspace, token, process, or prompt artifact may be created for that claim.

## Worker write authority

The orchestrator actor owns scheduler state transitions and terminal outcomes. Worker goroutines may write scoped progress details only through actor-approved store methods.

Allowed worker-origin writes:

```text
run_events for progress
approval_requests created through adapter bridge
artifacts created by prompt/review/tool flows
prompt_snapshots once prompt files are durably written
tool_calls through Tool Gateway
handoffs through Tool Gateway
```

Terminal `run_attempts.status`, issue state transitions, and dispatch pause/resume side effects are actor/finalizer responsibilities.

## File and DB atomicity

SQLite and filesystem writes are not a single atomic transaction. For artifact-producing flows, use this order:

```text
1. write files into a temporary directory under .symphony/tmp
2. fsync/close files when supported
3. rename the temporary directory or files into final artifact path under .symphony/artifacts or .symphony/exports
4. insert artifact/review/prompt DB rows in one DB transaction
5. if DB transaction fails, leave files as orphan candidates and emit diagnostic on next scan
6. if file write fails, do not insert success rows
```

A row with `review_packets.status=generated` must never point to missing required files. If a partial packet is useful for diagnostics, use `status=partial` or `status=failed`.

## Artifact paths

Artifact DB `path` is project-local relative path. It must resolve under one of:

```text
<repo>/.symphony/artifacts
<repo>/.symphony/exports
```

Do not store absolute artifact paths in the DB. API content access must re-resolve and enforce containment.

## Prompt snapshot consistency

Prompt snapshot files must be durable before inserting `prompt_snapshots`.

`review_packets.prompt_snapshot_id` should reference the prompt snapshot used for the run when available.

## Handoff canonical payload hash

Tool Gateway computes `handoffs.payload_hash` from canonical JSON:

```text
UTF-8 JSON
object keys sorted recursively
no insignificant whitespace
arrays preserve order
omit absent optional fields
include explicit nulls if supplied and accepted by schema
```

The hash is SHA-256 over canonical bytes, hex encoded.

Idempotency rule:

| Existing handoff for run | New payload hash | Result |
|---|---|---|
| none | any valid hash | insert handoff |
| exists | same hash | return existing handoff as idempotent success |
| exists | different hash | reject with state conflict |

`tool_calls.input_hash` may match the handoff payload hash, but `handoffs.payload_hash` is the durable handoff idempotency source.

## Startup stale active runs

On startup, before dispatch, scan active `run_attempts.status` values:

```text
pending
preparing_workspace
rendering_prompt
starting_agent
running
```

For rows owned by a previous daemon/process, mark:

```text
status = failed
failure_code = daemon_restarted_run_interrupted
ended_at = now
issues.dispatch_paused = true
issues.dispatch_pause_reason = daemon_restarted_run_interrupted
issues.dispatch_paused_at = now
```

Emit `system.interrupted` or equivalent run event.

## Store tests

Required tests:

```text
schema init from SQL files
unsupported DB version refusal
foreign key enforcement
issue sequence concurrent allocation
attempt_no monotonic allocation
handoff idempotency same hash vs different hash
review packet generated row cannot point to missing files
artifact path containment
startup stale active run interruption
single-daemon project lock refusal
```

## Frozen decisions

| ID | Decision |
|---|---|
| IS14-001 | SQL files are DB source of truth. |
| IS14-002 | IDs are opaque prefixed strings and contain no secrets. |
| IS14-003 | all timestamps are RFC3339 UTC. |
| IS14-004 | WAL, busy timeout, synchronous normal, and foreign keys are required. |
| IS14-005 | unsupported DB version fails safely; no v1 migrations. |
| IS14-006 | one active daemon owner per project DB. |
| IS14-007 | issue sequence and attempt number allocation are transactional. |
| IS14-008 | artifact files are written durably before success DB rows. |
| IS14-009 | handoff idempotency uses `handoffs.payload_hash`. |
| IS14-010 | startup stale active runs are interrupted, not recovered. |
