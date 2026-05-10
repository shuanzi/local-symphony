# IS-015 — Codex Protocol Fixture and Adapter Contract

## Status

Frozen.

## Goal

Turn the Codex app-server integration from a conceptual adapter into a version-fixture-gated implementation contract.

## Supported protocol policy

The implementation must not infer support for arbitrary Codex app-server versions at runtime.

A Codex version is supported only when the repo contains committed fixtures for that version:

```text
internal/agent/codex/testdata/schema/<codex-version>/
internal/agent/codex/testdata/ts/<codex-version>/
internal/agent/codex/testdata/transcripts/<codex-version>/
```

Unsupported installed versions fail before dispatch with:

```text
run_attempts.status = failed
failure_code = unsupported_codex_version
issues.dispatch_paused = true
```

The docs do not freeze a semantic minimum Codex version. The first implementation freezes support by committing a fixture directory and adapter tests.

## Fixture generation

When updating supported Codex protocol fixtures, generate from the installed Codex binary:

```bash
codex app-server generate-json-schema --out internal/agent/codex/testdata/schema/<codex-version>/
codex app-server generate-ts --out internal/agent/codex/testdata/ts/<codex-version>/
```

Then add representative transcripts:

```text
initialize_success.jsonl
turn_success_with_handoff.jsonl
approval_command_pending.jsonl
approval_file_change_pending.jsonl
approval_network_pending.jsonl
turn_failed.jsonl
malformed_event.jsonl
```

Fixtures are test data. They must not contain secrets, absolute user paths, raw private prompts, or real access tokens.

## Launch contract

Command:

```text
codex app-server
```

Transport:

```text
stdio
```

Working directory:

```text
issue workspace path
```

Environment:

```text
minimal inherited environment
SYMPHONY_TOOL_ENDPOINT
SYMPHONY_TOOL_TOKEN
SYMPHONY_PROJECT_ID
SYMPHONY_ISSUE_ID
SYMPHONY_ISSUE_IDENTIFIER
SYMPHONY_RUN_ID
SYMPHONY_WORKSPACE_PATH
```

The adapter captures `codex --version` before launching app-server when available and stores it in `run_attempts.codex_version`.

## Minimum method flow

The selected fixture must define exact JSON-RPC method names and payload shapes. The adapter implementation must cover this logical flow:

```text
1. launch codex app-server over stdio
2. complete initialize handshake within codex.startup_timeout_ms
3. start or create a thread/session for the run
4. start the main turn with rendered prompt
5. read server notifications and requests until terminal turn result, cancellation, timeout, or protocol error
6. bridge command/file/network approval requests to Symphony approval_requests
7. write approval decision back to Codex
8. detect turn completed, turn failed, stalled, or timed out
9. if completed without handoff and continuation unused, send one handoff continuation in same thread/session
10. cancel or interrupt on operator cancellation, approval cancel_run, reconciliation, shutdown, timeout, or context cancellation
11. terminate process group if graceful shutdown fails
```

Do not expose raw Codex protocol shapes outside `internal/agent/codex`.

## Event normalization contract

The adapter writes normalized run events. Required event types:

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

Large/raw protocol payloads go to redacted or raw-ref artifacts according to the security model. UI timelines use normalized events.

## Approval bridge mapping

| Symphony decision | Codex writeback semantics |
|---|---|
| `approve_once` | approve only the current request |
| `approve_for_run` | approve this run; if Codex lacks run scope, emulate locally and approve current request |
| `approve_for_session` | use Codex session-level approval if supported; otherwise emulate for current Codex process session |
| `deny` | decline request |
| `cancel_run` | interrupt/cancel run and apply `operator_cancelled` side effects |

If the selected Codex fixture has different literal field names, the fixture-specific adapter maps them internally. The public Symphony API remains unchanged.

## Timeout mapping

| Condition | Failure code |
|---|---|
| app-server binary missing or launch failed | `codex_startup_failed` |
| startup handshake timeout | `codex_startup_failed` |
| schema/framing mismatch | `codex_protocol_error` |
| unsupported installed fixture | `unsupported_codex_version` |
| whole turn timeout | `turn_timeout` |
| no protocol progress beyond stall timeout | `stall_timeout` |
| approval expired while run waits | `approval_timeout` |

All terminal timeout/failure cases pause issue dispatch per IS-006.

## Fake runner parity

The fake runner is not a separate product path. It must simulate enough adapter behavior to test orchestration and policy deterministically.

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

## Real Codex tests

Real Codex tests are opt-in only:

```bash
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex/...
```

Default CI must use fake runner and fixture replay tests only.

## Frozen decisions

| ID | Decision |
|---|---|
| IS15-001 | Codex support is fixture-gated by committed generated schemas. |
| IS15-002 | unsupported installed versions fail before dispatch with `unsupported_codex_version`. |
| IS15-003 | stdio app-server protocol remains internal to `internal/agent/codex`. |
| IS15-004 | logical initialize/thread/turn/approval/cancel flow is mandatory. |
| IS15-005 | normalized events are the UI/API surface; raw protocol is diagnostic only. |
| IS15-006 | fake runner scenarios are mandatory for default tests. |
| IS15-007 | real Codex tests are opt-in. |
