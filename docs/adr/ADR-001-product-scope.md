# ADR-001 — Product Scope

## Status

Frozen.

## Decision

Local Symphony App v1 is a local-first agent engineering workflow control plane. It is not a SaaS product, multi-tenant platform, or general-purpose project management system.

The v1 product is:

```text
local issue tracker
+ orchestrator
+ workspace manager
+ Codex runner
+ tool gateway
+ handoff/review packet
+ dashboard
```

## Rationale

The core product value is not a task board by itself. The value is converting local engineering work items into reliable agent execution units with observable lifecycle, bounded permissions, human review, and durable artifacts.

## Consequences

v1 focuses on one single-user local project at a time. Multi-user collaboration, RBAC, cloud sync, remote dashboard, and enterprise compliance are explicitly deferred.
