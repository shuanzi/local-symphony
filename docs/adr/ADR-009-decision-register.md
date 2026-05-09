# ADR-009 — Decision Register

## Status

Frozen.

This register summarizes the frozen decisions across product, architecture, implementation, and final amendments.

## Product decisions

| ID | Decision |
|---|---|
| D0 | Product is local tracker + orchestrator + workspace manager + agent runner + review dashboard. |
| D1 | SQLite is local tracker source of truth. |
| D2 | Markdown/JSON are import/export aids, not source of truth. |
| D3 | Use `tracker.kind: local`; do not simulate Linear API. |
| D4 | State machine includes Inbox, Ready, Working, Rework, Blocked, Human Review, Done, Cancelled, Duplicate. |
| D5 | Active states: Ready, Working, Rework. |
| D6 | Terminal states: Done, Cancelled, Duplicate. |
| D7 | Agent writes through restricted local tracker tools. |
| D8 | UI is localhost dashboard + CLI fallback. |
| D9 | GitHub/GitLab/Gitea are optional Git providers later, not v1 tracker. |
| D10 | v1 is single-machine local mode. |

## Runtime decisions

| ID | Decision |
|---|---|
| D11 | Backend: Go. |
| D12 | Frontend: React/TypeScript; v1 localhost dashboard; v2 Tauri + Go sidecar. |
| D13 | Single daemon, multiple Codex subprocesses. |
| D14 | Orchestrator is single authoritative actor. |
| D15 | Global app DB + project DB. |
| D16 | Persist run metadata; v1 later narrowed to no full crash recovery. |
| D17 | Codex adapter is version-aware; protocol framing/schema are isolated inside `internal/agent/codex`. |
| D18 | Tool gateway uses CLI + local IPC + run-scoped token. |
| D19 | REST + SSE API. |
| D20 | Structured logs + durable events + dashboard timeline. |
| D21 | Default approval mode: balanced. |
| D22 | Project facts in repo `.symphony`; workspaces in global workspace root. |

## Workspace/Git decisions

| ID | Decision |
|---|---|
| D23 | v1 project = one local Git repo. |
| D24 | workspace technology = git worktree. |
| D25 | one stable branch per issue. |
| D26 | base ref supports `auto`. |
| D27 | same issue reuses same workspace. |
| D28 | no automatic destructive reset. |
| D29 | preserve hooks with Git-aware preflight. |
| D30 | agent can edit workspace files but cannot push/PR. |
| D31 | agent does not commit by default. |
| D32 | every completed run attempts review packet generation. |
| D33 | publish is manual and deferred. |
| D34 | Git provider optional and not tracker. |
| D35 | no Human Review cleanup before review; v1 destructive cleanup deferred. |
| D36 | no automatic rebase of dirty workspace. |
| D37 | submodules disabled by default. |

## Agent and prompt decisions

| ID | Decision |
|---|---|
| D38 | v1 only supports Codex app-server. |
| D39 | one app-server subprocess per run. |
| D40 | one main turn plus at most one handoff continuation. |
| D41 | prompt = Runtime Envelope + Workflow Prompt + Context Pack. |
| D42 | runtime envelope is system-generated and cannot be overridden. |
| D43 | prompt context variables are fixed. |
| D44 | each run stores redacted prompt snapshot. |
| D45 | tool gateway v1 is `symphony tool` CLI + local IPC. |
| D46 | tool auth uses run-scoped capability token. |
| D47 | handoff is an atomic submission tool; review-packet finalizer performs Human Review transition. |
| D48 | tool registry defines schemas, permissions, and logging. |
| D49 | approval mode defaults to balanced. |
| D50 | network default deny. |
| D51 | sensitive path protection. |
| D52 | dynamic tools/MCP deferred. |
| D53 | tool manifest injected into prompt from registry. |
| D54 | review packet uses handoff as summary source. |
| D55 | failures classified by prompt/protocol/approval/tool/handoff/workspace. |

## UI/API/Observability decisions

