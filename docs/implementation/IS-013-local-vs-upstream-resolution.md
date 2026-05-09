# IS-013 — Local v1 vs Upstream SPEC Resolution Rules

## Status

Frozen.

## Goal

Provide the implementation-time conflict resolution contract between upstream `openai/symphony` `SPEC.md` language and Local Symphony App v1 documents.

This document exists because Local v1 is intentionally a local-first product variant, not a byte-for-byte upstream implementation.

## Authority

When implementing Local v1, use this priority order:

```text
1. Generated api/openapi.yaml and db/schema/*.sql, once implemented
2. docs/implementation/*.md, including this document
3. docs/schema/*.md
4. docs/config/*.md
5. docs/references/spec-conformance-matrix.md for upstream-vs-local ambiguity
6. docs/adr/*.md
7. docs/prd/*.md as product context only
8. Upstream SPEC for compatible behavior not overridden above
```

If upstream SPEC and Local v1 conflict, implement Local v1 only when the difference is marked as compatible extension, product extension, intentional restriction, or intentional deviation in `docs/references/spec-conformance-matrix.md`.

## Required resolution table

| Topic | Upstream behavior | Local v1 implementation rule | Required tests |
|---|---|---|---|
| Tracker | Linear tracker adapter. | `tracker.kind: local` backed by SQLite. No Linear API surface. | Local issue CRUD and dispatch eligibility without Linear config. |
| Active run reconciliation | If an active issue becomes ineligible/terminal, active run is stopped. | Implement reconciliation. When an issue with active run leaves `Ready`, `Working`, or `Rework`, cancel the run process group, set run `status=cancelled`, set `failure_code=issue_state_changed`, keep workspace. | Transition `Working -> Blocked/Cancelled/Duplicate/Done` while fake runner is active. |
| Retry/backoff | Exponential backoff and retry queue are part of upstream recovery goals. | No automatic retry queue/timers in v1. Failures pause dispatch. Operator may resume and manually redispatch with `dispatch_reason=retry`. | Failure pauses issue; no automatic redispatch after waiting. |
| Normal continuation | Upstream may continue up to configured max turns. | One main turn plus at most one handoff continuation. | Missing handoff once continues; missing handoff twice pauses. |
| Hooks | `after_create` on new workspace; `before_run` before every run; `after_run` after every run attempt. | Same lifecycle. `before_run` must run on the first run immediately after `after_create` if the workspace was newly created. | Fresh workspace records `after_create` then `before_run`; reused workspace records only `before_run`. |
| NormalizedIssue | Upstream issue shape has top-level `branch_name`. | Local DTO keeps upstream-compatible top-level aliases (`branch_name`, `workspace_path`, `base_ref`, `base_sha`) and also exposes local nested `workspace` and `git` objects. | Prompt using `{{ issue.branch_name }}` and `{{ git.branch_name }}` both render. |
| Workspace key sanitization | Invalid characters replaced with `_`. | Invalid characters replaced with `-`, repeated `-` collapsed, max 80 chars. This is an intentional local deviation. | Identifiers/titles with slash, whitespace, unicode, and long text sanitize deterministically. |
| Handoff target state | Handoff state may be workflow-defined. | v1 only supports target state `Human Review`. Any other configured value is workflow validation error. Handoff submit itself does not transition state. | Config with `agent.handoff_state: Done` fails validation. |
| Review finalizer | Upstream handoff is enough for successful run handoff semantics. | Local v1 requires review packet finalizer success before `Human Review`. | Handoff with review generation failure leaves issue not Human Review and pauses dispatch. |
| Run terminal states | Upstream names include succeeded/failed/timed out/stalled/canceled by reconciliation. | Local stores compact statuses plus canonical `failure_code`. Use mapping in IS-006. | Timeout, stall, operator cancel, and reconciliation cancel produce expected status/code. |
| Terminal workspace cleanup | Upstream startup/reconciliation may clean terminal workspaces. | v1 never deletes, resets, or cleans workspaces automatically. Operator-visible diagnostics only. | Terminal issue keeps workspace and branch. |
| Protected paths | Implementation-defined safety posture. | Use the full default protected path list in IS-009 across config, security engine, and tests. | `.env`, `.ssh/**`, `.aws/**`, `.kube/**`, `.npmrc`, `.pypirc`, `.netrc` are protected. |
| Workflow reload | Upstream dynamic reload keeps service running. | Running attempts use their captured workflow snapshot. New dispatch uses latest valid config. Invalid reload preserves last valid config; if none exists, dispatch is blocked. | Invalid reload does not crash; active run continues with old snapshot. |
| Clean handoff package | Not specified. | Agent implementation input must not include `.git/`, stale patches, or uncommitted diff artifacts unless explicitly part of the task. | Release/documentation packaging excludes `.git/` and `*.patch`. |

## Implementation guardrails

```text
Do not implement Linear.
Do not add automatic PR creation, git push, merge, backup, migration, or destructive workspace cleanup.
Do not infer behavior from old PRD sections when implementation/schema/config/API docs define a different rule.
Do not treat `handoff.submit` as issue completion.
Do not treat dashboard/API as the orchestrator source of truth.
```

## Frozen decisions

| ID | Decision |
|---|---|
| IS13-001 | Local v1 is a documented product variant, not a byte-for-byte upstream implementation. |
| IS13-002 | Conformance matrix plus implementation docs resolve upstream conflicts. |
| IS13-003 | Active run reconciliation is required in v1. |
| IS13-004 | `Human Review` is the only supported v1 handoff target state. |
| IS13-005 | Upstream-compatible NormalizedIssue aliases are required. |
| IS13-006 | Dirty packaging artifacts must be excluded from implementation handoff bundles. |
