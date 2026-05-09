# ADR-004 — Workspace and Git Strategy

## Status

Frozen.

## Decision

v1 project equals one local Git repo. Each issue gets one stable git worktree and one stable branch.

Default workspace path:

```text
~/.symphony/workspaces/<project_id>/<issue_identifier>/
```

Branch format:

```text
symphony/<issue_identifier>-<title_slug>-<short_hash>
```

Base ref default:

```yaml
git:
  base_ref: auto
```

`auto` resolution order:

```text
origin/main
origin/master
main
master
HEAD
```

## Rules

```text
Same issue reuses same workspace and branch.
No automatic reset, clean, rebase, push, PR, merge, or force push.
Agent does not commit by default.
Human review controls Done/Rework.
```

## Rationale

Git worktree provides isolation without cloning the full repository for every issue. Keeping workspaces outside the main repo avoids nested working tree confusion and prevents accidental commits of workspace directories.
