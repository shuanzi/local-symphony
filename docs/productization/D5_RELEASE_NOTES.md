# D5 — Release packaging & install experience (R13)

> Internal deliverable doc that backs the v1.1 WIP release packaging work
> captured by `docs/RELEASE_NOTES.md`. This file is the **design record**;
> `docs/RELEASE_NOTES.md` is the **public contract** the release ships
> with.

## 1. Release build layout (decision)

We chose **install layout next to the executable** over `//go:embed`-ing
the dashboard into the binary.

Layout produced by `scripts/build-release.sh`:

```text
dist/
  symphony[.exe]    # Go daemon + CLI binary
  web/dist/...      # React/Vite dashboard static assets (already-built)
  INSTALL.md        # Short install hint
```

### Why install layout, not embed

1. **No rebuild for dashboard changes.** When the dashboard changes we can
   repackage `dist/web/dist/` without recompiling Go, which keeps the
   release loop short.
2. **Cross-compile does not pay the embed tax.** `//go:embed` requires the
   embed source to be readable at Go build time. Cross-compiling a Go
   binary on a host without `npm` would fail to embed. The install layout
   keeps the build script tolerant of a missing `npm`.
3. **Already in the codebase.** `internal/httpapi.dashboardDist()` already
   walks `os.Executable()`'s parent directory and looks for
   `<exe>/web/dist`. The install layout is a **discoverable** superset
   of what the code already accepts — no new code path required for the
   primary case.
4. **`SYMPHONY_DASHBOARD_DIST` still wins.** The discovery code keeps
   honoring the explicit env override (tested by
   `TestDashboardExplicitDistMayPointInsideProjectRoot`).

The tradeoff: a power user who *only* ships the binary still needs
`web/dist/` next to it. We capture this in `INSTALL.md` and in the
fallback HTML page the server returns when the dist is missing.

## 2. Dashboard asset embedding vs install layout

Picked install layout. The `//go:embed` route was considered and rejected
for the reasons above. If a future use case requires a single-file
binary (e.g. distribution through a one-shot installer), the embed path
is a small, well-isolated follow-up: add a `webFS` field to
`internal/httpapi.dashboardDist()` and prefer it over the file-system
candidates.

## 3. Release-blocking checklist

Captured in full in `docs/RELEASE_NOTES.md` §4. The CI gate is:

1. `bash scripts/build-release.sh` (Go + npm)
2. `go test ./...`
3. `cd web && npm run build && cd ..`
4. `python3 -m pip install -r requirements-dev.txt && python3 scripts/validate_contracts.py`
5. `bash scripts/acceptance-local.sh`

Steps 1 and 3 are partially overlapping (the build script runs the web
build) — the redundant step exists so a failure in step 1 cannot mask a
regression in step 3 and vice versa.

## 4. Supported platforms / dependency versions

Captured in `docs/RELEASE_NOTES.md` §2 and §3. Summary:

- **Build-tested / acceptance-gated**: macOS 13+ arm64, macOS 13+ amd64,
  Linux glibc amd64.
- **Best-effort**: Windows (binary builds; `acceptance-local.sh` is
  POSIX-only), other Linux arches / libc flavors.
- **Required at build**: Go 1.23+, a C toolchain (for CGO/SQLite), Node
  18+ (only for the dashboard build), Python 3.10+ (only for
  `validate_contracts.py`).
- **Required at runtime**: libsqlite3 ≥ 3.32, Git 2.30+. No Node and no
  Python are needed at runtime — only the prebuilt `web/dist/`.

## 5. Known limits

- Codex is fixture-gated; the default runner is `fake`. Real Codex CLI
  integration is explicitly **out of scope** for v1.1 WIP and is listed
  in `docs/RELEASE_NOTES.md` §6.
- API is loopback-only (`--host` accepts only `127.0.0.1` or `localhost`).
- Single owner per project. A second `symphony serve` for the same
  `--project` is rejected with `daemon_already_running`.
- Windows is best-effort (see §2 of `docs/RELEASE_NOTES.md`).

## 6. Routing priority

`internal/httpapi.Handler()` registers routes in this exact order:

```text
/tool/v1/call  →  handleTool        (1)
/api/v1/...    →  handleAPI         (2)
/...           →  serveDashboard    (3, catch-all)
```

This means `http.ServeMux` resolves `/api/v1/health`,
`/api/v1/auth/session`, `/api/v1/diagnostics`, and `/tool/v1/call` to
the API/Tool Gateway handlers before the static-asset handler can
match. The behavior is locked in by three new tests:

- `TestRoutingPriorityAPIBeforeDashboard` — seeds a `web/dist` that
  intentionally shadows API responses, then asserts the API responses
  still win and the dashboard fallback still serves a real asset at `/`.
- `TestDashboardDistDiscoveryFailureSurfacesError` — points the
  explicit dist override at a non-existent path, then asserts the
  fallback HTML page is returned (status 200, no file paths leaked)
  and the API still works.
- `TestDashboardDistDiscoveryCorruptIndexSurfacesError` — points at a
  dist directory that exists but has no `index.html`, asserts the same
  safe fallback page is returned.

## 7. Commits (this branch)

`codex/v1-productization-d5-release`, branched from `f15ba39`:

1. **chore(release): add scripts/build-release.sh** — produces
   `dist/symphony` + `dist/web/dist/` install layout. Honors
   `GOOS/GOARCH/CGO_ENABLED/SKIP_WEB` env vars; tolerates a missing
   `npm`; drops `dist/INSTALL.md` next to the binary.
2. **docs(release): add docs/RELEASE_NOTES.md** — public release
   notes with version matrix, release-blocking checklist, and Windows
   best-effort caveats.
3. **test(httpapi): routing priority and dist discovery failure** — adds
   the three new tests described in §6.
4. **docs(readme): quickstart uses dist/symphony** — README §4/§5/§6
   now reference the release artifact; cleanup section updates
   `~/.symphony/cli-session.json` → `~/.symphony/cli-sessions`.
5. **fix(cli): help text matches real flags and commands** — replaces
   the placeholder `--addr 127.0.0.1:3777` with the real default
   (`--host 127.0.0.1 [--port]` / `--addr 127.0.0.1:7331`), removes the
   `symphony tool login` reference (no such command), and aligns
   `login`/`login --list`/`login --logout` with the actual semantics
   (server-side revoke).
6. **chore(gitignore): dist/, web/package-lock.json** — keep the release
   layout out of source control; the release artifact is reproducible
   via the build script.

## 8. Acceptance results

- `go test ./internal/httpapi -count=1 -timeout 60s` → PASS (incl. 3
  new tests).
- `go test ./internal/app ./internal/cli -count=1 -timeout 60s` → PASS.
- `bash scripts/acceptance-local.sh` → `acceptance-local passed`.
- `python3 scripts/validate_contracts.py` → `contract validation passed`.
- `bash scripts/build-release.sh` → produced
  `dist/symphony` (12 MB), `dist/web/dist/`, `dist/INSTALL.md`.

The full `go test ./...` is run by the CI gate (it exceeds the local
660s default timeout on this workstation but completes in CI; that is
not a D5 regression and is documented in the handoff).
