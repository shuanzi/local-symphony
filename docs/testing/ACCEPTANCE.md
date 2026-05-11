# Local Symphony App v1 Acceptance

## A0 Contract validation

```bash
scripts/validate-contracts.sh
```

必须验证：

```text
OpenAPI parseable
JSON schemas parseable
SQLite app/project DDL can initialize empty databases
examples/WORKFLOW.default.md validates
examples/handoff.json and examples/followup.json validate against schemas/tools/*.input.schema.json and wrapped Tool Gateway call schemas
```

## A1 Init and local tracker

```bash
symphony init --issue-prefix LOC
symphony issue create --title "Add greeting" --description "Implement a greeting helper" --priority 3
symphony issue transition LOC-1 Ready
symphony issue show LOC-1 --json
```

Expected：issue identifier is `LOC-1`, state is `Ready`, dispatch_paused is false.

## A2 Fake runner main path

```bash
symphony serve --project . --no-open
symphony issue dispatch LOC-1
symphony run events run_<from-dispatch-response> --follow
symphony review LOC-1
```

Expected：

```text
worktree created under global workspace root
branch name starts with symphony/LOC-1-
handoff stored
review packet generated
issue state = Human Review
main repo working tree not polluted except documented init files
```

## A3 Rework path

```bash
symphony review send-to-rework LOC-1 --reason "Need stronger tests"
symphony issue dispatch LOC-1
symphony review LOC-1
```

Expected：same workspace/branch reused; latest review packet has packet_no incremented; cumulative diff includes prior changes.

## A4 Failure pause and resume

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

## A5 Missing handoff

Use fake runner fixture that completes without handoff twice.

Expected：run.status = completed_without_handoff; failure_code = missing_handoff; issue restored to source state and paused.

## A6 Tool Gateway scope

Expected：

```text
agent can call issue.get for current issue
agent cannot fetch another issue
agent cannot mark Done
agent cannot attach artifact outside workspace
agent cannot handoff target other than Human Review
expired/revoked tool token fails
```

## A7 Review packet integrity

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

## A8 Security

Expected：

```text
API binds loopback only by default
cookie-authenticated browser command APIs require X-Symphony-CSRF
CLI bearer works and can rotate
runtime descriptor contains no secrets
protected path access denies or requires review according to policy
network deny does not claim OS-level isolation
raw prompt/raw Codex log not exposed via API
```

## A9 Done gate

```bash
symphony review mark-done LOC-1 --reason "Accepted"
```

Expected：issue state = Done; no git commit, push, PR, merge, publish, workspace cleanup, reset, or delete occurs.
