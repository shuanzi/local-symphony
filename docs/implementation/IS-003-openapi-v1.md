# IS-003 — OpenAPI v1 Contract and REST/SSE API

## Status

Frozen.

## Goal

Define the v1 API contract for React Dashboard, operator CLI, and future desktop shell.

## API categories

```text
Auth API
Query API
Command API
Artifact API
SSE API
```

API prefix:

```text
/api/v1
```

All business JSON APIs use OpenAPI. SSE uses the `RunEvent` schema documented in `api/openapi.yaml`.

`api/openapi.yaml` is now the API source of truth. This implementation spec explains semantics that cannot be fully expressed in OpenAPI. If request/response shape conflicts, update this document to match `api/openapi.yaml`. `docs/api/openapi-v1-outline.md` is retained only as a historical outline.

## Response envelopes

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

## Auth API

```http
POST /api/v1/auth/exchange
GET  /api/v1/auth/session
POST /api/v1/auth/logout
```

`symphony open` generates a one-time open token and opens:

```text
http://127.0.0.1:<port>/?open_token=<token>
```

React exchanges it for a browser session. Browser uses HttpOnly SameSite cookie plus CSRF header for command APIs.

CLI uses bearer token.

## Health / state

```http
GET /api/v1/health
GET /api/v1/state
```

`/health` returns machine-readable health. `/state` returns Overview dashboard snapshot.

## Events

```http
GET /api/v1/events
GET /api/v1/events/stream
GET /api/v1/runs/{run_id}/events
GET /api/v1/runs/{run_id}/events/stream
GET /api/v1/issues/{issue_ref}/events/stream
```

SSE uses:

```text
id = run_events.seq
event = event type
data = JSON RunEvent
```

Reconnect uses `Last-Event-ID`. Large payloads are fetched via Artifact API.

## Issue API

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

Resource reference rules:

```text
{issue_ref} accepts either internal id `iss_...` or human identifier such as `LOC-1`.
{blocker_issue_ref} follows the same rule.
The server resolves refs before authorization, state checks, and transactions.
Ambiguous, missing, or malformed refs return `not_found` or `invalid_request` with no partial mutation.
Responses always include both `id` and `identifier`.
```

Rules:

```text
PATCH cannot change state.
State changes use /transition.
Dispatch uses /dispatch.
Blockers use blocker command endpoints.
G1 adds dispatch pause/resume endpoints.
If a transition moves an issue with an active run out of Ready/Working/Rework, the API command must enqueue orchestrator reconciliation cancel and return the updated issue plus reconciliation metadata.
```

## Issue transition response side effects

Transition commands return the normalized issue and may include side effects:

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

If no active run exists, `active_run_cancelled=false` or `side_effects` may be omitted.

## Run API

```http
GET  /api/v1/runs
GET  /api/v1/runs/{run_id}
GET  /api/v1/runs/{run_id}/events
POST /api/v1/runs/{run_id}/cancel
```

No arbitrary run mutation endpoint.

`POST /api/v1/runs/{run_id}/cancel` applies the cancellation behavior in IS-006: terminal `cancelled`, `failure_code=operator_cancelled`, issue dispatch paused with `dispatch_pause_reason=operator_cancelled`, and no automatic redispatch.

## Approval API

```http
GET  /api/v1/approvals
POST /api/v1/approvals/{approval_id}/decide
```

Allowed decisions:

```text
approve_once
approve_for_run
approve_for_session
deny
cancel_run
```

Only pending approvals can be decided. Approval responses must expose `requested_at`, `timeout_ms`, `expires_at`, and `resolved_at` so UI can show expiry consistently after daemon restart. Decision `cancel_run` has the same side effect as operator run cancel: run `cancelled`, `failure_code=operator_cancelled`, issue dispatch paused, and no automatic redispatch.

## Review API

```http
GET  /api/v1/reviews/{issue_ref}
POST /api/v1/reviews/{issue_ref}/send-to-rework
POST /api/v1/reviews/{issue_ref}/mark-done
```

Mark Done requires:

```text
issue.state = Human Review
latest review_packet.status = generated
```

## Artifact API

```http
GET /api/v1/artifacts/{artifact_id}
GET /api/v1/artifacts/{artifact_id}/content
```

