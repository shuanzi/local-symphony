# Local Symphony App v1 — Frozen Product & Implementation Docs

**Status:** Frozen for v1 implementation
**Freeze date:** 2026-05-09
**Document authority:** `docs/implementation`, `docs/schema`, `docs/config`, and `docs/api` are the implementation contract for Local Symphony App v1 until generated `api/openapi.yaml` and `db/schema/*.sql` exist. PRDs and ADRs provide product context and decision rationale. Chat history is historical input, not implementation authority.

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
docs/
├── prd/
│   ├── 00-final-frozen-version.md
│   ├── 01-prd.md
│   └── ...
├── adr/
│   ├── ADR-001-product-scope.md
│   ├── ADR-002-local-tracker.md
│   ├── ADR-003-runtime-architecture.md
│   ├── ADR-004-workspace-git.md
│   ├── ADR-005-agent-prompt-tools.md
│   ├── ADR-006-ui-api-observability.md
│   ├── ADR-007-security-baseline.md
│   ├── ADR-008-v1-scope-and-non-goals.md
│   └── ADR-009-decision-register.md
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
│   └── IS-012-testing-release.md
├── backlog/
│   └── m0-m8-mvp-backlog.md
├── config/
│   ├── workflow-reference-v1.md
│   └── starter-WORKFLOW.md
├── api/
│   └── openapi-v1-outline.md
├── schema/
│   ├── app-schema-v1.md
│   ├── project-schema-v1.md
│   └── normalized-issue-v1.md
└── references/
    ├── references.md
    └── spec-conformance-matrix.md
```

## Source-of-truth hierarchy

```text
1. api/openapi.yaml and db/schema/*.sql when implemented
2. docs/implementation/*.md
3. docs/schema/*.md
4. docs/config/starter-WORKFLOW.md and docs/config/workflow-reference-v1.md
5. docs/api/openapi-v1-outline.md until api/openapi.yaml exists
6. docs/references/spec-conformance-matrix.md for upstream SPEC vs local v1 ambiguity
7. docs/adr/*.md
8. docs/prd/*.md as product context only
9. docs/backlog/*.md
10. chat history is historical input only
```

Implementation agents should treat old PRD files as context unless a PRD section explicitly matches the frozen implementation specs.

PRD documents are retained for product context. When a PRD conflicts with implementation/schema/config/API docs, the implementation/schema/config/API docs win.

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
```

## Accepted final amendments

The following implementation amendments are frozen:

| ID | Amendment |
|---|---|
| G1 | Add dispatch pause/resume API and CLI. |
| G2 | Change starter `git.base_ref` to `auto`. |
| G3 | v1 run failure sets `dispatch_paused=true` by default; operator must resume. |
| G4 | startup marks stale running runs as interrupted; no crash recovery. |
| G5 | Handoff is two-stage: tool submission first, review-packet finalizer transitions to Human Review. |
| G6 | PRD files are product context; implementation/schema/config/API documents are authoritative. |
