# IS-008 — Codex Adapter and Approval Bridge

## Status

Frozen.

## Goal

Define the v1 Codex app-server integration, version-aware protocol adapter, event normalization, approval handling, timeouts, cancellation, and fake runner test strategy. `docs/implementation/IS-015-codex-protocol-fixture.md` is the detailed fixture-gated protocol contract and must be implemented together with this file.

## Process launch

Command:

```text
codex app-server
```

cwd:

```text
issue workspace path
```

Environment:

```text
minimal host env
SYMPHONY_TOOL_ENDPOINT
SYMPHONY_TOOL_TOKEN
SYMPHONY_PROJECT_ID
SYMPHONY_ISSUE_ID
SYMPHONY_ISSUE_IDENTIFIER
SYMPHONY_RUN_ID
SYMPHONY_WORKSPACE_PATH
```

Protocol target:

```text
v1 targets the selected Codex app-server stdio transport.
The adapter must validate framing/schema compatibility for the installed Codex version in tests.
stderr is diagnostic only unless the selected Codex protocol explicitly documents otherwise.
```

Versioned protocol fixtures are required before implementing or updating the adapter. The exact fixture policy, transcript requirements, and logical initialize/thread/turn flow are frozen in IS-015. Generate and commit fixtures for each supported Codex app-server version:

```bash
codex app-server generate-json-schema --out internal/agent/codex/testdata/schema/<codex-version>/
codex app-server generate-ts --out internal/agent/codex/testdata/ts/<codex-version>/
```

If the installed Codex schema/version is not covered by committed fixtures, the adapter must fail before dispatch with `unsupported_codex_version`.

Startup/version rules:

```text
capture `codex --version` before process launch when available
store version in run_attempts.codex_version
startup handshake must complete within codex.startup_timeout_ms, default 60000
missing binary or launch failure → codex_startup_failed
unsupported selected protocol fixture → unsupported_codex_version
handshake framing/schema mismatch → codex_protocol_error
unsupported installed version → unsupported_codex_version
```

The documentation does not hard-code a semantic Codex minimum version. The minimum is the first version covered by the selected adapter protocol fixture in `internal/agent/codex/testdata`.

Each run has a separate process group. Cancel first attempts graceful shutdown, then kills process group.

## Adapter interface

The orchestrator sees a minimal runner interface, not Codex protocol internals:

```go
type Runner interface {
    Run(ctx context.Context, req RunRequest) (*RunResult, error)
    Cancel(ctx context.Context, runID core.RunID) error
}
```

The Codex implementation handles process, protocol, approvals, and event normalization internally.

## Protocol parser

Rules:

```text
protocol framing/parser lives only in internal/agent/codex
parser follows the selected Codex app-server protocol version
framing/schema mismatch → codex_protocol_error
raw protocol messages written to raw Codex log reference
normalized events written to run_events
orchestrator never depends on raw Codex protocol shapes
```

## Event normalization

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

Raw payloads are not the main UI timeline. Large payloads use artifacts/raw references.

## Approval bridge

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

```text
approve_once        → accept
approve_for_run     → acceptForSession or local run allow
approve_for_session → acceptForSession
deny                → decline
cancel_run          → cancel and apply IS-006 operator_cancelled dispatch pause semantics
```

If a command, network, or protected-path denial causes the turn or run to terminate, the adapter reports the corresponding canonical failure code: `command_denied`, `network_denied`, or `protected_path_denied`.

## Timeouts

```text
turn_timeout_ms: whole turn max duration
stall_timeout_ms: max duration without protocol event
approval timeout: default 30 minutes
startup_timeout_ms: app-server launch/handshake max duration
read_timeout_ms: protocol read heartbeat/blocking guard
```

Timeout results:

```text
run failed
failure_code = codex_startup_failed / turn_timeout / stall_timeout / approval_timeout
issue.dispatch_paused = true
```

## Handoff continuation

If turn completes without handoff:

```text
reuse same session/thread
send dedicated handoff continuation prompt
max continuation = 1
```

Do not resend full prompt.

## Fake runner

Required test modules:

```text
internal/agent/fake
internal/agent/codex/testdata/protocol-fixtures
```

Scenarios:

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
malformed_event
unsupported_codex_version
startup_handshake_timeout
review_packet_failure
active_run_reconciliation_cancel
```

Real Codex tests are opt-in.

## Frozen decisions

| ID | Decision |
|---|---|
| IS8-001 | one Codex subprocess per run |
| IS8-002 | version-aware Codex protocol adapter; stdio target is adapter detail |
| IS8-003 | adapter hides Codex protocol from orchestrator |
| IS8-004 | approval bridge handles blocking/decision/writeback, including `cancel_run` pause semantics |
| IS8-005 | raw Codex log diagnostic only |
| IS8-006 | timeout failure pauses issue dispatch |
| IS8-007 | handoff continuation reuses same session/thread |
| IS8-008 | fake runner mandatory |
| IS8-009 | Codex version capture and startup handshake timeout are required |
| IS8-010 | protocol fixtures define supported Codex versions and unsupported installed schemas fail with `unsupported_codex_version` |
| IS8-011 | IS-015 is the protocol fixture and adapter contract for concrete Codex method flow, transcript fixtures, and fake-runner parity |
