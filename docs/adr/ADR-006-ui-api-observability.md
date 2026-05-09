# ADR-006 — UI, API, and Observability

## Status

Frozen.

## Decision

React Dashboard is a control surface only. It does not participate in orchestrator correctness and never directly accesses SQLite, Git, filesystem, Codex, or tool gateway.

UI uses:

```text
REST /api/v1
SSE /api/v1/events/stream
Artifact API for controlled artifact content
```

Main pages:

```text
Overview
Board
Issue Detail
Run Detail
Approval Inbox
Review Packet
Workflow
Diagnostics
```

Durable `run_events` drive timelines and SSE replay.

## API principles

```text
Query endpoints read state.
Command endpoints mutate state.
Issue state changes do not use generic PATCH.
Review API is the Human Review decision entrypoint.
Done requires latest generated review packet.
```

## Rationale

Separating UI from correctness allows daemon, CLI, and future desktop shell to operate reliably even if the browser UI closes or crashes.
