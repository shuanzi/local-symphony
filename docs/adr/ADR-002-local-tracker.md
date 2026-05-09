# ADR-002 — Local Tracker

## Status

Frozen.

## Decision

Use SQLite as the local tracker source of truth. Introduce `tracker.kind: local`. Do not simulate Linear API. Markdown/JSON export is allowed, but not source of truth.

## Tracker state machine

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

Active states:

```text
Ready
Working
Rework
```

Terminal states:

```text
Done
Cancelled
Duplicate
```

## Agent writes

Agent writes issue updates through a restricted local tool gateway. It can:

```text
read current issue
comment on current issue
mark current issue Blocked with reason
attach run artifacts
create follow-up issue
submit handoff
```

It cannot:

```text
delete issue
mark Done
modify unrelated issue
change project settings
push / PR / merge
```

## Rationale

SQLite gives local durability, transactions, queryability, and simple backup/export paths. Keeping tracker writes behind `symphony tool` preserves the Symphony separation between scheduler/runner and issue-writing tools.
