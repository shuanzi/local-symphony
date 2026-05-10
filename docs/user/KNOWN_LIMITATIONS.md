# Known Limitations — Local Symphony App v1

Local Symphony App v1 intentionally optimizes for a reviewable local workflow rather than maximum automation.

## Not included in v1

```text
No Linear tracker dependency or adapter
No Tauri desktop shell
No automatic PR creation
No git push or merge automation
No agent automatic commit
No automatic SQLite backup
No production migration or rollback flow
No automatic retry queue or retry timers
No crash recovery beyond stale active-run interruption
No full audit log
No supply-chain deep risk scoring
No dynamic tools or MCP
No multi-tenant RBAC
No remote dashboard
No automatic workspace cleanup, delete, reset, or rebase
No raw prompt or raw Codex log export through v1 API
```

## Security limitations

- v1 is local-first and assumes the operator controls the local machine account.
- Network deny is not an OS-level firewall. It depends on Codex approval/sandbox surfacing network requests.
- Command and protected-path enforcement depend partly on Codex surfacing command/file approval requests before execution.
- Diagnostics are redacted best-effort and are not a compliance-grade audit trail.

## Operational limitations

- Only one active daemon should own a project DB at a time.
- Unsupported DB versions fail safely; v1 does not migrate.
- Unsupported Codex protocol versions fail before dispatch.
- Workspaces are intentionally retained and may require manual cleanup by the operator.
- Failed or cancelled issues require explicit operator resume before redispatch.

## Review limitations

- Review packets are cumulative from workspace `base_sha`; they are not incremental between rework attempts.
- Mark Done does not commit, push, merge, publish, or delete workspace.
- Older review packets remain visible but only the latest generated packet satisfies Mark Done.
