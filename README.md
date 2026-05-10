# Local Symphony App v1 — Frozen Product & Implementation Docs

**Status:** Frozen for v1 implementation
**Freeze date:** 2026-05-09
**Document authority:** `docs/AGENT_IMPLEMENTATION_GUIDE.md` defines the implementation-time source-of-truth order. `api/openapi.yaml` and `db/schema/*.sql` are now the executable API and database contracts. PRDs and ADRs provide product context and decision rationale only.

## Product definition

Local Symphony App v1 is a local-first agent engineering workflow control plane inspired by the OpenAI Symphony SPEC. It is **not** a byte-for-byte implementation of that SPEC: `tracker.kind: local`, git worktrees, the local dashboard/API, the CLI tool gateway, review packets, and manual failure pause/resume are intentional Local v1 extensions or deviations. See `docs/references/spec-conformance-matrix.md` before implementing.

```text
Go daemon
+ React/TypeScript dashboard
+ SQLite local tracker
+ git worktree workspace manager
+ Codex app-server runner
+ CLI/IPC tool gateway
+ two-stage handoff submit/finalize
+ review packet
+ REST/SSE API
+ balanced-secure local security baseline
```

The v1 goal is to establish a reliable, observable, reviewable local agent workflow, not maximum automation.

## Implementation entrypoint for agents

Implementation agents must start here:

```text
docs/AGENT_IMPLEMENTATION_GUIDE.md
```

That file defines the authoritative document order, forbidden upstream behaviors, phase sequence, acceptance gate, and conflict-resolution rules for implementation work. Do not implement directly from `docs/prd/*.md`.

## Relationship to upstream Symphony SPEC

Local Symphony App v1 is **not** a direct implementation of upstream `openai/symphony` `SPEC.md`. It is a local-first implementation inspired by the SPEC, with documented extensions and deviations: local SQLite tracker, git worktree workspaces, localhost dashboard/API, CLI/IPC tool gateway, review packet finalization, and manual failure pause/resume.

Use `docs/references/spec-conformance-matrix.md` to distinguish:

```text
SPEC-compatible behavior
intentional Local v1 extension
intentional Local v1 deviation
```

## Frozen v1 main path

```text
symphony init
  ↓
Create local issue
  ↓
Issue → Ready
  ↓
Manual or orchestrator dispatch
  ↓
Create git worktree + branch
  ↓
Start Codex app-server
  ↓
Codex works inside workspace
  ↓
Codex calls symphony tool handoff
  ↓
Run after_run hook executes if workspace exists
  ↓
Run finalizer generates review packet
  ↓
Issue → Human Review
  ↓
Human review
  ├── Send to Rework
  └── Mark Done
```

## Document map

```text
api/
└── openapi.yaml                         # executable API contract

db/
└── schema/
    ├── app_v1.sql                       # executable app DB contract
    └── project_v1.sql                   # executable project DB contract

docs/
├── AGENT_IMPLEMENTATION_GUIDE.md        # implementation entrypoint
├── agent/
│   ├── ACCEPTANCE.md
│   ├── DEFINITION_OF_DONE.md
│   └── TASKS.yaml
├── implementation/
│   ├── IS-001-repo-structure.md
│   ├── IS-002-sqlite-schema-v1.md
│   ├── IS-003-openapi-v1.md
│   ├── IS-004-cli-tool-gateway.md
│   ├── IS-005-workflow-prompt.md
│   ├── IS-006-orchestrator-run-lifecycle.md
│   ├── IS-007-workspace-git-implementation.md
│   ├── IS-008-codex-adapter-approval-bridge.md
│   ├── IS-009-security-policy-engine.md
│   ├── IS-010-review-packet-artifacts.md
│   ├── IS-011-frontend-dashboard.md
│   ├── IS-012-testing-release.md
│   ├── IS-013-local-vs-upstream-resolution.md
│   ├── IS-014-store-contract.md
│   ├── IS-015-codex-protocol-fixture.md
│   └── IS-016-rework-flow.md
├── security/
│   └── SECURITY_MODEL.md
├── user/
│   ├── QUICKSTART.md
│   └── KNOWN_LIMITATIONS.md
├── backlog/
│   └── m0-m8-mvp-backlog.md
├── config/
│   ├── workflow-reference-v1.md
│   └── starter-WORKFLOW.md
├── api/
│   └── openapi-v1-outline.md            # historical outline; do not supersede api/openapi.yaml
├── schema/
│   ├── app-schema-v1.md
│   ├── project-schema-v1.md
│   └── normalized-issue-v1.md
├── references/
│   ├── references.md
│   └── spec-conformance-matrix.md
├── adr/
│   └── ...
└── prd/
    └── ...                              # product background only
```

## Source-of-truth hierarchy

Use this order for all implementation decisions:

```text
1. api/openapi.yaml
2. db/schema/*.sql
3. docs/AGENT_IMPLEMENTATION_GUIDE.md
4. docs/implementation/*.md
5. docs/schema/*.md
6. docs/config/*.md
7. docs/security/SECURITY_MODEL.md
8. docs/agent/*.md and docs/agent/TASKS.yaml
9. docs/references/spec-conformance-matrix.md and docs/implementation/IS-013-local-vs-upstream-resolution.md
10. docs/adr/*.md
11. docs/prd/*.md as product context only
12. docs/backlog/*.md
13. chat history as historical input only
```

When two documents conflict, implement the highest-ranked document above. PRD documents are retained for product context and must not override implementation, schema, config, API, security, or acceptance documents.

## Frozen v1 non-goals

v1 intentionally does **not** include:

```text
Tauri desktop shell
automatic PR / merge
agent automatic commit
automatic SQLite backup
production migration / rollback flow
automatic retry queue/timers
crash recovery
full audit log
supply-chain deep risk policy
dynamic tools / MCP
multi-tenant RBAC
remote dashboard
Linear tracker dependency
```

## Accepted final amendments

The following implementation amendments are frozen:

| ID | Amendment |
|---|---|
| G1 | Add dispatch pause/resume API and CLI. |
| G2 | Change starter `git.base_ref` to `auto`. |
| G3 | v1 run failure sets `dispatch_paused=true` by default; operator must resume. |
| G4 | startup marks stale running runs as interrupted; no crash recovery. |
| G5 | Handoff is two-stage: tool submission first, after_run hook, review-packet finalizer, then Human Review. |
| G6 | PRD files are product context; implementation/schema/config/API/security/acceptance documents are authoritative. |
| G7 | Active run reconciliation is required; non-active issue transitions cancel active runs. |
| G8 | v1 only supports `Human Review` as the handoff target state. |
| G9 | NormalizedIssue keeps upstream-compatible top-level git/workspace aliases. |
| G10 | `api/openapi.yaml` and `db/schema/*.sql` are executable contracts and must be used for generated types, handlers, migrations, and contract tests. |
| G11 | Codex app-server integration is version-fixture gated; unsupported versions fail before dispatch. |
| G12 | Rework review packets are cumulative from workspace `base_sha` and previous packets remain immutable. |
