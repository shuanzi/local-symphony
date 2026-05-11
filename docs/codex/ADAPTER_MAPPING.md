# Codex Adapter Mapping

Real Codex integration is not inferred from memory. It must be implemented only against committed protocol fixtures for a specific Codex version.

## Lifecycle mapping

| Codex concept | Symphony action/event |
|---|---|
| initialize/handshake | `codex.initialized`, version fixture check |
| thread/start or resume | create runner session for `run_attempt.id` |
| turn/start | `run.status = running`, emit `codex.turn_started` |
| assistant/user/item notification | append redacted `run_events` |
| command approval request | insert `approval_requests(kind=command,status=pending)` |
| file change approval request | insert `approval_requests(kind=file_change,status=pending)` |
| network approval request | insert `approval_requests(kind=network,status=pending)` |
| approval decision approve_once/approve_for_run/approve_for_session | respond to Codex approval channel and update row; emulate run/session scope locally if Codex lacks it |
| approval decision deny | respond deny; in v1 default terminates run unless action-only denial is implemented |
| approval decision cancel_run | terminate run with `operator_cancelled` |
| turn complete with handoff | run finalizer may generate review packet |
| turn complete without handoff | one continuation, then `missing_handoff` |
| protocol error | `codex_protocol_error` |
| process startup failure | `codex_startup_failed` |

## Event normalization

Codex-specific payloads must be redacted before storing in `run_events.data_json`. Raw prompt and raw Codex logs must not be exposed through v1 API.

## Experimental API

If `codex.experimental_api=true`, fixtures must explicitly include experimental fields. Otherwise dispatch fails with `unsupported_codex_version`.