| ID | Decision |
|---|---|
| D56 | UI is control surface, not correctness layer. |
| D57 | UI pages: Overview, Board, Issue, Run, Approval, Review, Workflow, Diagnostics. |
| D58 | Overview is operator cockpit. |
| D59 | Board is local tracker entrypoint. |
| D60 | Issue Detail is task facts page. |
| D61 | Run Detail is agent execution trace. |
| D62 | Approval Inbox is first-class page. |
| D63 | Review Packet is Human Review core page. |
| D64 | Workspace/Git page handles safe publish later. |
| D65 | Workflow page displays raw/parsed/effective config. |
| D66 | Diagnostics is mandatory. |
| D67 | API uses `/api/v1` REST with query/command split. |
| D68 | Realtime uses SSE. |
| D69 | Durable event store drives timelines. |
| D70 | logging layers: app, run, raw Codex, lightweight events. |
| D71 | redaction by default. |
| D72 | retention policy exists conceptually; v1 raw export limited. |
| D73 | key UI actions have CLI equivalent. |
| D74 | localhost also requires session token. |
| D75 | frontend organized by product surface. |
| D76 | OpenAPI + generated TypeScript client. |

## Security decisions

| ID | Decision |
|---|---|
| D77 | threat model: local but not fully trusted. |
| D78 | default mode: balanced-secure. |
| D79 | capability-based security; no v1 RBAC. |
| D80 | loopback-only API + session + CSRF. |
| D81 | Tauri v2 will use minimal capabilities. |
| D82 | v1 does not store third-party long-lived secrets. |
| D83 | agent uses minimal env allowlist. |
| D84 | workspace is default writable boundary. |
| D85 | sensitive paths protected. |
| D86 | commands are allow/review/deny. |
| D87 | network default deny. |
| D88 | hooks are trusted config but bounded. |
| D89 | tool gateway uses run-scoped token. |
| D90 | agent has no publish permission. |
| D91 | redaction default; raw access deferred. |
| D92 | automatic backup deferred from v1. |
| D93 | migration/upgrade production flow deferred from v1. |
| D94 | crash recovery deferred from v1. |
| D95 | supply-chain deep policy deferred from v1. |
| D96 | v1 Local Mode only. |
| D97 | full audit log deferred from v1. |
| D98 | security tests cover adopted v1 controls. |

## MVP decisions

| ID | Decision |
|---|---|
| D99 | v1 MVP is local tracker + worktree + Codex run + handoff + review packet + dashboard. |
| D100 | v1 main path is issue → Ready → dispatch → Codex → handoff → review packet → Human Review → Done/Rework. |
| D101 | milestones M0–M8. |
| D102 | non-goals are explicitly documented. |
| D103 | v1 data model covers tracker, runs, events, approvals, tools, handoff, review, workflow/prompt snapshots. |
| D104 | v1 API is minimal REST + SSE. |
| D105 | v1 CLI includes init/serve/issue/run/review/workflow/diagnostics/tool. |
| D106 | full E2E main path is acceptance gate. |
| D107 | v1.1 priorities: migration → backup → crash recovery → audit. |
| D108 | v1.2 priorities: supply-chain policy, desktop shell, Git provider publish. |

## Final amendments

| ID | Amendment |
|---|---|
| G1 | Add dispatch pause/resume API and CLI. |
| G2 | starter `git.base_ref` becomes `auto`. |
| G3 | v1 run failure sets `dispatch_paused=true` by default; operator must resume. |
| G4 | startup marks stale running runs as interrupted; no crash recovery. |
| G5 | Handoff is two-stage: tool submission first, review-packet finalizer transitions to Human Review. |
| G6 | PRD files are product context; implementation/schema/config/API documents are authoritative. |
| G7 | Active run reconciliation is required; non-active issue transitions cancel active runs. |
| G8 | v1 only supports `Human Review` as the handoff target state. |
| G9 | NormalizedIssue keeps upstream-compatible top-level git/workspace aliases. |
