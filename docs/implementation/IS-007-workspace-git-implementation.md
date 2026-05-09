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
workspace.root cannot equal repo root
workspace.root cannot be inside repo root
workspace.root cannot be inside .git
existing path must belong to the same issue workspace
```

Sanitization:

```text
allow A-Z a-z 0-9 . _ -
replace other characters with -
collapse repeated -
trim leading/trailing - when possible
fallback to issue id when result is empty
max length 80
```

This differs from upstream SPEC underscore replacement and is an intentional Local v1 deviation. Tests must cover slash, whitespace, unicode, repeated separators, and long identifiers/titles.

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

## WorkspaceManager.Prepare lifecycle

Every run calls `WorkspaceManager.Prepare(issue)` before prompt rendering and Codex launch.

For a new workspace:

```text
1. Resolve repo root.
2. Resolve base ref config, resolved base ref, and base SHA.
3. Generate branch name.
4. Create worktree with new branch.
5. Insert/update workspaces row with `base_ref_config`, resolved `base_ref`, and `base_sha`.
6. Run after_create hook if configured.
7. Run before_run hook if configured.
```

For a reused workspace:

```text
1. Check workspace path exists.
2. Check path belongs to same issue.
3. Check branch matches DB.
4. Preserve stored `base_ref_config`, resolved `base_ref`, and `base_sha`; do not silently rebase to a newer base.
5. Do not reset.
6. Do not clean.
7. Do not rebase.
8. Run before_run hook if configured.
```

`before_run` therefore runs before every run attempt, including the first run immediately after `after_create` on a newly created worktree.

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
after_create failed → run failed with failure_code=after_create_failed
before_run failed → run failed with failure_code=before_run_failed
workspace ownership/path/branch conflict → failure_code=workspace_conflict
general workspace failure before hooks → failure_code=workspace_prepare_failed
after_run failed → log/event only
before_remove unused in v1 because destructive cleanup is deferred
```

`after_run` is a worker finally-step hook. If a workspace exists, it runs for completed, failed, cancelled, timeout, missing-handoff, and review-packet-failure outcomes. It is best-effort and cannot change the primary run outcome.

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

Untracked files must be included in the review packet. Because `git diff <base_sha>` does not include ordinary untracked files, the generator must add their content without relying on the agent to stage files. The v1 rule is:

```text
1. Collect untracked files with git ls-files --others --exclude-standard.
2. Validate each path is under the workspace and not protected by IS-009.
3. Add each untracked path to changed-files.txt.
4. Record path, size, sha256, and patch_included=true in untracked-files.json.
5. Append a binary-safe new-file patch for each untracked file to changes.patch.
6. Do not stage, commit, reset, clean, or mutate the index as part of review generation.
```

The untracked patch may be produced with Git-compatible no-index/new-file diff generation, but paths in the final patch must be normalized to workspace-relative `a/<path>` / `b/<path>` entries.

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
| IS7-001 | workspace path defaults to global workspace root by project/issue and must stay outside the repository root |
| IS7-002 | `git.base_ref` default is `auto`; workspace stores both configured value and resolved ref |
| IS7-003 | explicit base_ref missing → fail; resolved base_ref differs from base_ref_config when config uses auto |
| IS7-004 | worktree reuse never reset/clean/rebase automatically |
| IS7-005 | hooks run in workspace with timeout/output limits; `before_run` runs every attempt |
| IS7-006 | all Git commands centralized in `internal/gitx` |
| IS7-007 | review packet diff based on workspace `base_sha` and includes untracked new-file content |
| IS7-008 | v1 does not implement publish, cleanup, reset, or rebase |
| IS7-009 | workspace metadata stores both `base_ref_config` and resolved `base_ref` |
| G2 | starter base_ref changed to `auto` |
| G7 | active run reconciliation retains workspace without reset/clean/delete |
