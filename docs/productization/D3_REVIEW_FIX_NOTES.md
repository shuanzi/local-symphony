# D3 R14 — Review Round 5 Fix Notes

- **Reviewed commit:** `5e76253 internal/agent/codex: round-4 review fix (1 P2: path-qualified binary)`
- **Parent commit:** `5003070` (round-3 fix → round-4 review target)
- **Worktree:** `/Users/xiquandai/Documents/code/local-symphony-d3-codex-availability`
- **Reviewer:** `codex review` CLI (gpt-5.5, reasoning `xhigh`, sandbox `danger-full-access`)
- **Review source:** `docs/productization/D3_CODEX_REVIEW_ROUND5.md` and `D3_CODEX_REVIEW_ROUND5_RAW.txt`
- **Fix commit:** `084ac88 D3 R-fix: 3 review findings (F1 failure_reason null, F2 workflow config propagation, F3 sentinel-after-underscore redaction)`
- **PR:** https://github.com/shuanzi/local-symphony/pull/24
- **Fix timestamp:** 2026-06-09 (Asia/Shanghai)

## Verdict

**3 findings — 1 × P1 + 2 × P2.** All three are addressed in
`084ac88`. The round-4 path-qualified-binary fix and the
embedded-sentinel widening are confirmed correct; the
round-5 review returned a fresh wave of contract / redaction
issues that are all the diagnostics-layer surface of the
Codex availability block (failure_reason enum, workflow
config propagation, scalar-metadata sentinel redaction).

## Findings → Fix mapping

| # | Sev | File:Line | Title | Fix in `084ac88` |
|---|-----|-----------|-------|-------------------|
| 1 | **P2** | `internal/observability/codex_availability.go:39` | `failure_reason: ""` fails contract validation | Surface `failure_reason` (and `failure_code` / `failure_message` for symmetry) as JSON null on the success path. Schema and OpenAPI widened to `oneOf: [null, enum]`; `validate_contracts.py` grew an `unwrap_one_of_enum` helper. |
| 2 | **P2** | `internal/observability/codex_availability.go:106` | Preflight ignores `WORKFLOW.md` `codex.command` and `codex.experimental_api` | `CodexAvailability` now takes a `repoRoot` arg and loads the workflow via `config.Load`; a new `preflightOptionsFromWorkflow` helper threads `codex.command` + `codex.experimental_api` into `RunPreflight`. `Diagnostics` (which already has the workflow loaded) reuses the helper. `cli/operator.go statusData` and `httpapi.Server.state` pass `st.RepoRoot` / `s.Store.RepoRoot`. |
| 3 | **P1** | `internal/agent/codex/codex.go:1905` | Sentinel-after-underscore slips past the gate | Detector now matches the body `SYNTHETIC_[A-Z0-9_]+` greedily, then does a manual prefix check (character before `S` is start-of-string OR not in `[A-Z0-9]`) and the standard `\b` suffix check. Underscore and lowercase letter are now prefix boundaries; trailing-lowercase sentinels (`SYNTHETIC_PROMPT_BODY_do_not_leak`) keep their `sentinel: false` classification. |

## Test-first evidence

All three findings were fixed test-first. The new failing
tests:

- `TestDiagnosticsPreflightSuccessHasNullFailureReason`
  (`internal/observability/diagnostics_test.go`) — asserts
  `last_preflight.failure_reason` is JSON null on the
  success path AND that `last_preflight.failure_code` /
  `failure_message` are also null. The test pins
  `SYMPHONY_CODEX_FIXTURE_ROOT` to the codex package's
  hermetic testdata and `SYMPHONY_CODEX_VERSION_OUTPUT` so
  the preflight actually walks the success branch.
