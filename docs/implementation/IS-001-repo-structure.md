# IS-001 — Repo Structure and Go Module Layout

## Status

Frozen.

## Goal

Define the repository, Go packages, frontend layout, CLI/daemon binary shape, and dependency directions for Local Symphony App v1.

## Repository structure

```text
local-symphony/
├── go.mod
├── go.sum
├── cmd/
│   └── symphony/
│       └── main.go
├── internal/
│   ├── app/
│   ├── core/
│   ├── config/
│   ├── db/
│   ├── store/
│   ├── tracker/
│   ├── orchestrator/
│   ├── workspace/
│   ├── gitx/
│   ├── agent/
│   ├── toolgateway/
│   ├── review/
│   ├── httpapi/
│   ├── cli/
│   ├── security/
│   ├── observability/
│   └── platform/
├── api/
│   └── openapi.yaml
├── db/
│   └── schema/
│       ├── app_v1.sql
│       └── project_v1.sql
├── web/
├── docs/
├── examples/
├── testdata/
└── scripts/
```

## Binary shape

v1 builds one binary:

```text
symphony
```

Subcommands:

```text
symphony init
symphony serve
symphony open
symphony status
symphony issue ...
symphony run ...
symphony approval ...
symphony review ...
symphony workflow ...
symphony diagnostics ...
symphony tool ...
```

## Go module

Use a single Go module, e.g.:

```go
module github.com/your-org/local-symphony
```

No multi-module layout in v1.

## Package responsibilities

| Package | Responsibility |
|---|---|
| `cmd/symphony` | minimal entrypoint; calls `internal/cli`. |
| `internal/app` | composition root, bootstrap, daemon lifecycle. |
| `internal/core` | pure domain types, enums, errors; stdlib only. |
| `internal/config` | WORKFLOW parser, effective config, prompt rendering. |
| `internal/db` | SQLite open/init/schema version/transactions. |
| `internal/store` | SQLite repositories returning core types. |
| `internal/tracker/local` | local issue tracker business logic. |
| `internal/orchestrator` | single authoritative actor and dispatch loop. |
| `internal/workspace` | workspace path and lifecycle. |
| `internal/gitx` | all Git command execution. |
| `internal/agent/codex` | Codex app-server adapter. |
| `internal/toolgateway` | agent tool IPC server/client and registry. |
| `internal/review` | review packet generation. |
| `internal/httpapi` | REST/SSE translation layer. |
| `internal/cli` | operator CLI and tool CLI command parsing. |
| `internal/security` | tokens, CSRF, command policy, path policy, redaction. |
| `internal/observability` | logs, events, diagnostics, exports. |
| `internal/platform` | OS-specific paths, IPC, process groups. |

## Dependency direction

```text
cmd
 ↓
cli / app
 ↓
httpapi / orchestrator / services
 ↓
tracker / workspace / agent / review / toolgateway
 ↓
store / gitx / security / observability
 ↓
db / platform
 ↓
core
```

Forbidden:

```text
core → any internal package
store → httpapi
orchestrator → httpapi
agent/codex → tracker/local
web → SQLite/filesystem/Git/Codex
tool CLI → SQLite directly
```

## SQLite driver

Use:

```text
database/sql + modernc.org/sqlite
```

Rationale: cgo-free, easier cross-platform binary and future desktop packaging.

## Store approach

v1 uses handwritten store/repository methods. Do not introduce sqlc in v1. Re-evaluate after schema stabilizes in v1.1.

## Testing layout

```text
testdata/
├── workflows/
├── repos/
├── codex-events/
└── review-packets/
```

v1 must include a fake Codex runner:

```text
internal/agent/fake
```

Real Codex integration tests are opt-in.

## Frozen decisions

| ID | Decision |
|---|---|
| IS-001 | single monorepo |
| IS-002 | single `symphony` binary |
| IS-003 | single Go module |
| IS-004 | `internal/core` stdlib-only |
| IS-005 | `internal/config` owns workflow parsing/prompt rendering |
| IS-006 | SQLite driver: `database/sql + modernc.org/sqlite` |
| IS-007 | handwritten store in v1 |
| IS-008 | local tracker in `internal/tracker/local` |
| IS-009 | orchestrator as single actor package |
| IS-010 | all Git in `internal/gitx` |
| IS-011 | Codex-only implementation behind minimal Runner interface |
| IS-012 | tool CLI uses daemon IPC, not DB direct |
| IS-013 | HTTP handlers translate request/response only |
| IS-014 | CLI uses API/IPC except `init` |
| IS-015 | OS-specific logic in `internal/platform` |
| IS-016 | frontend organized by product surfaces |
| IS-017 | OpenAPI is API contract source of truth |
| IS-018 | v1 schema files only; no migration framework |
| IS-019 | fake Codex runner mandatory |
| IS-020 | import direction + constructors, no DI framework |
