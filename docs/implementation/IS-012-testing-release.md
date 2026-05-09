# IS-012 — Testing, Release, and Documentation

## Status

Frozen.

## Test layers

```text
unit
integration
fake-agent E2E
real-Codex opt-in
frontend component
API contract
security regression
```

## Unit tests

Required coverage:

```text
core state transitions
workflow parser
effective config defaults
Liquid strict rendering
path normalization
branch naming
workspace key sanitization
command policy
protected path matching
redaction
error code mapping for split ApiErrorCode vs FailureCode
agent turn-count/handoff hard constraints
```

## Integration tests

Required coverage:

```text
SQLite schema init
issue create transaction
blocker eligibility query
worktree create/reuse
hook lifecycle: after_create then before_run on first run, before_run on reuse
tool gateway token validation
artifact attach containment
review packet generation, including untracked new-file content in changes.patch
SSE replay from run_events.seq
```

## Fake-agent E2E tests

Required scenarios:

```text
init → create issue → Ready → dispatch → fake handoff → review → Done
missing handoff → continuation → handoff
missing handoff twice → dispatch_paused
tool token invalid
approval pending → approve → continue
command denied → failed with command_denied
network denied → failed with network_denied
protected path denied → failed with protected_path_denied
operator run cancel → cancelled + dispatch_paused + no automatic redispatch
approval cancel_run → cancelled + dispatch_paused + no automatic redispatch
workflow invalid → dispatch blocked
workspace conflict → failed with workspace_conflict
review packet failure → no Human Review
untracked file created by agent → review packet changes.patch includes file content
active run issue transition → reconciliation cancel
agent issue.block → Blocked + cancelled with agent_blocked
stale running run on startup → interrupted + dispatch_paused
```

## Real Codex opt-in tests

Not default CI. Run only when explicitly enabled:

```bash
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex/...
```

Required before release candidate, but not every local test run.

## API contract tests

```text
OpenAPI validates all response schemas
Go handlers conform to OpenAPI
frontend generated types compile
api/openapi.yaml exists before generated frontend types are committed
error envelope consistent
IssueRef path parameters accept both `iss_...` and `LOC-...`
state-transition side_effects for active-run reconciliation
run cancel and approval cancel_run side effects are schema-covered
```

## Security regression tests

Cover v1 adopted controls:

```text
loopback/session/CSRF
tool token invalid/expired/wrong scope
workspace boundary
protected path
command allow/review/deny
`symphony tool handoff` and other fixed tool commands allowed by command policy but rejected without valid tool token
network deny
redaction
artifact containment
```

## Release build

Output:

```text
dist/symphony
```

Build steps:

```text
frontend typecheck
build React static assets
embed static assets
go test ./...
fake-agent E2E
build single symphony binary
```

## Required docs

```text
README
Quickstart
Known limitations
Security model
WORKFLOW.md reference
CLI reference
OpenAPI reference
M0–M8 backlog
```

Known limitations must explicitly list:

```text
No automatic backup
No migration/rollback
No crash recovery
No full audit log
No supply-chain deep policy
No desktop shell
No automatic PR/merge
No automatic retry queue/timers
No automatic workspace cleanup/delete/reset
```

## Documentation authority

After implementation begins:

```text
docs/implementation/*.md are implementation source of truth
docs/adr/*.md record irreversible decisions
api/openapi.yaml is API source of truth when implemented
db/schema/*.sql is DB source of truth when implemented
WORKFLOW.md reference is config source of truth
```

## Frozen decisions

| ID | Decision |
|---|---|
| IS12-001 | fake-agent E2E mandatory |
| IS12-002 | real Codex tests opt-in |
| IS12-003 | OpenAPI contract tests required |
| IS12-004 | security regression tests cover v1 adopted controls |
| IS12-005 | release build embeds frontend into single binary |
| IS12-006 | known limitations must be documented |
| IS12-007 | Markdown docs become source of truth |
| IS12-008 | no implementation without docs/ADR alignment |
| IS12-009 | tests must cover active run reconciliation, hook lifecycle, aliases, and fixed handoff target |
| IS12-010 | tests must cover cancellation pause/no-redispatch semantics and untracked review-packet content |
| IS12-011 | API contract tests require generated `api/openapi.yaml` before frontend type generation |