- `TestDiagnosticsPreflightReadsWorkflowCodexConfig`
  (`internal/observability/diagnostics_test.go`) — writes
  a `WORKFLOW.md` with `codex.experimental_api: true`,
  copies the codex package's `0.0.0-test` fixture
  (whose `experimental_api: false`), and asserts the
  preflight surfaces `experimental_api_not_supported`
  (the failure that the workflow's setting triggers). The
  test would FAIL on the previous code (the preflight
  would fall back to the default `ExperimentalAPI=false`
  and succeed against a fixture that also has
  `experimental_api: false`, masking the propagation
  bug).
- `TestCodexScrubCatchesSentinelAfterUnderscore`
  (`internal/agent/codex/preflight_test.go`) — pins the
  detector on `protocol_SYNTHETIC_PROMPT_BODY` and
  `schema_SYNTHETIC_API_SECRET` (both must now match),
  pins `scrubbedForDiagnostics` on the same strings, and
  asserts the failureDetails scrubber redacts the
  underscored-sentinel values end-to-end.

All three were observed to FAIL on `5e76253` and PASS on
`084ac88`. The team-lead repro
`TestVerifyEmbeddedSentinelTeamLeadRepro` was extended to
include the new positive cases (`vSYNTHETIC_OWNER_NONCE`,
`protocol_SYNTHETIC_PROMPT_BODY`,
`schema_SYNTHETIC_API_SECRET`) so the round-3 word-boundary
case and the round-5 widening are both pinned.

## Files touched

- `internal/observability/codex_availability.go` —
  `codexAvailability` projection converts empty strings
  to JSON null; `CodexAvailability` takes `repoRoot`,
  loads the workflow, threads `codex.command` /
  `codex.experimental_api` into preflight options.
- `internal/observability/diagnostics.go` — `Diagnostics`
  reuses `preflightOptionsFromWorkflow` (which is also
  used by the public `CodexAvailability`).
- `internal/cli/operator.go` — `statusData` passes
  `st.RepoRoot` to `observability.CodexAvailability`.
- `internal/httpapi/httpapi.go` — `Server.state` passes
  `s.Store.RepoRoot` to `observability.CodexAvailability`.
- `internal/agent/codex/codex.go` — sentinel detector now
  uses a body regex + manual prefix / suffix checks; the
  `\b` rule on the suffix side is preserved; an underscore
  or lowercase letter before the `S` is now a valid prefix
  boundary. Helpers `isSafePrefixChar` and `isWordChar`
  document the rule.
- `internal/agent/codex/preflight_test.go` — new F3 test;
  `TestVerifyEmbeddedSentinelTeamLeadRepro` extended with
  the round-5 widening cases.
- `internal/observability/diagnostics_test.go` — new F1
  and F2 tests; `codexFixtureRootForTest` and
  `copyFixtureDir` helpers to share the codex package's
  hermetic fixture root.
- `schemas/diagnostics.schema.json` — `failure_reason`
  widened to `oneOf: [null, enum]`.
- `api/openapi.yaml` — `failure_reason` widened to
  `oneOf: [null, enum]`.
- `scripts/validate_contracts.py` — `unwrap_one_of_enum`
  helper; both the schema and OpenAPI validation sites
  use it to extract the enum branch before checking
  membership.

## Validation

- `go test -count=1 -race -timeout 90s ./internal/agent/codex ./internal/observability ./internal/cli ./internal/httpapi` → all pass
- `go test -count=1 -short -timeout 60s ./...` (per package, to stay under cumulative timeout) → all packages pass
- `python3 scripts/validate_contracts.py` → `contract validation passed`
- `bash scripts/acceptance-local.sh` → `acceptance-local passed`

## Out of scope (carried forward to next round if any)

- The diagnostic envelope's `last_preflight` block does
  NOT include the `command` field. The round-5 fix
  propagates the workflow's command into the preflight
  options but the F2 test asserts via a behavioral
  signal (the `experimental_api_not_supported` failure
  reason) rather than a literal field check, because
  surfacing `command` in the envelope would require a
  schema / OpenAPI addition. That is a clean follow-up
  if the dashboard team wants the binary basename in
  the "Codex" card; it is intentionally NOT in this
  commit.
- The Python `validate_contracts.py`'s
  `SYNTHETIC_SENTINEL_RE` (`\bSYNTHETIC_[A-Z0-9_]+\b`)
  is unchanged. The runtime gate is now strictly more
  permissive than the contract validator on
  underscore / lowercase-prefix sentinels; the two
  surfaces have different threat models (the validator
  runs over test fixture golden files, the gate runs
  over operator-supplied runtime fixtures). Future
  work: align the validator with the gate so the
  "what is a sentinel" definition is in one place.

## Round 1 → Round 5 delta

| | R1 (4b37cb5) | R2 (6073a15) | R3 (5003070) | R4 (5e76253) | R5 (084ac88) |
|---|---|---|---|---|---|
| Total findings | 4 | 2 | 3 | 1 | **3** |
| P1 | 3 | 1 | 1 | 0 | **1** |
| P2 | 1 | 1 | 1 | 1 | **2** |
| P3 | 0 | 0 | 1 | 0 | 0 |
| New tests in fix | n/a | 5 (24 sub-cases) | 5 (21 sub-cases) | n/a | 3 |
| Cumulative tests | 4 | 24 | 21 | 0 | **3** |