Artifact content requires path containment under `.symphony/artifacts` or `.symphony/exports`. v1 does not provide raw prompt or raw Codex log export.

## Workflow API

```http
GET  /api/v1/workflow
POST /api/v1/workflow/reload
```

`dry_run=true` validates without replacing effective config. Invalid reload preserves last valid config. If no valid config exists, dispatch is blocked.

## Diagnostics API

```http
GET  /api/v1/diagnostics
POST /api/v1/diagnostics/export
```

Diagnostics export is redacted only. `include_raw_logs=true` returns unsupported in v1.

## v1 excluded APIs

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

## Run status and error code contract

`RunStatus` uses the local enum from IS-006:

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

Terminal run reasons and dispatch pause reasons use `FailureCode` from IS-006. API error envelopes use `ApiErrorCode`. An API error code may equal a `FailureCode` when the API operation exposes or causes that run failure, but protocol/auth/request errors are not run failure codes.

Required mapping:

| Situation | API `error.code` | Run `failure_code` / pause reason |
|---|---|---|
| command policy terminates a run | `command_denied` | `command_denied` |
| network policy terminates a run | `network_denied` | `network_denied` |
| protected path policy terminates a run | `protected_path_denied` | `protected_path_denied` |
| workspace ownership/path conflict | `workspace_conflict` | `workspace_conflict` or `workspace_prepare_failed` if lower-level failure |
| invalid browser/CLI auth | `unauthorized` / `forbidden` | none |
| invalid CSRF | `csrf_required` | none |
| invalid transition request | `invalid_state_transition` | none unless reconciliation cancels an active run |

Dashboard labels for terminal runs and dispatch pauses must use the canonical `FailureCode` names in IS-006.

## Core schemas

OpenAPI components must include:

```text
SuccessEnvelope
ErrorEnvelope
ApiErrorCode
Pagination
IssueRef
Issue
IssueState
IssueCreateRequest
IssueUpdateRequest
IssueTransitionRequest
IssueDispatchRequest
DispatchPauseRequest
RunAttempt
RunStatus
RunEvent
ApprovalRequest
ApprovalDecisionRequest
ReviewPacket
Artifact
WorkflowValidation
Diagnostics
FailureCode
```

`Issue` follows `docs/schema/normalized-issue-v1.md` unless an endpoint explicitly documents a smaller projection.

## Frozen decisions

| ID | Decision |
|---|---|
| IS3-001 | `/api/v1` prefix |
| IS3-002 | REST success/error envelope |
| IS3-003 | HTTP status + error.code semantics |
| IS3-004 | Auth exchange/session/logout |
| IS3-005 | CSRF for cookie command APIs |
| IS3-006 | health vs state distinction |
| IS3-007 | JSON events and SSE events separated |
| IS3-008 | issue state/dispatch/blocker use command endpoints |
| IS3-009 | run API read/cancel only; cancel records `operator_cancelled` and pauses redispatch for active-state issues |
| IS3-010 | approval decision is final once |
| IS3-011 | review API is Human Review decision entrypoint |
| IS3-011a | issue transition matrix and active-run reconciliation notification required |
| IS3-012 | artifact API with containment checks |
| IS3-013 | workflow reload supports dry_run |
| IS3-014 | redacted diagnostics export only |
| IS3-015 | SSE replay via run_events.seq |
| IS3-016 | OpenAPI schemas for core DTOs; Issue follows normalized issue DTO |
| IS3-017 | cursor pagination; events use after_seq |
| IS3-018 | no generic idempotency key in v1 |
| IS3-019 | no publish/backup/migration/audit/destructive/secrets API |
| IS3-020 | frontend types generated from OpenAPI |
| IS3-021 | state transitions that make active runs ineligible trigger reconciliation cancel |
| IS3-022 | approval schemas expose timeout/expiry fields |
| IS3-023 | API error codes are split from run `FailureCode`, with explicit mapping where policy/API errors terminate runs |
| IS3-024 | issue path refs accept either internal id or human identifier and resolve server-side |
| IS3-025 | `api/openapi.yaml` is the executable API contract for generated frontend types, handler conformance, CLI clients, and contract tests |
| G1 | dispatch-pause and dispatch-resume endpoints added |
| G3 | run failure/cancellation pause semantics are API-visible |
| G7 | active run reconciliation side effects are API-visible |
