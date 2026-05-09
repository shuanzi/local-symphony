# ADR-007 — Security Baseline

## Status

Frozen.

## Decision

Default security mode is `balanced-secure`.

Baseline:

```text
API loopback-only
browser session token + CSRF
CLI bearer token
run-scoped tool token
agent minimal env allowlist
workspace is only default writable boundary
network default deny
protected path policy
git push / PR / force push denied
redacted logs, prompt snapshots, review packets
```

## v1 does not include

```text
full audit log
automatic backup
migration / rollback
crash recovery
supply-chain deep analysis
secret manager
remote dashboard
```

## Rationale

Local does not equal trusted. Repo content, issue text, hooks, command output, and dependency scripts may be adversarial or unsafe. v1 implements local safety boundaries without claiming enterprise-grade compliance.
