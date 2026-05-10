# Acceptance Tests — Local Symphony App v1

This file defines implementation acceptance from an agent-development perspective. Test names are normative even if the final test framework uses different file paths.

## Default test commands

The final repo should expose equivalent commands:

```bash
go test ./...
pnpm --dir web typecheck
pnpm --dir web test
go test ./internal/e2e -run TestMainPathFakeAgent
go test ./internal/e2e -run TestMissingHandoffThenContinuation
go test ./internal/e2e -run TestMissingHandoffTwicePausesDispatch
go test ./internal/e2e -run TestUntrackedFileIncludedInReviewPatch
go test ./internal/e2e -run TestOperatorCancelNoRedispatch
go test ./internal/e2e -run TestApprovalCancelRunNoRedispatch
go test ./internal/e2e -run TestActiveRunReconciliationCancel
go test ./internal/e2e -run TestAgentIssueBlockCancelsRun
go test ./internal/e2e -run TestStartupStaleRunInterrupted
```

Real Codex tests are opt-in only:

```bash
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex/...
```

## Required E2E scenarios

| Scenario | Expected result |
|---|---|
| `init → create issue → Ready → dispatch → fake handoff → review → Done` | Issue reaches `Done`; latest review packet is `generated`; workspace still exists. |
| Missing handoff once | Same session/thread receives one handoff continuation; handoff can still succeed. |
| Missing handoff twice | Run is `completed_without_handoff`, `failure_code=missing_handoff`; issue dispatch is paused. |
| Invalid tool token | Tool call fails; no mutation occurs; attributable failure is recorded. |
| Approval pending → approve | Run continues and can complete. |
| Command denied | Terminal run uses `failure_code=command_denied` if denial prevents completion. |
| Network denied | Terminal run uses `failure_code=network_denied` if denial prevents completion. |
| Protected path denied | Terminal run uses `failure_code=protected_path_denied` if denial prevents completion. |
| Operator run cancel | Run is `cancelled`, `failure_code=operator_cancelled`; issue dispatch is paused; next tick does not redispatch. |
| Approval `cancel_run` | Same side effects as operator cancel. |
| Workflow invalid with no last valid config | Dispatch is blocked. |
| Workflow invalid after last valid config | Active runs continue with captured snapshot; new dispatch uses last valid config or is blocked according to IS-005. |
| Workspace conflict | Run fails with `workspace_conflict` or `workspace_prepare_failed` per IS-006 mapping; dispatch pauses. |
| Review packet failure | Issue does not enter `Human Review`; run fails with `review_packet_failed`; dispatch pauses. |
| Untracked file created by agent | Review packet `changes.patch` includes new-file content. |
| Issue transitioned while run active | Reconciliation cancels run and keeps workspace. |
| Agent `issue.block` | Issue becomes `Blocked`; run becomes `cancelled` with `agent_blocked`; dispatch pauses. |
| Startup stale active run | Run becomes `failed` with `daemon_restarted_run_interrupted`; dispatch pauses. |
| Rework after Human Review | Same workspace is reused; new review packet is cumulative from `base_sha`; previous packet remains immutable. |

## API contract acceptance

- All success responses use `{ data, meta }`.
- All error responses use `{ error: { code, message, details, request_id } }`.
- `{issue_ref}` accepts both `iss_...` and `LOC-...` and returns the same issue.
- `POST /runs/{run_id}/cancel` schema covers cancellation side effects.
- `POST /approvals/{approval_id}/decide` with `cancel_run` schema covers cancellation side effects.
- SSE `id` equals `run_events.seq`; replay supports `Last-Event-ID` and `after_seq`.
- `GET /artifacts/{id}/content` enforces containment and rejects raw prompt/raw Codex log access in v1.

## DB acceptance

- Fresh app DB and project DB initialize from SQL files.
- `schema_version` contains exactly one row with `version=1`.
- Foreign keys are enabled on every connection.
- WAL, busy timeout, and synchronous mode are configured on every connection.
- Handoff idempotency uses canonical payload hashing and `handoffs.payload_hash`.
- Review packet rows reference prompt snapshots when available.

## Security acceptance

- Non-loopback host is rejected when `require_loopback_api=true`.
- Browser command APIs require valid session cookie and CSRF header.
- CLI command APIs require bearer token.
- Tool gateway rejects expired, revoked, wrong-run, wrong-issue, wrong-cwd, and unauthorized-tool tokens.
- Protected paths such as `.env`, `.ssh/**`, `.aws/**`, `.kube/**`, `.npmrc`, `.pypirc`, and `.netrc` are denied according to `docs/security/SECURITY_MODEL.md`.
- Diagnostics export redaction has golden fixtures.
