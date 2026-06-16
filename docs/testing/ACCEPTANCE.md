# Local Symphony App v1 Acceptance

**阶段 D 收口状态（2026-06-09）**：本 acceptance 文档按"是否依赖真实 Codex"分为三类。

| 类别 | 标记 | 触发方式 | 涉及段 |
|---|---|---|---|
| **fake acceptance** | `[fake]` | 默认 CI / `go test ./internal/...` + `python3 scripts/validate_contracts.py`；`bash scripts/acceptance-local.sh` 只覆盖本地构建 + daemon-backed A1/A2/A9 主路径冒烟 | A0 / A0a / A0b / A1 / A2 / A3 / A3a / A4 / A4a / A4a.1 / A4b / A5 / A6 / A7 / A9 / A9a |
| **安全回归** | `[security-regression]` | 默认 CI / `go test ./internal/security ./internal/observability ./internal/httpapi ./internal/toolgateway ./internal/agent/codex` | A8 |
| **real Codex opt-in acceptance** | `[real-codex-opt-in]` | 显式 `SYMPHONY_TEST_CODEX=1` | A10 |

`[fake]` 与 `[security-regression]` 不依赖本机真实 Codex；`acceptance-local.sh` 是本地主路径 smoke，不运行 A0-A9a 的全部 fake rejection matrix；`[real-codex-opt-in]` 必须 fixture 齐备且显式 opt-in，否则按 `unsupported_codex_version` 失败（fail-closed）。详见 `docs/productization/D6_DOCS_CLOSE_NOTES.md`。

---

## A0 Contract validation `[fake]`

```bash
scripts/validate-contracts.sh
```

必须验证：

```text
OpenAPI parseable
JSON schemas parseable
schemas/normalized_issue.schema.json matches OpenAPI Issue required fields
schemas/run_event.schema.json requires seq for SSE replay IDs
SQLite app/project DDL can initialize empty databases
examples/WORKFLOW.default.md validates
workflow config schema accepts unknown top-level keys so validation can emit warning-only diagnostics instead of schema errors
examples/handoff.json and examples/followup.json validate against schemas/tools/*.input.schema.json and wrapped Tool Gateway call schemas
docs/testing/CONTRACT_VALIDATION_MANIFEST.json parseable and complete for v1 manifest/snapshot contracts
required docs/testing, docs/codex, and docs/agent_work_orders M0-M8 files exist
OpenAPI contains every required non-excluded v1 route and no excluded API route fragments/patterns
OpenAPI forbidden method/path operations are validated for git publish/push/PR/create-pr, db backup/restore/migrate, audit, workspace delete/reset/clean/rebase, secrets, project settings, issue delete, and arbitrary state mutation
manifest OpenAPI route inventory stays aligned with validate_contracts.py required routes
handler route inventory maps to documented OpenAPI routes
CLI required/help tokens are declared and forbidden v1 commands are declared
dashboard action inventory declares required actions and forbidden hidden/future actions
Tool Gateway registry enum matches the fixed documented v1 tool list
Tool Gateway error enum includes `handoff_conflict` for mismatched handoff payload hashes
docs/agent_work_orders/*.md do not contain forbidden v1 command-like capability tokens
security regression command/topic manifest includes default fake-only commands and gates real Codex behind SYMPHONY_TEST_CODEX=1
```

## A0a WORKFLOW validation `[fake]`

Expected：

```text
unknown top-level config key produces a warning only and does not block dispatch
wrong type, missing required field, unsupported enum, and unset-or-empty full-string $VAR_NAME produce workflow validation errors
unset-or-empty full-string $VAR_NAME blocks dispatch and invalid reload does not replace the effective config
```

## A0b Workflow validate API `[fake]`

Call `POST /api/v1/workflow/validate` with omitted body, `{}`, and `{"dry_run": true}`.

Expected:

```text
validates the current filesystem WORKFLOW.md
returns data.source = current_filesystem
returns validation.valid / warnings / errors
returns side_effects fields all false
does not replace effective config
does not update last-valid config
does not render prompts, dispatch runs, or write review artifacts
candidate_workflow_md, candidate_config, render_context, unknown fields, malformed JSON, and dry_run=false return invalid_request
invalid WORKFLOW.md content returns 200 with validation.valid=false
```

## A1 Init and local tracker `[fake]`

```bash
symphony init --issue-prefix LOC
symphony issue create --title "Add greeting" --description "Implement a greeting helper" --priority 3
symphony issue transition LOC-1 Ready
symphony issue show LOC-1 --json
```

Expected：issue identifier is `LOC-1`, state is `Ready`, dispatch_paused is false.

## A2 Fake runner main path `[fake]`

```bash
symphony serve --project . --no-open
symphony issue dispatch LOC-1
symphony run events run_<from-dispatch-response> --follow
symphony review LOC-1
symphony review path LOC-1
```

Expected：

```text
worktree created under global workspace root
branch name starts with symphony/LOC-1-
handoff stored
review packet generated
issue state = Human Review
main repo working tree not polluted except documented init files
symphony review path prints metadata/path diagnostics only and does not print packet file contents or raw artifact bytes
```

## A3 Rework path `[fake]`

```bash
symphony review send-to-rework LOC-1 --reason "Need stronger tests"
symphony issue dispatch LOC-1
symphony review LOC-1
```

Expected：same workspace/branch reused; latest review packet has packet_no incremented; cumulative diff includes prior changes.

## A3a Send to Rework rejection matrix `[fake]`

