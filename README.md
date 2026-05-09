# Local Symphony App v1 — Frozen Product & Implementation Docs

**Status:** Frozen for v1 implementation  
**Freeze date:** 2026-05-09  
**Document authority:** These Markdown documents are the source of truth for v1 implementation. Chat history is considered historical input, not the implementation authority.

## Product definition

Local Symphony App v1 is a local-first agent engineering workflow control plane:

```text
Go daemon
+ React/TypeScript dashboard
+ SQLite local tracker
+ git worktree workspace manager
+ Codex app-server runner
+ CLI/IPC tool gateway
+ atomic handoff
+ review packet
+ REST/SSE API
+ balanced-secure local security baseline
```

The v1 goal is to establish a reliable, observable, reviewable local agent workflow, not maximum automation.

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
Generate review packet
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
│   └── local-symphony-v1-prd.md
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
│   └── project-schema-v1.md
└── references/
    └── references.md
```

## Source-of-truth hierarchy

```text
1. docs/implementation/*.md
2. docs/adr/*.md
3. docs/prd/*.md
4. docs/backlog/*.md
5. api/openapi.yaml when implemented
6. db/schema/*.sql when implemented
7. chat history
```

## Frozen v1 non-goals

v1 intentionally does **not** include:

```text
Tauri desktop shell
automatic PR / merge
agent automatic commit
automatic SQLite backup
production migration / rollback flow
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
