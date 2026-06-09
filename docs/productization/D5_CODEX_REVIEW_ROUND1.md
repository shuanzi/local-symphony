# D5 / R13 Codex Review — Round 1

- **Worktree**: `/Users/xiquandai/Documents/code/local-symphony-d5-release`
- **Commit under review**: `573b1f0` — D5: release packaging, install layout, routing-priority tests
- **Base**: `main` (`f15ba39` at review time)
- **Reviewer**: `codex review` (Codex v0.130.0, model `gpt-5.5`, reasoning effort `xhigh`, sandbox `danger-full-access`, agent profile `~/.codex/agents/reviewer.toml`)
- **Command**:
  ```bash
  cd /Users/xiquandai/Documents/code/local-symphony-d5-release
  codex review --commit 573b1f0 --title "D5 R13: release packaging, install layout, routing-priority tests"
  ```
  > The `codex review` CLI rejects combining `--base <BRANCH>` with `--commit <SHA>` (exit 2). When reviewing a single commit, pass `--commit` only. The D5 commit is the only delta relative to `main`, so the diff is identical.
- **Timestamp**: 2026-06-09T11:44:21+0800
- **Exit code**: 0 (clean run, reviewer surfaced findings)
- **Full stdout/stderr log**: `/tmp/codex_d5_round1.log` (4050 lines, 172 KB)

## Diff scope under review (per `git diff main..HEAD --stat`)

| File | Status | +/- |
|---|---|---|
| `.gitignore` | M | +2 / -0 |
| `README.md` | M | +59 / -17 |
| `docs/RELEASE_NOTES.md` | A | +179 / -0 |
| `docs/productization/D5_RELEASE_NOTES.md` | A | +155 / -0 |
| `internal/cli/cli.go` | M | +18 / -0 |
| `internal/httpapi/httpapi_test.go` | M | +168 / -0 |
| `scripts/build-release.sh` | A | +86 / -0 |
| **Total** | | **+650 / -17** (7 files) |

## Summary

