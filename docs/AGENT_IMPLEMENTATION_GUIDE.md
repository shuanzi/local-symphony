# Agent Implementation Guide

## Purpose

This is the entrypoint for implementation agents building Local Symphony App v1. It converts the product documentation set into an implementation contract.

Local Symphony App v1 is a **local-first product variant** inspired by the upstream Symphony SPEC. It is not “upstream Symphony minus Linear.” Implement Local v1 behavior only.

## Source-of-truth order

Use this order whenever documents conflict:

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

`docs/prd/*.md` files are background. Do not implement API, schema, CLI, state, or security details from PRD files when higher-ranked files define a contract.

## Mandatory implementation posture

Implement these Local v1 choices even if upstream language suggests otherwise:

```text
tracker.kind = local only
no Linear adapter or Linear API dependency
no automatic retry queue/timers
no automatic PR creation, git push, merge, or publish
no automatic workspace delete/reset/clean
no crash recovery beyond stale active-run interruption
no remote dashboard
no dynamic tools/MCP in v1
handoff target is fixed to Human Review
handoff.submit never transitions the issue by itself
review packet generation is required before Human Review
run failures pause dispatch until operator resumes
Codex integration is version-fixture gated
```

## Implementation phases

### M0 — Contracts and scaffolding

Deliver:

```text
api/openapi.yaml is valid and used by generated clients/types
DB schema init uses db/schema/app_v1.sql and db/schema/project_v1.sql
storage contract in IS-014 is implemented or converted to tests
Codex fixture gate in IS-015 is implemented in fake form
acceptance test commands are wired, even if some tests initially fail before later phases
```

Do not start frontend type generation or handler implementation from `docs/api/openapi-v1-outline.md`. That file is retained only as a historical outline.

### M1 — Local tracker and store

Deliver issue CRUD, identifiers, blockers, comments, state history, DB version checks, WAL/busy timeout, and single-daemon project lock behavior.

### M2 — Workspace and workflow

Deliver WORKFLOW parsing, validation, prompt rendering, workspace creation/reuse, hooks, branch naming, and path containment.

### M3 — Orchestrator and fake runner

Deliver single actor scheduler, claim transaction, active run reconciliation, cancellation, failure pause, and fake-agent E2E scenarios.

### M4 — Tool gateway and handoff

Deliver run-scoped tool tokens, fixed registry, handoff payload hashing/idempotency, artifact attach, followup create, issue.block, and two-stage handoff semantics.

### M5 — Review packet

Deliver prompt snapshot artifacts, review packet generation, cumulative patch from `base_sha`, inclusion of untracked files, and Human Review gate.

### M6 — API, CLI, and dashboard

Deliver REST/SSE API against `api/openapi.yaml`, operator CLI, and dashboard views for issue queue, running runs, approvals, review, events, diagnostics, and workflow reload.

### M7 — Codex adapter

Deliver version-fixture-gated Codex app-server integration, approval bridge, timeout handling, cancellation, event normalization, and opt-in real Codex tests.

### M8 — Release hardening

Deliver security regression tests, contract tests, release build, quickstart, known limitations, redacted diagnostics, and final Definition of Done.

## Required acceptance gate

A release candidate is not complete until `docs/agent/DEFINITION_OF_DONE.md` is satisfied and the acceptance cases in `docs/agent/ACCEPTANCE.md` pass or are explicitly marked unavailable because the implementation phase has not reached them.

Default CI must not call a real Codex binary. Real Codex tests are opt-in only.

## Conflict resolution rules

1. If upstream SPEC behavior conflicts with Local v1 docs, implement Local v1.
2. If OpenAPI conflicts with an implementation Markdown file for request/response shape, OpenAPI wins and the Markdown must be corrected.
3. If SQL files conflict with schema Markdown, SQL files win and the Markdown must be corrected.
4. If PRD conflicts with any contract/spec file, ignore the PRD detail.
5. If a behavior is missing from all high-ranked documents, add a focused implementation spec or ADR before implementing broad new behavior.

## Implementation guardrails

```text
Never store raw bearer/session/tool tokens.
Never expose raw prompt or raw Codex logs through v1 API.
Never allow artifact path traversal outside .symphony/artifacts or .symphony/exports.
Never allow agent tools to mark Done, create PRs, push, delete workspaces, or mutate project settings.
Never let a run remain active after operator cancellation, approval cancel_run, startup stale-run guard, or issue-state reconciliation.
Never auto-redispatch a failed or cancelled issue while dispatch_paused=true.
```

## Minimum generated artifacts

The implementation should generate or validate the following from source-of-truth files:

```text
frontend API types from api/openapi.yaml
API handler contract tests from api/openapi.yaml
SQLite init from db/schema/app_v1.sql and db/schema/project_v1.sql
prompt/tool manifest from fixed tool registry
Codex protocol adapter fixtures from the selected supported Codex app-server schema
```

## Documentation update rule

When implementation discovers an ambiguity, update the smallest authoritative document needed:

```text
API ambiguity      → api/openapi.yaml and IS-003
DB ambiguity       → db/schema/*.sql and schema Markdown
lifecycle ambiguity → IS-006 or IS-016
Codex ambiguity    → IS-015
security ambiguity → docs/security/SECURITY_MODEL.md and IS-009
acceptance ambiguity → docs/agent/ACCEPTANCE.md
```
