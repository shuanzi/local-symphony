# D5 R-fix notes — PR23-P2-1 (mkdir before guard)

## Finding

- **PR**: https://github.com/shuanzi/local-symphony/pull/23
- **Finding ID**: PR23-P2-1 (D5 codex review, post-merge review)
- **Severity**: P2
- **File**: `scripts/build-release.sh:47` (pre-fix line)
- **Description**: The pre-guard resolution line
  `OUT_DIR_RESOLVED=$(mkdir -p "$OUT_DIR" && cd "$OUT_DIR" && pwd -P)`
  ran `mkdir -p` BEFORE the overlap check. When a caller passed
  `OUT_DIR=$ROOT/web/<forbidden-subdir>`, the script correctly
  rejected the path with the expected `refusing to run` error,
  but the forbidden directory was already created on disk as a
  side effect. The guard is documented as fail-closed, and a
  guard that mutates the filesystem before rejecting is not
  actually fail-closed.

## Fix

- **Commit**: `1c8a577` (D5 R-fix: defer mkdir -p until after
  overlap guard accepts target)
- **Branch**: `codex/v1-productization-d5-release`
- **Strategy**: split path resolution from path creation.
  - If `OUT_DIR` already exists, resolve via
    `cd "$OUT_DIR" && pwd -P` as before.
  - If `OUT_DIR` does not exist, resolve its **parent**
    (`dirname`) via `cd ... && pwd -P` and append the
    **leaf** (`basename`) — this computes the absolute target
    path without touching the filesystem.
  - Run the overlap guard on the computed `OUT_DIR_RESOLVED`.
  - Only after the guard accepts the path does
    `mkdir -p "$OUT_DIR"` create the directory.
- **Edge cases handled**:
  - `OUT_DIR_PARENT` itself does not exist → fail fast with a
    clear error (no filesystem mutation, no overlap-guard
    ambiguity from a non-canonical path).
  - Default `OUT_DIR=$ROOT/dist` is accepted (it doesn't exist
    on a fresh checkout, so the new "parent + leaf" branch is
    exercised, and the overlap guard's `$ROOT_RESOLVED/dist`
    whitelist entry still allows it). Verified by
    `TestBuildReleaseAcceptsDefaultOUTDir`.

## Test

- **New test**: `TestBuildReleaseMkdirDoesNotCreateRejectedOutDir`
  in `internal/buildrelease/safety_test.go`.
- **What it asserts**:
  1. Setting `OUT_DIR=$ROOT/web/forbidden-subdir` causes the
     script to exit non-zero with `refusing to run` (the
     existing guard).
  2. After the rejected run, `$ROOT/web/forbidden-subdir` MUST
     NOT exist on disk.
  3. After the rejected run, `$ROOT/web/` MUST NOT contain a
     `forbidden-subdir` entry.
- **Test-first FAIL→PASS evidence**:
  - Pre-fix run (commit before `1c8a577`):
    ```
    === RUN   TestBuildReleaseMkdirDoesNotCreateRejectedOutDir
        safety_test.go:235: fail-closed guard is not actually
        fail-closed: /var/folders/.../web/forbidden-subdir was
        created on disk by the pre-guard `mkdir -p` BEFORE the
        overlap check rejected the path.
    --- FAIL: TestBuildReleaseMkdirDoesNotCreateRejectedOutDir (0.16s)
    ```
  - Post-fix run (commit `1c8a577`):
    ```
    === RUN   TestBuildReleaseMkdirDoesNotCreateRejectedOutDir
    --- PASS: TestBuildReleaseMkdirDoesNotCreateRejectedOutDir (0.17s)
    ```

## Validation

All ran from the worktree at `/Users/xiquandai/Documents/code/local-symphony-d5-release`
on branch `codex/v1-productization-d5-release`, post-fix.

| Command | Result |
| --- | --- |
| `go test -count=1 -timeout 60s ./internal/buildrelease` | ok (9/9 tests pass, including the new one) |
| `go test -count=1 -timeout 300s ./...` | ok (all packages green) |
| `python3 scripts/validate_contracts.py` | contract validation passed |
| `bash scripts/acceptance-local.sh` | acceptance-local passed |

## End-to-end repro (manual)

A standalone repro script (in `/tmp/bug-repro/bug-repro.sh`) was run
against both the pre-fix and post-fix scripts. Pre-fix: `$ROOT/web/`
contained a `forbidden-subdir/` entry after the rejected run. Post-fix:
`$ROOT/web/` is byte-identical before and after the rejected run.

## Files touched (scope: strictly limited)

- `scripts/build-release.sh` — guard resolution rewritten to
  compute the target path without `mkdir -p`; `mkdir -p` deferred
  to after the guard accepts.
- `internal/buildrelease/safety_test.go` — new test
  `TestBuildReleaseMkdirDoesNotCreateRejectedOutDir` + new helper
  `dirListing` + package doc block updated to record the new
  finding.
- This notes doc.

No other files modified. No new dependencies. No schema / openapi
/ contract validation changes.