Codex returned **2 unique findings** (the same payload is echoed twice in stdout by the assistant's final summary; `grep -c '^\s*-\s*\[P[0-3]\]'` returns 4 because of the duplication).

| ID | Severity | File | Status |
|---|---|---|---|
| F1 | **P1** | `scripts/build-release.sh:49` | **Fixed** in round-1 fix commit (see §F1 outcome below) |
| F2 | **P2** | `scripts/build-release.sh:45-46` | **Fixed** in round-1 fix commit (see §F2 outcome below) |

No P3 / NIT findings. No findings against `internal/httpapi/httpapi_test.go`, `internal/cli/cli.go`, `docs/RELEASE_NOTES.md`, `docs/productization/D5_RELEASE_NOTES.md`, `README.md`, or `.gitignore`.

## F1 [P1] Guard `OUT_DIR` before deleting web assets

- **File**: `scripts/build-release.sh:49`
- **Cited line** (verified locally):
  ```sh
  49:    rm -rf "$OUT_DIR/web"
  50:    mkdir -p "$OUT_DIR/web"
  51:    cp -R "$ROOT/web/dist/." "$OUT_DIR/web/dist/"
  ```
- **Reviewer claim**: If a release caller sets `OUT_DIR=$PWD` (or any path that resolves to/under `$ROOT`), the unconditional `rm -rf "$OUT_DIR/web"` deletes the **checked-in `web/` source tree** that was just used by `npm run build`, after which `cp -R "$ROOT/web/dist/." ...` fails because `$ROOT/web/dist` is gone.
- **Impact**: Release packaging is data-destructively unsafe when the caller chooses an output path overlapping the source tree. This is a footgun the script hands to anyone running it.
- **Reviewer fix suggestions**: Either (a) reject `OUT_DIR` values that resolve to or under `$ROOT`, or (b) avoid the blanket `rm -rf` and instead delete only a dedicated `$OUT_DIR/web/dist` target (since that is the only thing the script needs to overwrite).
- **Local verification**: Confirmed — line 49 reads `rm -rf "$OUT_DIR/web"` with no guard, no `--one-file-system`, and no `realpath` equality check against `$ROOT`. F1 is **valid**.

## F2 [P2] Use a checked-in lockfile for web release builds

- **File**: `scripts/build-release.sh:45-46`
- **Cited lines** (verified locally):
  ```sh
  45:    if [ ! -d "$ROOT/web/node_modules" ]; then
  46:      (cd "$ROOT/web" && npm install --no-audit --no-fund)
  47:    fi
  ```
- **Reviewer claim**:
  1. The repo has both `web/pnpm-lock.yaml` and `web/package-lock.json` checked in, but the script uses `npm install`, which writes/respects `package-lock.json` (and ignores `pnpm-lock.yaml`).
  2. `web/package.json` uses `latest`-pinned versions, so a clean `npm install` resolves whatever upstream `latest` points to at build time. Two consecutive release builds with no source change can ship different React/Vite/TypeScript versions, breaking the release gate and producing non-reproducible artifacts.
- **Impact**: Release artifacts are **non-reproducible**; an upstream `latest` bump can flip the bundled dashboard's behavior between tagged builds.
- **Reviewer fix suggestions**: Use the committed lockfile with a frozen install — e.g. `npm ci` against `package-lock.json`, or switch to `pnpm install --frozen-lockfile` and stop calling `npm install`.
- **Local verification**:
  ```
  $ ls web/ | grep -E 'lock|json|yaml'
  action-inventory.json
  package-lock.json
  package.json
  pnpm-lock.yaml
  ```
  Both lockfiles exist; script uses neither for a frozen install. F2 is **valid**.

## F1 outcome — fixed (round-1 fix commit `1e051b6`)

- **Fix landed in**: `scripts/build-release.sh` (round-1 fix commit `1e051b6`).
- **What changed**: Two protection layers were added so a regression is observable
  even if a future change re-introduces a destructive `rm -rf`:
  1. **Early-exit guard** that rejects `OUT_DIR` values resolving to `$ROOT`
     or any descendant of `$ROOT` (uses `cd ... && pwd -P` on both
     sides so symlinks and `..` traversal cannot bypass it). The
     script exits with code 2 and prints a clear refusal message.
  2. **Scoped destructive line**: the blanket `rm -rf "$OUT_DIR/web"`
     was replaced with `rm -rf "$OUT_DIR/web/dist"` — the only
     subtree the script actually needs to overwrite. A sibling
     `$OUT_DIR/web/<unrelated>` directory is now safe from
     collateral damage.
- **Test coverage** (`internal/buildrelease/safety_test.go`, all
  written test-first and verified FAIL on the unfixed script before
  the fix was applied):
  - `TestBuildReleaseRejectsOUTDirEqualToRoot` — `OUT_DIR=$ROOT`
    must exit non-zero with an error naming both `OUT_DIR` and the
    overlap wording.
  - `TestBuildReleaseRejectsOUTDirUnderRoot` — `OUT_DIR=<subdir
    under $ROOT>` must exit non-zero.
  - `TestBuildReleaseDoesNotBlanketDeleteOUTDirWeb` — a sentinel
    file in `$OUT_DIR/web/user-data.txt` must survive the script;
    exercises the destructive line by stubbing `npm` in `PATH` and
    pre-seeding `web/node_modules` and `web/dist/`.
- **Re-verification**: `OUT_DIR=$PWD bash scripts/build-release.sh`
  exits 2 with the refusal message; `web/` source tree is intact
  afterwards.

## F2 outcome — fixed (round-1 fix commit `1e051b6`)

- **Fix landed in**: `scripts/build-release.sh` (round-1 fix commit
  `1e051b6`) and the repo's `web/pnpm-lock.yaml` is now
  removed.
- **What changed**:
  1. The web install switched from `npm install` to `npm ci` so
     web/package-lock.json pins the exact React/Vite/TypeScript
     versions that ship in the release artifact.
  2. The conflicting `web/pnpm-lock.yaml` is removed from the repo
     so the next person who runs `pnpm install` cannot silently
     re-resolve `latest` and overwrite the lockfile.
- **Test coverage** (`internal/buildrelease/safety_test.go`, all
  test-first FAIL → PASS):
  - `TestBuildReleaseUsesNpmCiNotNpmInstall` — static-text assertion
    that the script (comments stripped) calls `npm ci` and does not
    call `npm install`.
  - `TestBuildReleaseLockfileStoryIsConsistent` — `web/package-lock.json`
    must exist and contain a `lockfileVersion` field; `web/pnpm-lock.yaml`
    must NOT exist.
- **Re-verification**: `SKIP_WEB=0 OUT_DIR=/tmp/d5-build-smoke bash
  scripts/build-release.sh` produces a valid release artifact using
  `npm ci` end-to-end (verified locally on darwin/amd64).

## What is NOT flagged (good signal)

- **Routing-priority / dist-discovery tests** in `internal/httpapi/httpapi_test.go` (the three new tests) — no correctness findings.
- **`internal/cli/cli.go`** `printHelp` alignment (--host, --port, --addr, --no-open, login semantics) — no findings.
- **`docs/RELEASE_NOTES.md`** and **`docs/productization/D5_RELEASE_NOTES.md`** — no findings (release contract, version matrix, non-capability list, Windows caveats all read clean).
- **`README.md`** quickstart + cleanup section — no findings.
- **`.gitignore`** additions (`dist/`, `web/package-lock.json`) — no findings.

## Round-1 fix commit

`1e051b6` — "D5 R1: fix F1 (OUT_DIR guard) and F2 (npm ci) per codex review"

Stat: 4 files changed, +582 / -574 (the −574 is dominated by removing
`web/pnpm-lock.yaml`, which was 571 lines).
2. After the fixes land as a new commit on `codex/v1-productization-d5-release`, re-run codex review:
   ```bash
   codex review --commit <NEW_SHA> --title "D5 R13: release packaging (round 2)"
   ```
3. If round 2 returns 0 findings, mark `D3.0` / `D5.0` review tasks complete and the worktree is ready for PR.
