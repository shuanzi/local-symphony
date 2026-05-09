# ADR-005 — Agent, Prompt, and Tools

## Status

Frozen.

## Decision

v1 supports Codex app-server only. Each run starts one Codex app-server subprocess. The Codex adapter is version-aware and keeps protocol framing/schema details isolated from the orchestrator. The agent receives a generated prompt composed of:

```text
Runtime Envelope
+ Tool Manifest
+ rendered WORKFLOW.md prompt
+ Context Pack
+ Handoff Contract
```

The local tool channel is `symphony tool` over daemon IPC. Codex dynamic tools and MCP are deferred.

## Tool registry v1

```text
issue.get
issue.comment
issue.block
artifact.attach
followup.create
handoff.submit
```

## Handoff

`handoff.submit` is two-stage:

```text
1. Agent submits handoff.
2. Run finalizer generates review packet.
3. Only if review packet succeeds, issue → Human Review.
```

## Rationale

This avoids the common failure state where the issue is in Human Review but review artifacts are missing. It also keeps tool permissions auditable at the run boundary without relying on experimental dynamic tool protocols.
