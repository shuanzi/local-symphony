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

All business JSON APIs use OpenAPI. SSE uses a documented event schema.

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
GET /api/v1/issues/{issue_id}/events/stream
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
GET    /api/v1/issues/{issue_id}
PATCH  /api/v1/issues/{issue_id}
POST   /api/v1/issues/{issue_id}/transition
POST   /api/v1/issues/{issue_id}/comments
POST   /api/v1/issues/{issue_id}/blockers
DELETE /api/v1/issues/{issue_id}/blockers/{blocker_issue_id}
POST   /api/v1/issues/{issue_id}/dispatch
POST   /api/v1/issues/{issue_id}/dispatch-pause
POST   /api/v1/issues/{issue_id}/dispatch-resume
```

Rules:

```text
PATCH cannot change state.
State changes use /transition.
Dispatch uses /dispatch.
Blockers use blocker command endpoints.
G1 adds dispatch pause/resume endpoints.
```

## Run API

```http
GET  /api/v1/runs
GET  /api/v1/runs/{run_id}
GET  /api/v1/runs/{run_id}/events
POST /api/v1/runs/{run_id}/cancel
```

No arbitrary run mutation endpoint.

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

Only pending approvals can be decided.

## Review API

```http
GET  /api/v1/reviews/{issue_id}
POST /api/v1/reviews/{issue_id}/send-to-rework
POST /api/v1/reviews/{issue_id}/mark-done
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
POST /api/v1/git/:issue_id/push
POST /api/v1/git/:issue_id/create-pr
POST /api/v1/db/backup
POST /api/v1/db/migrate
GET  /api/v1/audit
POST /api/v1/workspaces/:issue_id/delete
POST /api/v1/secrets
```

## Core schemas

OpenAPI components must include:

```text
SuccessEnvelope
ErrorEnvelope
Pagination
Issue
IssueState
RunAttempt
RunStatus
RunEvent
ApprovalRequest
ReviewPacket
Artifact
WorkflowValidation
Diagnostics
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
| IS3-009 | run API read/cancel only |
| IS3-010 | approval decision is final once |
| IS3-011 | review API is Human Review decision entrypoint |
| IS3-012 | artifact API with containment checks |
| IS3-013 | workflow reload supports dry_run |
| IS3-014 | redacted diagnostics export only |
| IS3-015 | SSE replay via run_events.seq |
| IS3-016 | OpenAPI schemas for core DTOs; Issue follows normalized issue DTO |
| IS3-017 | cursor pagination; events use after_seq |
| IS3-018 | no generic idempotency key in v1 |
| IS3-019 | no publish/backup/migration/audit/destructive/secrets API |
| IS3-020 | frontend types generated from OpenAPI |
| G1 | dispatch-pause and dispatch-resume endpoints added |
