# ADR-008 — v1 Scope and Non-Goals

## Status

Frozen.

## v1 must implement

```text
local tracker
Go daemon
React dashboard
SQLite schema v1
OpenAPI v1
CLI and tool gateway
workflow parser and prompt renderer
git worktree manager
Codex adapter
approval bridge
review packet generator
fake-agent E2E tests
```

## v1 must not implement

```text
Tauri desktop shell
automatic PR / merge
agent automatic commit
automatic SQLite backup
migration / rollback production flow
crash recovery
full audit log
supply-chain deep risk policy
dynamic tools / MCP
multi-tenant RBAC
remote dashboard
workspace destructive APIs
secret manager
```

## Deferred roadmap

```text
v1.1: migration, backup, crash recovery, audit log, optional agent commit
v1.2: supply-chain policy, Git provider publish, optional PR flow
v2: Tauri desktop shell, multi-project UX, possible RBAC/remote modes after new threat model
```
