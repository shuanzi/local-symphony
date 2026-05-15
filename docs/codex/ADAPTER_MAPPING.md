# Codex Adapter Mapping

Real Codex integration is not inferred from memory. It must be implemented only against committed protocol fixtures for a specific Codex version.

The prelaunch fixture gate uses the installed Codex version plus committed fixture metadata/static compatibility metadata. The expected generated protocol/schema version is read from that metadata, not discovered by launching the real `codex app-server` process. If no compatible metadata and fixture exist, dispatch fails before process launch with `unsupported_codex_version`.

## Lifecycle mapping

| Codex concept | Symphony action/event |
|---|---|
| initialize/handshake | `codex.initialized`, verify handshake matches selected fixture metadata; schema/protocol mismatch after launch maps to `codex_protocol_error` |
| thread/start or resume | create runner session for `run_attempt.id` |
| turn/start | `run.status = running`, emit `codex.turn_started` |
| assistant/user/item notification | append redacted `run_events` |
| command approval request | insert `approval_requests(kind=command,status=pending)` |
| file change approval request | insert `approval_requests(kind=file_change,status=pending)` |
| network approval request | insert `approval_requests(kind=network,status=pending)` |
| approval decision approve_once/approve_for_run/approve_for_session | respond to Codex approval channel and update row; emulate run/session scope locally if Codex lacks it |
| approval decision deny | respond deny and update the approval row; continue reading Codex; deny itself must not trigger `cancel_run` or `operator_cancelled` side effects |
| terminal policy failure after denial | map to the corresponding canonical failure code (`command_denied`, `network_denied`, or `protected_path_denied`) and pause through the normal failure path |
| approval decision cancel_run | terminate run with `operator_cancelled` |
| turn complete with handoff | run finalizer may generate review packet |
| turn complete without handoff | one continuation, then `missing_handoff` |
| protocol error | `codex_protocol_error` |
| process startup failure | `codex_startup_failed` |

## Event normalization

Codex-specific payloads must be redacted before storing in `run_events.data_json`. Raw prompt and raw Codex logs must not be exposed through v1 API.

## Experimental API

If `codex.experimental_api=true`, fixtures must explicitly include experimental fields. Otherwise the dispatch attempt fails with `unsupported_codex_version` before launching the real Codex process.
