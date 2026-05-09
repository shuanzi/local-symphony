# IS-006 — Orchestrator State Machine and Run Lifecycle

## Status

Frozen.

## Goal

Define the v1 orchestrator actor, dispatch loop, run lifecycle, failure behavior, handoff finalization, and the limited stale-run startup guard.

## Orchestrator actor

Use one authoritative actor:

```text
one goroutine
one command queue
one in-memory running map
durable run_attempts and run_events
```

Commands:

```text
Tick
DispatchIssue
CancelRun
ApprovalResolved
AgentRunCompleted
WorkflowReloaded
Shutdown
```

Only the orchestrator actor creates run attempts and decides dispatch. Worker goroutines report outcomes to the actor; they do not directly mutate scheduler state.

## Run statuses

```text
pending
preparing_workspace
rendering_prompt
starting_agent
running
completed
completed_without_handoff
failed
cancelled
```

Flow:

```text
pending
  ↓
preparing_workspace
  ↓
rendering_prompt
  ↓
starting_agent
  ↓
running
  ├── completed
  ├── completed_without_handoff
  ├── failed
  └── cancelled
```

## Dispatch claim transaction

Within one transaction:

```text
1. Read issue.
2. Validate state in active_states.
3. Validate dispatch_paused = false.
4. Validate no active blockers.
5. Validate no running run.
6. Create run_attempt with status=pending.
7. If issue.state != Working, transition issue to Working.
8. Insert issue_state_history if transitioned.
9. Insert scheduler.dispatch_claimed run_event.
```

Workspace preparation and Codex launch run outside the transaction.

## Run worker lifecycle

```text
1. status → preparing_workspace
2. WorkspaceManager.Prepare(issue)
3. status → rendering_prompt
4. PromptBuilder.Build(run)
5. Create run-scoped tool token
6. status → starting_agent
7. CodexRunner.Start()
8. status → running
9. Wait for turn complete/fail/cancel
10. If missing handoff, run at most one continuation
11. If handoff exists, generate review packet
12. Run hooks.after_run best-effort in a finally path if workspace exists
13. Return outcome to actor
```

## Tick loop

Each tick:

```text
1. If workflow invalid and no last valid config, skip dispatch.
2. Before-dispatch revalidate WORKFLOW.md.
3. Compute available concurrency slots.
4. Query eligible issues.
5. Sort by priority ASC, created_at ASC, identifier ASC.
6. Claim issues until slots exhausted.
7. Launch run workers.
8. Emit scheduler events.
```

## Retry strategy

v1 does not implement automatic retry queue or timers. Failures pause dispatch and require operator action.

Retain fields:

```text
attempt_no
dispatch_reason = retry
```

`dispatch_reason = retry` is reserved for an operator-initiated redispatch of a previously failed or paused issue. It must not imply an automatic retry timer, retry queue, or exponential backoff scheduler in v1.

## Failure behavior

On run failure:

```text
run_attempt.status = failed
run_attempt.failure_code = <code>
run_attempt.failure_message = <message>
issues.dispatch_paused = true
issues.dispatch_pause_reason = <code>
run_event = run.failed
system comment with failure summary
```

G3 freezes failure → dispatch pause behavior.

Common failure codes:

```text
workflow_validation_failed
prompt_render_error
workspace_prepare_failed
hook_failed
codex_startup_failed
codex_protocol_error
turn_timeout
stall_timeout
approval_timeout
tool_gateway_failed
handoff_missing
review_packet_failed
operator_cancelled
daemon_restarted_run_interrupted
```

## Missing handoff

```text
main turn completes
  ↓
no handoff
  ↓
if continuation unused: send one handoff continuation
  ↓
still no handoff:
  run.status = completed_without_handoff
  issue.dispatch_paused = true
  dispatch_pause_reason = missing_handoff
  system comment
  handoff.missing event
```

## after_run hook guarantee

If a workspace was prepared, the worker must attempt `hooks.after_run` in a `finally` path for all terminal worker outcomes:

```text
completed
completed_without_handoff
failed
cancelled
timeout-derived failure
review_packet_failed
```

`after_run` failure is recorded as hook events and diagnostics. It does not hide the original run outcome and does not automatically move the issue to Human Review.

Events:

```text
hook.after_run.started
hook.after_run.completed
hook.after_run.failed
hook.after_run.timeout
```

## Handoff finalizer

If handoff exists:

```text
1. Generate review packet.
2. Insert review_packet.status = generated.
3. run.status = completed.
4. issue.state → Human Review.
5. issue.dispatch_paused = false.
6. Insert issue_state_history.
7. Emit review.packet_generated.
```

If review packet fails:

```text
run.status = failed
issue remains not Human Review
issue.dispatch_paused = true
failure_code = review_packet_failed
```

## Startup stale-run guard

v1 does not implement crash recovery. On startup, if DB contains run attempts in active statuses:

```text
mark them failed
failure_code = daemon_restarted_run_interrupted
pause dispatch for their issues
emit system.interrupted event
```

G4 freezes this behavior.

## Dispatch pause/resume

G1 adds command endpoints and CLI:

```http
POST /api/v1/issues/{issue_id}/dispatch-pause
POST /api/v1/issues/{issue_id}/dispatch-resume
```

```bash
symphony issue dispatch-pause LOC-1 --reason "..."
symphony issue dispatch-resume LOC-1 --reason "..."
```

Resume only clears pause. It does not change state and does not override blockers.

## Frozen decisions

| ID | Decision |
|---|---|
| IS6-001 | single actor + command queue |
| IS6-002 | claim issue inside transaction |
| IS6-003 | worker executes lifecycle; actor owns outcome state |
| IS6-004 | no automatic retry queue in v1 |
| IS6-005 | missing handoff gets at most one continuation |
| IS6-006 | review packet success required for Human Review |
| IS6-007 | startup marks stale runs; no crash recovery |
| IS6-008 | dispatch pause/resume API and CLI required |
| G1 | pause/resume added |
| G3 | failures pause dispatch |
| G4 | stale active runs marked interrupted |
