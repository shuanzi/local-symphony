# Upstream Symphony SPEC Conformance Matrix

## Status

Frozen for Local Symphony App v1.

## Purpose

This document records how Local Symphony App v1 relates to upstream `openai/symphony` `SPEC.md`.

Local v1 is a local-first product implementation inspired by the SPEC. It intentionally extends or deviates from several upstream requirements. Development agents must use this matrix when resolving conflicts between upstream SPEC language and Local v1 documents.

## Matrix

| Area | Upstream SPEC requirement or default | Local Symphony App v1 behavior | Status | Implementation authority |
|---|---|---|---|---|
| Problem shape | Long-running automation service reads work from an issue tracker, creates isolated per-issue workspaces, and runs a coding agent. | Same core shape. | Compatible | `docs/implementation/IS-006-orchestrator-run-lifecycle.md` |
| Tracker | Current SPEC version uses `tracker.kind: linear` and Linear adapter fields. | Uses `tracker.kind: local` backed by SQLite; does not simulate Linear API. | Intentional deviation | `docs/adr/ADR-002-local-tracker.md`, `docs/schema/project-schema-v1.md` |
| Tracker writes | Symphony is primarily scheduler/runner/tracker reader; ticket writes are usually done by the coding agent through runtime tools. | Agent writes only through restricted `symphony tool` gateway; daemon persists local issue/comment/handoff records. | Compatible extension | `docs/implementation/IS-004-cli-tool-gateway.md` |
| Normalized issue | SPEC normalized issue includes `id`, `identifier`, `title`, `description`, `priority`, `state`, `branch_name`, `url`, lowercase `labels`, `blocked_by`, `created_at`, `updated_at`. | API/prompt DTO must expose a compatible normalized issue. Git/workspace fields are joined from `workspaces`; labels are lowercase; blockers are derived from `issue_relations`. | Compatible extension | `docs/schema/normalized-issue-v1.md`, `docs/schema/project-schema-v1.md` |
| Workflow format | `WORKFLOW.md` with optional YAML front matter and Markdown prompt body. Unknown keys are ignored for forward compatibility. | Same format; Local v1 adds documented extension sections (`git`, `approvals`, `tools`, `security`, `observability`, `server`, `ui`, `prompt`). Unknown keys warn; invalid known keys block dispatch. | Compatible extension | `docs/config/workflow-reference-v1.md`, `docs/implementation/IS-005-workflow-prompt.md` |
| Config interpolation | SPEC supports environment variable indirection; prompt body is the template. | Config fields do not support Liquid interpolation. Only full-string `$VAR_NAME` is expanded. Prompt body supports Liquid-style variables. | Compatible extension | `docs/config/workflow-reference-v1.md` |
| Workspace | Deterministic per-issue workspace under configured workspace root; agent runs only in workspace cwd. | Uses one git worktree and stable branch per issue under global workspace root. | Compatible extension | `docs/implementation/IS-007-workspace-git-implementation.md` |
| Workspace key sanitization | Invalid characters in workspace key are replaced with `_`. | Invalid characters are replaced with `-`, repeated `-` is collapsed, and key length is capped at 80 characters. | Intentional deviation | `docs/implementation/IS-007-workspace-git-implementation.md` |
| Workspace hooks | `after_create` runs only when a workspace is newly created; `before_run` runs before each attempt; `after_run` runs after each attempt. | Same lifecycle. For a new workspace, `after_create` and then `before_run` both run before the first Codex process starts. | Compatible, must implement | `docs/implementation/IS-006-orchestrator-run-lifecycle.md`, `docs/implementation/IS-007-workspace-git-implementation.md` |
| Terminal workspace cleanup | SPEC cleans terminal-state workspaces during startup/reconciliation. | v1 does not automatically delete/reset workspaces; destructive cleanup is deferred and requires future snapshot/audit controls. | Intentional deviation | `docs/implementation/IS-007-workspace-git-implementation.md` |
| Orchestrator state | Single authoritative scheduler state for dispatch/retries/reconciliation. | Single authoritative orchestrator actor plus durable local DB run/event records. | Compatible extension | `docs/implementation/IS-006-orchestrator-run-lifecycle.md` |
| Active run reconciliation | Active runs are reconciled when their issue becomes terminal, inactive, or otherwise ineligible. | v1 must cancel active run process groups when an issue leaves active states while a run is active; workspace is retained. | Compatible, must implement | `docs/implementation/IS-006-orchestrator-run-lifecycle.md` |
| Run terminal states | SPEC distinguishes success, failure, timeout, stall, and cancellation by reconciliation. | v1 stores `completed`, `completed_without_handoff`, `failed`, or `cancelled` plus canonical `failure_code`. | Compatible mapping | `docs/implementation/IS-006-orchestrator-run-lifecycle.md` |
| Retry/backoff | SPEC goal includes transient failure recovery with exponential backoff and retry queue state. | v1 has no automatic retry queue/timers. Failures set `dispatch_paused=true`; operator must resume/re-dispatch. | Intentional deviation | `docs/implementation/IS-006-orchestrator-run-lifecycle.md` |
| Normal continuation | SPEC may start additional turns up to `agent.max_turns`; normal worker exit schedules a short continuation retry. | v1 runs one main turn plus at most one handoff continuation. Missing handoff pauses dispatch. | Intentional deviation | `docs/implementation/IS-006-orchestrator-run-lifecycle.md`, `docs/implementation/IS-008-codex-adapter-approval-bridge.md` |
| Handoff state | A successful run can end in workflow-defined handoff state, not necessarily `Done`. | `handoff.submit` only records handoff data. The run finalizer must generate a review packet; only then issue state becomes `Human Review`. v1 rejects any configured handoff target other than `Human Review`. | Intentional restriction + compatible extension | `docs/implementation/IS-004-cli-tool-gateway.md`, `docs/implementation/IS-010-review-packet-artifacts.md` |
| Protected paths | SPEC requires a documented safety posture; exact protected path list is implementation-defined. | v1 uses the IS-009 default protected path list consistently across starter workflow, parser defaults, policy engine, and tests. | Compatible extension | `docs/implementation/IS-009-security-policy-engine.md`, `docs/config/starter-WORKFLOW.md` |
| Workflow reload | SPEC supports dynamic workflow reload without crashing. | Running attempts use their captured workflow snapshot; new attempts use latest valid config; invalid reload preserves last valid config and blocks dispatch only when none exists. | Compatible extension | `docs/implementation/IS-005-workflow-prompt.md` |
| HTTP UI/API | SPEC treats status surface/dashboard/API as optional extensions. | Local dashboard, REST API, and SSE are core v1 product surfaces, but orchestrator correctness must not depend on UI. | Product extension | `docs/implementation/IS-003-openapi-v1.md`, `docs/implementation/IS-011-frontend-dashboard.md` |
| Codex protocol | SPEC defers protocol details to the targeted Codex app-server version/schema. | Adapter is isolated under `internal/agent/codex`; v1 target is stdio transport for the selected Codex version, with schema/protocol validation in tests. | Compatible extension | `docs/implementation/IS-008-codex-adapter-approval-bridge.md` |
| Safety posture | SPEC requires implementations to document trust/safety posture but does not mandate one global policy. | v1 uses `balanced-secure` local baseline: loopback API, session token, CSRF, run-scoped tool token, command/network policy, redaction. | Compatible extension | `docs/implementation/IS-009-security-policy-engine.md` |

## Resolution rule

When upstream SPEC and Local v1 differ, implement Local v1 behavior only when this matrix marks the difference as a compatible extension, product extension, intentional restriction, or intentional deviation. Otherwise treat the mismatch as a documentation bug and update this matrix before implementation.

`docs/implementation/IS-013-local-vs-upstream-resolution.md` is the implementation checklist for these rows and defines the required tests for high-risk deviations.
