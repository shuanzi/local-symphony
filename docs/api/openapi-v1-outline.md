# OpenAPI v1 Outline

This file is a Markdown outline for the future `api/openapi.yaml`. The actual OpenAPI YAML should use this structure. M0 must generate and commit a machine-readable `api/openapi.yaml` before frontend type generation is treated as complete.

## Server

```yaml
openapi: 3.1.0
info:
  title: Local Symphony API
  version: 0.1.0
servers:
  - url: /api/v1
```

## Paths

```yaml
paths:
  /auth/exchange: {}
  /auth/session: {}
  /auth/logout: {}
  /health: {}
  /state: {}
  /events: {}
  /events/stream: {}
  /issues: {}
  /issues/{issue_ref}: {}
  /issues/{issue_ref}/transition: {}
  /issues/{issue_ref}/comments: {}
  /issues/{issue_ref}/blockers: {}
  /issues/{issue_ref}/blockers/{blocker_issue_ref}: {}
  /issues/{issue_ref}/dispatch: {}
  /issues/{issue_ref}/dispatch-pause: {}
  /issues/{issue_ref}/dispatch-resume: {}
  /issues/{issue_ref}/events/stream: {}
  /runs: {}
  /runs/{run_id}: {}
  /runs/{run_id}/events: {}
  /runs/{run_id}/events/stream: {}
  /runs/{run_id}/cancel: {}
  /approvals: {}
  /approvals/{approval_id}/decide: {}
  /reviews/{issue_ref}: {}
  /reviews/{issue_ref}/send-to-rework: {}
  /reviews/{issue_ref}/mark-done: {}
  /artifacts/{artifact_id}: {}
  /artifacts/{artifact_id}/content: {}
  /workflow: {}
  /workflow/reload: {}
  /diagnostics: {}
  /diagnostics/export: {}
```

## Core schemas

```yaml
components:
  securitySchemes:
    sessionCookie: {}
    bearerAuth: {}
    csrfHeader: {}
  schemas:
    SuccessEnvelope: {}
    ErrorEnvelope: {}
    ApiErrorCode: {}
    Pagination: {}
    IssueRef: {}
    Issue: {}  # see docs/schema/normalized-issue-v1.md
    IssueState: {}
    IssueCreateRequest: {}
    IssueUpdateRequest: {}
    IssueTransitionRequest: {}
    IssueDispatchRequest: {}
    DispatchPauseRequest: {}
    RunAttempt: {}
    RunStatus: {}
    RunEvent: {}
    ApprovalRequest: {}
    ApprovalDecisionRequest: {}
    ReviewPacket: {}
    Artifact: {}
    WorkflowValidation: {}
    Diagnostics: {}
    FailureCode: {}
```

## Path parameter refs

```text
issue_ref accepts internal id `iss_...` or human identifier `LOC-...`.
blocker_issue_ref follows the same rule.
Responses always include both id and identifier.
```

## Key enums

IssueState:

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

RunStatus:

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

Approval decision:

```text
approve_once
approve_for_run
approve_for_session
deny
cancel_run
```

FailureCode:

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

## ApiErrorCode vs FailureCode

`FailureCode` is the terminal run/pause reason enum from IS-006. `ApiErrorCode` includes protocol, auth, request, and product errors. Codes may overlap only when an API operation exposes or causes a terminal run failure.

## Error envelope

```json
{
  "error": {
    "code": "review_packet_required",
    "message": "Latest generated review packet is required to mark Done.",
    "details": {},
    "request_id": "req_..."
  }
}
```

## Important error codes

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
