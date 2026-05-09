# IS-011 — React Dashboard Implementation

## Status

Frozen.

## Goal

Define v1 dashboard routes, frontend state model, API client behavior, and page responsibilities.

The dashboard is a control surface only. It never directly accesses SQLite, Git, filesystem, Codex, or Tool Gateway.

## Routes

```text
/                  Overview
/issues            Board
/issues/:issueId   Issue Detail
/runs/:runId       Run Detail
/approvals         Approval Inbox
/reviews/:issueId  Review Packet
/workflow          Workflow
/diagnostics       Diagnostics
```

## API client

Generated:

```text
web/src/api/generated/*
```

Handwritten:

```text
web/src/api/client.ts
web/src/api/events.ts
web/src/api/errors.ts
```

Responsibilities:

```text
base URL
session/CSRF
envelope unwrap
error.code mapping
SSE reconnect
Last-Event-ID
query invalidation
```

## State model

```text
server state: query cache
live updates: SSE reducer + query invalidation
form state: local component state
global UI state: minimal preferences only
```

Do not mirror orchestrator state in frontend.

## Pages

### Overview

Shows:

```text
workflow status
running runs
pending approvals
failed runs
Human Review count
dispatch paused issues
Codex availability
recent events
```

### Board

Columns:

```text
Inbox
Ready
Working
Rework
Blocked
Human Review
Done
Cancelled
Duplicate
```

Actions:

```text
create issue
transition state
dispatch
open review
open run
```

### Issue Detail

Shows:

```text
issue facts
comments
blockers
workspace
run history
review packets
dispatch paused state
```

Must include dispatch resume if paused.

### Run Detail

Shows normalized timeline:

```text
workspace prepared
prompt rendered
Codex started
approval requested
tool called
handoff submitted
review generated
failure
```

Raw Codex log is not the main UI.

### Approval Inbox

Shows:

```text
command/file/network approvals
risk level
policy match
approve/deny/cancel controls
```

### Review Packet

Shows:

```text
summary
tests
risks
verification
changed files
diff
tool calls
approvals
Send to Rework
Mark Done
```

Mark Done is gated by API result, not local UI assumptions.

### Workflow

Shows:

```text
current validation
last valid config
warnings/errors
reload button
render preview for selected issue
```

### Diagnostics

Shows:

```text
daemon
project paths
Codex
Git
DB
workflow
redacted export
```

## Mutation rule

All UI mutations call command APIs:

```text
transition
dispatch
cancel
approval decide
send to rework
mark done
workflow reload
diagnostics export
dispatch pause/resume
```

No direct optimistic mutation of final state. UI may show pending UI state but must refetch or consume SSE to confirm.

## Frozen decisions

| ID | Decision |
|---|---|
| IS11-001 | React uses REST + SSE only |
| IS11-002 | routes match product surfaces |
| IS11-003 | generated API types from OpenAPI |
| IS11-004 | SSE invalidates/refetches server state |
| IS11-005 | UI does not mirror orchestrator state |
| IS11-006 | Issue page exposes dispatch resume |
| IS11-007 | Review page gates Mark Done by API |
| IS11-008 | Diagnostics export redacted only |
