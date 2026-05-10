# Definition of Done — Local Symphony App v1

A v1 implementation is done only when all categories below are complete.

## Product behavior

- Local tracker works without Linear credentials, configuration, API calls, or code paths.
- Main path works: init → create issue → Ready → dispatch → workspace → fake agent handoff → after_run → review packet → Human Review → Done.
- Handoff target is fixed to `Human Review`; workflow validation rejects other handoff targets.
- Handoff is two-stage: `handoff.submit` records data; finalizer generates review packet; only then issue enters Human Review.
- Run failures, missing handoff twice, operator cancellation, approval `cancel_run`, and agent `issue.block` pause dispatch and do not automatically redispatch.
- Workspaces are retained in Done, Cancelled, Duplicate, Blocked, failed, and interrupted cases.
- Rework uses the same workspace and cumulative diff semantics defined in IS-016.

## Contracts

- `api/openapi.yaml` validates as OpenAPI 3.1.
- API handlers and CLI clients conform to `api/openapi.yaml`.
- Frontend types are generated from `api/openapi.yaml`.
- SQLite init uses `db/schema/app_v1.sql` and `db/schema/project_v1.sql`.
- Schema Markdown files match the SQL files where they document DDL.
- Codex adapter accepts only committed supported protocol fixtures and fails unsupported versions before dispatch.

## Tests

- Unit tests cover state transitions, workflow validation, prompt rendering, path normalization, security policy, redaction, and error-code mapping.
- Integration tests cover SQLite init, issue create transaction, blocker eligibility, workspace lifecycle, hooks, tool gateway, artifact containment, review packet generation, and SSE replay.
- Fake-agent E2E tests cover all required scenarios in `docs/agent/ACCEPTANCE.md`.
- API contract tests validate response schemas, error envelopes, issue refs, run cancel side effects, approval cancel side effects, and workflow reload.
- Security regression tests cover loopback/session/CSRF, tool tokens, workspace boundary, protected paths, command policy, network policy, redaction, and artifact containment.
- Real Codex tests are opt-in and not required for default CI.

## Security and data handling

- Browser session uses HttpOnly SameSite cookie plus CSRF header for command APIs.
- CLI session uses bearer token stored outside the DB; only token hash is stored.
- Run-scoped tool tokens are single-run, hashed at rest, expiring, revocable, and scope checked on every call.
- Diagnostics exports are redacted only.
- Raw prompt and raw Codex logs are not exposed through v1 API.
- Protected path writes are denied. Reads and artifact attachments follow the security model.

## Operations

- `symphony serve` writes a runtime descriptor with no secrets.
- A project prevents concurrent daemon ownership or handles it with a deterministic refusal path.
- Startup stale active runs become failed with `daemon_restarted_run_interrupted` and pause dispatch.
- Unsupported DB version fails safely with `unsupported_db_version`.
- Release build embeds the frontend and produces a single `dist/symphony` binary.

## Documentation

- Quickstart exists and matches the implemented CLI.
- Known limitations explicitly list all v1 non-goals.
- Security model documents enforcement source, bypass risk, and tests.
- Implementation docs, OpenAPI, SQL schema, acceptance docs, and backlog remain aligned.
