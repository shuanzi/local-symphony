# IS-007 — Workspace and Git Implementation

## Status

Frozen.

## Goal

Define how workspaces, branches, hooks, Git preflight, diff generation, and safety checks work in v1.

## Workspace path

Default:

```text
~/.symphony/workspaces/<project_id>/<issue_identifier>/
```

Rules:

```text
issue_identifier is sanitized
workspace path must be under workspace.root
workspace.root cannot be repo root
workspace.root cannot be inside .git
existing path must belong to the same issue workspace
```

Sanitization:

```text
allow A-Z a-z 0-9 . _ -
replace other characters with -
collapse repeated -
max length 80
```

## Base ref resolver

Default:

```yaml
git:
  base_ref: auto
```

Resolution order:

```text
origin/main
origin/master
main
master
HEAD
```

If user configures explicit base ref, it must resolve successfully with Git. Otherwise workspace preparation fails.

## Branch name

Format:

```text
symphony/<issue_identifier>-<title_slug>-<short_hash>
```

Example:

```text
symphony/LOC-1-add-local-tracker-a1b2c3
```

Validation:

```text
max length 96
no spaces
not ending with /
no ..
no @{
passes git check-ref-format
```

## Worktree create

```text
1. Resolve repo root.
2. Resolve base ref and base SHA.
3. Generate branch name.
4. Create worktree with new branch.
5. Insert/update workspaces row.
6. Run after_create hook.
```

## Worktree reuse

```text
1. Check workspace path exists.
2. Check path belongs to same issue.
3. Check branch matches DB.
4. Do not reset.
5. Do not clean.
6. Do not rebase.
7. Run before_run hook.
```

## Hooks

Hook execution environment:

```text
cwd = workspace_path
minimal env + safe project metadata
timeout = hooks.timeout_ms
stdout/stderr truncated to hooks.max_output_bytes
```

Failure semantics:

```text
after_create failed → workspace_prepare_failed
before_run failed → run failed
after_run failed → log only
before_remove unused in v1 because destructive cleanup is deferred
```

## Git command execution

All Git commands must go through:

```text
internal/gitx
```

Unified controls:

```text
timeout
cwd
stdout/stderr capture
redaction
path validation
structured errors
```

Forbidden outside `internal/gitx`:

```go
exec.Command("git", ...)
```

## Diff and patch generation

Review packet generation uses:

```text
git status --porcelain=v1
git diff --binary <base_sha> -- .
git diff --name-only <base_sha> -- .
git ls-files --others --exclude-standard
```

Outputs:

```text
changed-files.txt
changes.patch
untracked-files.json
```

## v1 non-goals

```text
git push
PR creation
auto rebase
auto merge
workspace delete
workspace reset
workspace cleanup
submodule recursive init
```

## Frozen decisions

| ID | Decision |
|---|---|
| IS7-001 | workspace path defaults to global workspace root by project/issue |
| IS7-002 | `git.base_ref` default is `auto` |
| IS7-003 | explicit base_ref missing → fail |
| IS7-004 | worktree reuse never reset/clean/rebase automatically |
| IS7-005 | hooks run in workspace with timeout/output limits |
| IS7-006 | all Git commands centralized in `internal/gitx` |
| IS7-007 | review packet diff based on workspace `base_sha` |
| IS7-008 | v1 does not implement publish, cleanup, reset, or rebase |
| G2 | starter base_ref changed to `auto` |
