# IS-013 — Local v1 vs Upstream SPEC Resolution Rules

## Status

Frozen.

## Goal

Provide the implementation-time conflict resolution contract between upstream `openai/symphony` `SPEC.md` language and Local Symphony App v1 documents.

This document exists because Local v1 is intentionally a local-first product variant, not a byte-for-byte upstream implementation.

## Authority

When implementing Local v1, use this priority order:

```text
1. api/openapi.yaml
2. db/schema/*.sql
3. docs/AGENT_IMPLEMENTATION_GUIDE.md
4. docs/implementation/*.md, including this document
5. docs/schema/*.md
6. docs/config/*.md
7. docs/security/SECURITY_MODEL.md
8. docs/agent/*.md and docs/agent/TASKS.yaml
9. docs/references/spec-conformance-matrix.md for upstream-vs-local ambiguity
10. docs/adr/*.md
11. docs/prd/*.md as product context only
12. Upstream SPEC for compatible behavior not overridden above
```

If upstream SPEC and Local v1 conflict, implement Local v1 only when the difference is marked as compatible extension, product extension, intentional restriction, or intentional deviation in `docs/references/spec-conformance-matrix.md`.

## Required resolution table

| Topic | Upstream behavior | Local v1 implementation rule | Required tests |
|---|---|---|---|
| Tracker | Linear tracker adapter. | `tracker.kind: local` backed by SQLite. No Linear API surface. | Local issue CRUD and dispatch eligibility without Linear config. |
| Active run reconciliation | If an active issue becomes ineligible/terminal, active run is stopped. | Implement reconciliation. When an issue with active run leaves `Ready`, `Working`, or `Rework`, cancel the run process group, set run `status=cancelled`, set `failure_code=issue_state_changed`, keep workspace. | Transition `Working -> Blocked/Cancelled/Duplicate/Done` while fake runner is active. |
| Operator run cancel | Upstream cancellation semantics are runner-level. | Operator cancel and approval `cancel_run` set run `status=cancelled`, `failure_code=operator_cancelled`, revoke tool tokens, keep issue state unchanged, and pause redispatch if issue remains active-state. | Cancel active `Working` run; next tick must not redispatch until operator resumes. |
| Retry/backoff | Exponential backoff and retry queue are part of upstream recovery goals. | No automatic retry queue/timers in v1. Failures pause dispatch. Operator may resume and manually redispatch with `dispatch_reason=retry`. | Failure pauses issue; no automatic redispatch after waiting. |
| Normal continuation | Upstream may continue up to configured max turns. | One main turn plus at most one handoff continuation. | Missing handoff once continues; missing handoff twice pauses. |
| Hooks | `after_create` on new workspace; `before_run` before every run; `after_run` after every run attempt. | Same lifecycle. `before_run` must run on the first run immediately after `after_create` if the workspace was newly created. | Fresh workspace records `after_create` then `before_run`; reused workspace records only `before_run`. |
| NormalizedIssue | Upstream issue shape has top-level `branch_name`. | Local DTO keeps upstream-compatible top-level aliases (`branch_name`, `workspace_path`, `base_ref`, `base_ref_config`, `base_sha`) and also exposes local nested `workspace` and `git` objects. | Prompt using `{{ issue.branch_name }}` and `{{ git.branch_name }}` both render. |
| Workspace key sanitization | Invalid characters replaced with `_`. | Invalid characters replaced with `-`, repeated `-` collapsed, max 80 chars. This is an intentional local deviation. | Identifiers/titles with slash, whitespace, unicode, and long text sanitize deterministically. |
| Handoff target state | Handoff state may be workflow-defined. | v1 only supports target state `Human Review`. Any other configured value is workflow validation error. Handoff submit itself does not transition state. | Config with `agent.handoff_state: Done` fails validation. |
| Review finalizer | Upstream handoff is enough for successful run handoff semantics. | Local v1 requires review packet finalizer success before `Human Review`. Review packet diffs must include untracked new-file content. | Handoff with review generation failure leaves issue not Human Review and pauses dispatch; untracked new file appears in `changes.patch`. |
| Run terminal states | Upstream names include succeeded/failed/timed out/stalled/canceled by reconciliation. | Local stores compact statuses plus canonical `failure_code`. Use mapping in IS-006. | Timeout, stall, operator cancel, and reconciliation cancel produce expected status/code. |
| Terminal workspace cleanup | Upstream startup/reconciliation may clean terminal workspaces. | v1 never deletes, resets, or cleans workspaces automatically. Operator-visible diagnostics only. | Terminal issue keeps workspace and branch. |
| Protected paths | Implementation-defined safety posture. | Use the full default protected path list in IS-009 across config, security engine, and tests. | `.env`, `.ssh/**`, `.aws/**`, `.gcp/**`, `.azure/**`, `.kube/**`, `**/*_rsa`, `**/*_ed25519`, `.npmrc`, `.pypirc`, `.netrc` are protected. |
| Tool CLI command policy | Upstream tool transport may be native/dynamic. | Local v1 allows `symphony tool ...` shell commands by default, but every tool operation is daemon-authorized by token, scope, cwd, and schema. | Handoff command reaches gateway; wrong token still denied. |
| Issue path refs | Upstream tracker id handling is adapter-defined. | Local REST `{issue_ref}` accepts internal id or human identifier and resolves server-side. | `GET /issues/iss_...` and `GET /issues/LOC-1` return same issue. |
| Codex protocol fixtures | Upstream does not define local adapter fixture policy. | Local Codex adapter supports only generated/versioned protocol fixtures; unsupported installed versions fail with `unsupported_codex_version`. | Unsupported fake version fails before run. |
| Workflow reload | Upstream dynamic reload keeps service running. | Running attempts use their captured workflow snapshot. New dispatch uses latest valid config. Invalid reload preserves last valid config; if none exists, dispatch is blocked. | Invalid reload does not crash; active run continues with old snapshot. |
| Clean handoff package | Not specified. | Agent implementation input must not include `.git/`, stale patches, or uncommitted diff artifacts unless explicitly part of the task. | Release/documentation packaging excludes `.git/` and `*.patch`. |

| OpenAPI contract | Upstream does not define Local v1 dashboard API. | `api/openapi.yaml` is the executable source for API shapes; `docs/api/openapi-v1-outline.md` is historical only. | OpenAPI validation and handler/frontend conformance tests. |
| DB schema contract | Upstream does not define Local v1 SQLite schema. | `db/schema/*.sql` is the executable source for DB initialization; Markdown schema is explanatory. | In-memory schema init and unsupported version tests. |
| Rework diff semantics | Upstream does not define local review packet versioning. | Rework reuses workspace and branch; review packet diff is cumulative from `base_sha`, not incremental. | Human Review → Rework → Human Review creates new immutable cumulative packet. |
| Security enforcement boundary | Upstream leaves implementation safety controls to product. | Local v1 distinguishes hard daemon enforcement, Codex-mediated enforcement, and detection-only surfaces. | Security regression suite from `docs/security/SECURITY_MODEL.md`. |

## Implementation guardrails

```text
Do not implement Linear.
Do not implement from PRD files when executable contracts or implementation specs define different behavior.
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
| IS13-007 | operator cancellation pauses redispatch when the issue remains active-state. |
| IS13-008 | review packet diffs must include untracked new-file content. |
| IS13-009 | `symphony tool ...` commands are default-allowed but tool operations remain daemon-authorized. |
| IS13-010 | executable OpenAPI and SQL contracts outrank Markdown explanations. |
| IS13-011 | rework packets are immutable and cumulative from `base_sha`. |
| IS13-012 | security enforcement boundaries must be explicit and tested. |
