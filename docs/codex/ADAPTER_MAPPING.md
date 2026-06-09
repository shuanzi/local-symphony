# Codex Adapter Mapping

**同步状态**：与 D3 / R14 5 轮 codex review 收口一致（HEAD `57c46c0`）。本文件描述的 lifecycle mapping / event normalization 与 `internal/agent/codex` adapter、`internal/orchestrator` 的 dispatcher、`internal/observability` 的 diagnostics、阶段 A → 阶段 D 的 fixture policy 一致。

Real Codex integration is not inferred from memory. It must be implemented only against committed protocol fixtures for a specific Codex version.

The prelaunch fixture gate uses the installed Codex version plus committed fixture metadata/static compatibility metadata. The expected generated protocol/schema version is read from that metadata, not discovered by launching the real `codex app-server` process. If no compatible metadata and fixture exist, dispatch fails before process launch with `unsupported_codex_version`.

Compatibility metadata lives at:

```text
internal/agent/codex/testdata/schema/<codex-version>/compatibility.json
```

该文件必须包含 `codex_version`、`protocol_version`、`schema_version`、`supported_notifications`、`supported_requests` 和 `experimental_api`。Adapter preflight 只用 `codex --version` 的解析结果选择 metadata 与 fixture；post-launch initialize handshake 只能验证一致性，不能扩大支持范围。

## Lifecycle mapping

| Codex concept | Symphony action/event |
|---|---|
| initialize/handshake | emit `agent.handshake_completed`, verify handshake matches selected fixture metadata; schema/protocol mismatch after launch maps to `codex_protocol_error` |
| thread/start or resume | create runner session for `run_attempt.id` |
| turn/start | `run.status = running`, emit `agent.turn_started` |
| assistant/user/item notification | append redacted `run_events` |
| stage-A approval placeholder notification | emit redacted `approval.requested` / `approval.resolved`; production row/writeback semantics land in Approval bridge |
| stage-B command approval request | insert `approval_requests(kind=command,status=pending)` |
| stage-B file change approval request | insert `approval_requests(kind=file_change,status=pending)` |
| stage-B network approval request | insert `approval_requests(kind=network,status=pending)` |
| tool observation notification | emit redacted `agent.tool_call_observed`; Tool Gateway records remain daemon-owned |
| stage-B approval decision approve_once/approve_for_run/approve_for_session | respond to Codex approval channel and update row; emulate run/session scope locally if Codex lacks it |
| stage-B approval decision deny | respond deny and update the approval row; continue reading Codex; deny itself must not trigger `cancel_run` or `operator_cancelled` side effects |
| terminal policy failure after denial | map to the corresponding canonical failure code (`command_denied`, `network_denied`, or `protected_path_denied`) and pause through the normal failure path |
| stage-B approval decision cancel_run | terminate run with `operator_cancelled` |
| turn complete with handoff | run finalizer may generate review packet |
| turn complete without handoff | one continuation, then `missing_handoff` |
| protocol error | `codex_protocol_error` |
| process startup failure | `codex_startup_failed` |

## Event normalization

Codex-specific payloads must be redacted before storing in `run_events.data_json`. Raw prompt and raw Codex logs must not be exposed through v1 API.

## Experimental API

If `codex.experimental_api=true`, fixtures must explicitly include experimental fields. Otherwise the dispatch attempt fails with `unsupported_codex_version` before launching the real Codex process.