Each rejection must return the listed error code and leave issue state, active run records, review packet metadata, review packet files, comments, and system events unchanged.

| Scenario | Expected error |
| --- | --- |
| missing, blank, or trim-to-empty reason | invalid_request |
| issue is not in Human Review | invalid_state_transition |
| issue has an active run | issue_already_running |
| latest review packet is missing | review_packet_required |
| latest review packet status is not generated | review_packet_required |
| latest review packet row is generated but does not belong to latest completed handoff run | review_packet_required |

## A4 Failure pause and resume `[fake]`

Use fake runner failure fixture.

Expected：

```text
run.status = failed
issue.state restored to source_issue_state Ready/Rework
issue.dispatch_paused = true
symphony issue dispatch LOC-1 returns state conflict while paused
symphony issue dispatch-resume LOC-1 clears pause
next dispatch succeeds when other eligibility guards pass
```

## A4a Manual dispatch control rejects active run `[fake]`

Start a run, then call manual dispatch pause and resume against the same Working issue.

Expected：

```text
symphony issue dispatch-pause LOC-1 returns issue_already_running
symphony issue dispatch-resume LOC-1 returns issue_already_running
issue.dispatch_paused is unchanged by both requests
issue.dispatch_pause_reason is unchanged by both requests
issue.dispatch_paused_at is unchanged by both requests
active run remains active unless separately cancelled
```

## A4a.1 Manual dispatch control rejects blank reason `[fake]`

Call manual dispatch pause and resume with missing, blank, and trim-to-empty `--reason` / request `reason` values.

Expected：

```text
symphony issue dispatch-pause LOC-1 returns invalid_request
symphony issue dispatch-resume LOC-1 returns invalid_request
issue.dispatch_paused is unchanged by both requests
issue.dispatch_pause_reason is unchanged by both requests
issue.dispatch_paused_at is unchanged by both requests
no system event or issue comment is appended
```

## A4b Reconciliation pause survives unblock `[fake]`

Start a run, use an operator transition to move its Working issue to Blocked, then resolve Blocked to Ready.

Expected：

```text
run.status = cancelled
run.failure_code = issue_state_changed
issue.state = Ready
issue.dispatch_paused = true
issue.dispatch_pause_reason = run.failure_code
next scheduler tick does not dispatch the issue
symphony issue dispatch-resume LOC-1 clears pause
next dispatch succeeds when other eligibility guards pass
```

## A5 Missing handoff `[fake]`

Use fake runner fixture that completes without handoff twice.

Expected：run.status = completed_without_handoff; failure_code = missing_handoff; issue restored to source state and paused.

## A6 Tool Gateway scope `[fake]`

Expected：

```text
agent can call issue.get for current issue
agent cannot fetch another issue
agent cannot mark Done
agent cannot attach artifact outside workspace
agent cannot handoff target other than Human Review
expired/revoked tool token fails
second handoff.submit for the same run with a different canonical payload hash returns handoff_conflict, CLI exit code 7, does not replace the original handoff, and does not advance Human Review
```

## A7 Review packet integrity `[fake]`

Expected review packet contains:

```text
review.md
review.json
changes.patch
changed-files.txt
untracked-files.json
diffstat.txt when available
prompt snapshot metadata
handoff payload
tool calls
approvals
redacted run events
```

Untracked files, binary-safe diffs, deletions, mode changes, and symlink escape failures must be tested.

Untracked file handling must be tested with concrete cases:

```text
ordinary text untracked file content is included in changes.patch
large untracked files, binary untracked files, and policy-restricted untracked files are listed in changed-files.txt
large untracked files, binary untracked files, and policy-restricted untracked files are listed in untracked-files.json
untracked-files.json sets patch_included=false for each omitted untracked file
untracked-files.json includes a non-empty reason for each omitted untracked file
```

## A8 Security `[security-regression]`

Expected：

```text
API binds loopback only by default
cookie-authenticated browser command APIs require X-Symphony-CSRF
CLI bearer works and can rotate
runtime descriptor contains no secrets
protected path access denies or requires review according to policy
network deny does not claim OS-level isolation
raw prompt context values are not exposed via API
raw secrets are not exposed via API
diagnostics export contains redacted values only
symphony review path does not bypass Review API + Artifact API redaction/refusal
security tests do not claim compliance-grade DLP
```

## A9 Done gate `[fake]`

```bash
symphony review mark-done LOC-1 --reason "Accepted"
```

Expected：issue state = Done; no git commit, push, PR, merge, publish, workspace cleanup, reset, or delete occurs.

## A9a Mark Done rejection matrix `[fake]`

Each rejection must return the listed error code and leave issue state, active run records, review packet metadata, review packet files, comments, and system events unchanged.

| Scenario | Expected error |
| --- | --- |
| missing, blank, or trim-to-empty reason | invalid_request |
| issue is not in Human Review | invalid_state_transition |
| issue has an active run | issue_already_running |
| latest review packet is missing | review_packet_required |
| latest review packet status is not generated | review_packet_required |
| latest review packet row is generated but does not belong to latest completed handoff run | review_packet_required |

## A10 Codex fixture gate `[real-codex-opt-in]`

Expected：

```text
prelaunch gate reads installed Codex version without starting the long-lived real codex app-server process
generated protocol/schema version comes from committed fixture metadata or static compatibility metadata
missing compatible metadata or fixture fails before process launch with unsupported_codex_version
post-launch handshake schema/protocol mismatch fails through codex_protocol_error
```
