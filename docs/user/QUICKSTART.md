# Quickstart — Local Symphony App v1

## Prerequisites

```text
Git repository
Go toolchain for backend development
Node/pnpm for dashboard development
Codex CLI/app-server for real Codex runs
```

Default tests use the fake runner and do not require a real Codex binary.

## Initialize a project

From a Git repository:

```bash
symphony init --name "My Project" --issue-prefix LOC
```

This creates:

```text
.symphony/symphony.db
WORKFLOW.md
```

## Start the daemon

```bash
symphony serve --project . --host 127.0.0.1 --port 0 --open
```

The daemon writes a local runtime descriptor with no secrets:

```text
~/.symphony/runtime/<project_id>.json
```

## Open the dashboard

```bash
symphony open --project .
```

`symphony open` creates a short-lived one-time token and exchanges it for a browser session.

## Create and dispatch an issue

```bash
symphony issue create \
  --title "Implement a small feature" \
  --description "Describe the desired behavior" \
  --label feature \
  --priority 3

symphony issue transition LOC-1 Ready
symphony issue dispatch LOC-1
```

The orchestrator creates or reuses a git worktree, starts the configured agent runner, and waits for a `handoff.submit` tool call.

## Review work

After a successful handoff and review packet generation:

```bash
symphony review LOC-1
symphony review mark-done LOC-1 --reason "Reviewed and accepted"
```

To request more work:

```bash
symphony review send-to-rework LOC-1 --reason "Please address the review feedback"
```

Rework reuses the same workspace and produces a new cumulative review packet.

## Pause or resume dispatch

```bash
symphony issue dispatch-pause LOC-1 --reason "Investigating failure"
symphony issue dispatch-resume LOC-1 --reason "Ready to retry"
```

Resume only clears the pause. It does not change issue state or remove blockers.

## Cancel a run

```bash
symphony run cancel run_... --reason "Operator cancelled"
```

Cancellation keeps the workspace, pauses dispatch, and does not automatically redispatch.

## Real Codex tests

Default CI and local tests should use the fake runner. Real Codex tests are opt-in:

```bash
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex/...
```
