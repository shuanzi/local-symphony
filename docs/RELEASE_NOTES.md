# Local Symphony — Release Notes (v1.1 WIP)

This document is the authoritative source for **what is in this build**, the
**platforms and dependency versions** we test against, and the **known limits**
that ship with the v1.1 WIP release. The README and CLI `--help` are kept in
sync with this file; if they ever disagree, treat this file as the source of
truth.

## 1. Release artifact

`scripts/build-release.sh` produces a single self-contained directory:

```text
dist/
  symphony[.exe]    # Daemon + CLI binary
  web/dist/...      # React/Vite dashboard static assets
  INSTALL.md        # Short install hint
```

The Go binary discovers the dashboard by walking from its own
`os.Executable()` path, so **no source checkout is required to run it**. The
fallback environment variable `SYMPHONY_DASHBOARD_DIST` is honored if you
prefer to point the binary at a different dist location.

Build with the host's default target:

```bash
bash scripts/build-release.sh
```

Cross-compile (note: CGO must remain enabled because the SQLite binding is
CGO-based):

```bash
GOOS=darwin GOARCH=arm64 bash scripts/build-release.sh
GOOS=linux  GOARCH=amd64 bash scripts/build-release.sh
```

## 2. Supported platforms

The v1.1 WIP release is **build-tested and acceptance-gated** on the
following targets:

| OS            | Arch    | Status   |
| ------------- | ------- | -------- |
| macOS 13+     | arm64   | Supported |
| macOS 13+     | amd64   | Supported |
| Linux glibc   | amd64   | Supported |

Other targets (Linux arm64, musl libc, etc.) are **best-effort**: the Go
toolchain will produce a binary, but the acceptance script is only
exercised on the rows above.

### Windows (best-effort)

Windows is **not** part of the acceptance gate. Caveats:

- The binary can be cross-built with `GOOS=windows`, but the SQLite binding
  requires a working C compiler (TDM-GCC / MinGW-w64) on the build host.
- POSIX-only paths inside `scripts/acceptance-local.sh` are not portable;
  the script will fail on Windows. The CLI itself uses only Go's stdlib
  path handling, so `symphony --help` and `symphony init` work in
  PowerShell or `cmd.exe`.
- Dashboard discovery uses `os.Executable()`, which resolves to the
  binary's directory on Windows. The release layout
  `dist\symphony.exe` + `dist\web\dist\` is honored.
- `symphony serve` binds to loopback only, so the Windows Defender
  firewall does not need to be touched.

Treat Windows as a "developer can boot it" target, not a released
platform. Real Windows support is explicitly out of scope for v1.1 WIP.

## 3. Supported dependency versions

| Dependency             | Version          | Notes |
| ---------------------- | ---------------- | ----- |
| Go (build & run)       | 1.23+            | Tested with the project go.mod `go 1.23` directive. |
| C toolchain            | C11 +            | Required because the SQLite layer is CGO-based. macOS: `xcode-select --install`. Debian/Ubuntu: `build-essential libsqlite3-dev`. |
| SQLite (runtime)       | libsqlite3 ≥ 3.32 | Bundled via CGO. macOS: pre-installed. |
| Git                    | 2.30+            | Used for worktree, branch, and diff generation. |
| Python (validate_contracts) | 3.10+        | Required only for `scripts/validate_contracts.py`. |
| Node.js (web build)    | 18+ (LTS)        | Required only to produce `web/dist/`. The prebuilt `web/dist` shipped under `dist/web/dist/` needs no Node at runtime. |
| npm                    | bundled          | The web build does not pin pnpm in CI; the lockfile uses pnpm but `npm install && npm run build` works equivalently. |
| Codex CLI              | not required     | Real Codex integration is fixture-gated and is off by default. See §6. |

## 4. Release-blocking checklist

Every tagged build must pass **all** of the following before it is
distributed:

```bash
# 1. Compile the Go binary cleanly.
bash scripts/build-release.sh
test -x dist/symphony || test -x dist/symphony.exe

# 2. Run the full Go test suite (covers routing, store, httpapi, app).
go test ./...

# 3. Build the dashboard, including TypeScript typecheck.
cd web && npm run build && cd ..

# 4. Static contract validation (OpenAPI, JSON Schema, SQLite schema,
#    action inventory, v1 forbidden-capability drift).
python3 -m pip install -r requirements-dev.txt
python3 scripts/validate_contracts.py

# 5. End-to-end acceptance in a temp git repo.
bash scripts/acceptance-local.sh
```

The CI gate must record an **all-green** status for every one of the
five steps above. A release artifact with any red step is not
distributed.

## 5. Routing priority

`internal/httpapi/httpapi.go:Handler()` registers API/Tool routes
**before** the dashboard catch-all. The order is:

```text
/tool/v1/call  →  handleTool       (always first)
/api/v1/...    →  handleAPI        (always second)
/...           →  serveDashboard   (catch-all fallback)
```

This guarantees that a request to `/api/v1/health`, `/api/v1/auth/session`,
`/api/v1/diagnostics`, or `/tool/v1/call` is **never** intercepted by the
static-asset handler, even if `web/dist/index.html` happens to contain
matching paths. Tests in `internal/httpapi/httpapi_test.go` lock this
behavior in (`TestDashboard*`, `TestStateRequiresSessionButSessionEndpointRemainsPublic`,
`TestToolRoute*`).

## 6. What is **not** in this release

The v1.1 WIP release does **not** claim the following capabilities. The
release notes, README, CLI help, and dashboard action inventory all reflect
this. They are tracked in `docs/productization/V1_REAL_PRODUCTIZATION_GAPS.md`
for the v1.2+ productization phases:

- Linear / GitHub Issues adapters
- Auto push, auto PR, auto merge, auto publish
- Agent-driven auto commit
- Auto workspace cleanup / reset / rebase
- Auto retry queue / retry timer
- Dynamic tools / MCP
- Remote dashboard / multi-tenant RBAC
- Secret management
- Raw prompt or raw Codex log export through API / dashboard / diagnostics
- Real Codex protocol execution (the adapter is fixture-gated; the
  default runner is `fake`)
- Auto-update
- Built-in `publish`, `create-pr`, `backup`, `restore`, `migrate`,
  `audit`, `workspace-delete`, `secret`, `project settings`,
  `issue delete`, or arbitrary state-mutation commands

If a future release adds any of the above, the relevant section of this
document and `README.md` must be updated in the same commit.

## 7. Known limits (v1.1 WIP)

- **Codex runner is fake by default.** Real Codex integration is a
  fixture-gated skeleton; without a fixture the dispatcher fails closed
  with `codex_not_supported`. See `docs/codex/` for the supported-fixture
  list.
- **API is loopback-only.** `--host` accepts only `127.0.0.1` or
  `localhost`. Binding to a public address is a v1 hard error.
- **No background auto-start.** The CLI does not start the daemon
  implicitly. The operator must run `symphony serve` (or use
  `symphony open` to mint a session for a daemon that is already up).
- **Single owner per project.** Two `symphony serve` instances for the
  same `--project` will conflict on the runtime owner nonce; the second
  is rejected with `daemon_already_running`.
- **Windows is best-effort.** See §2.

## 8. History

- v1.1 WIP — D5 release packaging: `dist/symphony` install layout, dashboard
  discovery, routing-priority guarantees, supported-platform matrix.
- v1.0 (M0–M8) — Local-first agent control plane with fake runner.
