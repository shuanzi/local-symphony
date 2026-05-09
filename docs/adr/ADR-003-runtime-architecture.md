# ADR-003 — Runtime Architecture

## Status

Frozen.

## Decision

Use a Go daemon with embedded React/TypeScript dashboard assets. v1 exposes a localhost dashboard. v2 desktop packaging will use Tauri + Go sidecar.

Runtime structure:

```text
symphony binary
├── symphony serve        # daemon
├── symphony open         # dashboard auth/open flow
├── symphony issue/run/...# operator CLI
└── symphony tool ...     # agent tool CLI
```

Daemon components:

```text
HTTP API / SSE
Orchestrator actor
Local tracker service
Workspace manager
Git service
Codex app-server adapter
Tool gateway
Review packet generator
Diagnostics
```

## Key decisions

| Area | Decision |
|---|---|
| Backend | Go |
| Frontend | React + TypeScript |
| DB | SQLite |
| Realtime | SSE |
| API | `/api/v1` REST |
| CLI | single `symphony` binary |
| Desktop future | Tauri + Go sidecar |

## Rationale

Go is well-suited to local daemon behavior, process management, filesystems, IPC, and single-binary distribution. React/TypeScript provides a durable UI codebase that can later be wrapped by Tauri without changing core product boundaries.
