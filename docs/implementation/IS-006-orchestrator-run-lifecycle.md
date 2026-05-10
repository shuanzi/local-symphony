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

Active run statuses:

```text
pending
preparing_workspace
rendering_prompt
starting_agent
running
```

Terminal run statuses:

```text
completed
completed_without_handoff
failed
cancelled
```

Upstream terminal outcome mapping:

| Upstream concept | Local status | Local `failure_code` / reason |
|---|---|---|
| Succeeded with handoff | `completed` | null |
| Succeeded without handoff | `completed_without_handoff` | `missing_handoff` |
| Failed | `failed` | canonical failure code |
| TimedOut | `failed` | `turn_timeout` |
| Stalled | `failed` | `stall_timeout` |
| CanceledByReconciliation | `cancelled` | `issue_state_changed` or `canceled_by_reconciliation` |
| Operator cancel | `cancelled` | `operator_cancelled` |
| Approval decision `cancel_run` | `cancelled` | `operator_cancelled` |
| Agent blocked current issue | `cancelled` | `agent_blocked` |

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
5. Validate no active run for the same issue.
6. Create run_attempt with status=pending. This pending row is the local claim.
7. If issue.state != Working, transition issue to Working.
8. Insert issue_state_history if transitioned.
9. Insert scheduler.dispatch_claimed run_event.
```

Workspace preparation and Codex launch run outside the transaction.

## Run worker lifecycle

```text
1. status → preparing_workspace
2. WorkspaceManager.Prepare(issue): create/reuse worktree, run `after_create` if new, then always run `before_run`
3. status → rendering_prompt
4. PromptBuilder.Build(run)
5. Create run-scoped tool token
6. status → starting_agent
7. CodexRunner.Start()
8. status → running
9. Wait for turn complete/fail/cancel
10. If missing handoff, run at most one continuation
11. Run hooks.after_run best-effort in a finally path if workspace exists
12. If handoff exists and the run was not cancelled by a higher-precedence outcome, generate review packet
13. Return outcome to actor
```

## Tick loop

Each tick:

```text
1. Reconcile active runs against current issue state and process liveness.
2. If workflow invalid and no last valid config, skip dispatch.
3. Before-dispatch revalidate WORKFLOW.md.
4. Compute available concurrency slots.
5. Query eligible issues.
6. Sort by priority ASC, created_at ASC, identifier ASC.
7. Claim issues until slots exhausted.
8. Launch run workers.
9. Emit scheduler events.
```


## Dispatch eligibility

The scheduler normally claims only issues in:

```text
Ready
Rework
```

`Working` is an active-dispatch-eligible state for reconciliation purposes, but it is not a normal scheduler candidate. A `Working` issue with no active run must not be redispatched automatically unless one of these explicit recovery paths applies:

```text
operator manually dispatches the issue after clearing dispatch pause
startup/interruption handling deliberately leaves issue Working and operator requests retry
future continuation/recovery feature explicitly records dispatch_reason=retry
```

This rule prevents accidental redispatch of stale `Working` rows. Tests must cover a `Working` issue with no active run and `dispatch_paused=true` staying idle across ticks.

## Retry strategy

v1 does not implement automatic retry queue or timers. Failures pause dispatch and require operator action.

Retain fields:

```text
attempt_no
dispatch_reason = retry
```

`dispatch_reason = retry` is reserved for an operator-initiated redispatch of a previously failed or paused issue. It must not imply an automatic retry timer, retry queue, or exponential backoff scheduler in v1.


## Active run reconciliation

Active run reconciliation is required in v1.

A run is active when its status is one of:

```text
pending
preparing_workspace
rendering_prompt
starting_agent
running
```

An issue is active-dispatch eligible for reconciliation only when its state is in:

```text
Ready
Working
Rework
```

This set is broader than normal scheduler claim eligibility; see Dispatch eligibility above.

Reconciliation triggers:

```text
orchestrator tick
issue transition command
operator run cancel
agent issue.block tool
startup stale-run guard
```

Rules:

```text
If an issue with an active run leaves Ready/Working/Rework:
  1. send CancelRun to the orchestrator actor
  2. terminate the Codex process group if it exists
  3. set run_attempt.status = cancelled
  4. set failure_code = issue_state_changed unless a more specific code applies
  5. set ended_at
  6. emit run.cancelled and scheduler.reconciled events
  7. retain workspace without reset/clean/delete
```

Specific codes:

| Trigger | Local status | failure_code |
|---|---|---|
| Operator `POST /runs/{id}/cancel` or CLI `symphony run cancel` | `cancelled` | `operator_cancelled` |
| Approval decision `cancel_run` | `cancelled` | `operator_cancelled` |
| Issue transitioned to `Blocked` by agent `issue.block` | `cancelled` | `agent_blocked` |
| Issue transitioned to inactive/terminal by operator | `cancelled` | `issue_state_changed` |
| Reconciliation finds active run for terminal issue | `cancelled` | `canceled_by_reconciliation` |
| Daemon restart finds active DB rows but no process ownership | `failed` | `daemon_restarted_run_interrupted` |

State changes do not delete workspace state. Review packet generation is not attempted for reconciliation-cancelled runs unless a successful handoff already exists and the worker has reached the finalizer step.

## Cancellation behavior

Operator-initiated cancellation is terminal and pauses redispatch. This applies to:

```text
POST /api/v1/runs/{run_id}/cancel
symphony run cancel run_...
approval decision = cancel_run
```

Required side effects:

```text
run_attempt.status = cancelled
run_attempt.failure_code = operator_cancelled
run_attempt.failure_message = <operator or approval reason>
run_attempt.ended_at = now
issues.dispatch_paused = true
issues.dispatch_pause_reason = operator_cancelled
issues.dispatch_paused_at = now
issue.state remains unchanged unless a separate operator transition command changes it
revoke run-scoped tool tokens
run_event = run.cancelled
scheduler.paused event
system comment with cancellation summary
```

A cancelled run must not be automatically redispatched on the next tick. The operator must explicitly call dispatch-resume, and then dispatch again or allow the scheduler to claim the issue if it remains in an active-dispatch-eligible state.

Agent-driven `issue.block` cancellation uses the same no-automatic-redispatch rule, but with `failure_code=agent_blocked` and `dispatch_pause_reason=agent_blocked`.

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

Canonical failure codes:

| Code | Meaning |
|---|---|
| `workflow_invalid` | Effective workflow config is invalid and blocks dispatch. |
| `workflow_validation_failed` | Workflow reload/validation failed for a specific operation. |
| `prompt_render_failed` | Strict prompt rendering failed. |
| `workspace_prepare_failed` | Worktree preparation failed before hook-specific classification. |
| `workspace_conflict` | Existing workspace path, branch, or DB metadata conflicts with the current issue/workspace ownership. |
| `after_create_failed` | `hooks.after_create` failed. |
| `before_run_failed` | `hooks.before_run` failed. |
| `codex_startup_failed` | Codex process could not start or failed startup handshake. |
| `unsupported_codex_version` | Installed Codex version is incompatible with selected adapter fixture. |
| `codex_protocol_error` | Codex protocol framing/schema mismatch. |
| `turn_timeout` | Turn exceeded `codex.turn_timeout_ms`. |
| `stall_timeout` | No protocol progress within `codex.stall_timeout_ms`. |
| `approval_timeout` | Pending approval expired. |
| `command_denied` | Command policy denied a command required for the run to proceed. |
| `network_denied` | Network policy denied an egress request required for the run to proceed. |
| `protected_path_denied` | Protected path policy denied file read/write/artifact access required for the run to proceed. |
| `tool_gateway_failed` | Required tool call failed for daemon/gateway reasons. |
| `missing_handoff` | Required handoff missing after allowed continuation. |
| `review_packet_failed` | Review packet finalizer failed. |
| `operator_cancelled` | Operator cancelled run. |
| `agent_blocked` | Agent blocked the current issue through `issue.block`. |
| `issue_state_changed` | Issue left an active state while run was active. |
| `canceled_by_reconciliation` | Reconciliation cancelled a stale/ineligible active run. |
| `daemon_restarted_run_interrupted` | Startup found active DB row from previous daemon process. |

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

If a workspace was prepared, the worker must attempt `hooks.after_run` in a `finally` path for all terminal worker outcomes before any successful handoff review packet is generated:

```text
completed
completed_without_handoff
failed
cancelled
timeout-derived failure
```

`after_run` failure is recorded as hook events and diagnostics. It does not hide the original run outcome and does not automatically move the issue to Human Review. If `after_run` writes test reports, formatting changes, or diagnostics into the workspace/artifact area before a successful handoff finalizer, the review packet must capture the resulting workspace state.

Events:

```text
hook.after_run.started
hook.after_run.completed
hook.after_run.failed
hook.after_run.timeout
```

## Handoff finalizer

If handoff exists and no higher-precedence cancellation/failure has already won:

```text
1. Confirm hooks.after_run has already been attempted if workspace exists.
2. Generate review packet from current workspace state.
3. Insert review_packet.status = generated.
4. run.status = completed.
5. issue.state → Human Review. This is the only supported v1 handoff target state.
6. issue.dispatch_paused = false.
7. Insert issue_state_history.
8. Emit review.packet_generated.
```

If review packet fails:

```text
run.status = failed
issue remains not Human Review
issue.dispatch_paused = true
failure_code = review_packet_failed
```


## Run outcome precedence

When multiple outcomes race, apply this precedence before writing terminal state:

| Priority | Outcome | Final status/code | Notes |
|---:|---|---|---|
| 1 | Operator run cancel or approval `cancel_run` before finalizer commit | `cancelled` / `operator_cancelled` | Cancels process group, revokes tool token, pauses dispatch. |
| 2 | Issue leaves active-dispatch-eligible state before finalizer commit | `cancelled` / `issue_state_changed` or `agent_blocked` | Reconciliation wins unless finalizer transaction already committed Human Review. |
| 3 | Startup stale active run guard | `failed` / `daemon_restarted_run_interrupted` | Applies only before a live worker owns the process. |
| 4 | Codex/runner/protocol/workspace/prompt failure | `failed` / canonical failure code | Pauses dispatch. |
| 5 | Missing handoff after continuation | `completed_without_handoff` / `missing_handoff` | Pauses dispatch. |
| 6 | Handoff exists but review packet fails | `failed` / `review_packet_failed` | Issue must not enter Human Review. |
| 7 | Handoff exists and review packet generated | `completed` / null | Transitions issue to Human Review. |

The finalizer must commit run status, issue state, dispatch pause fields, review packet row, and state history in a single DB transaction where possible. Once that transaction commits `issue.state=Human Review` and `run.status=completed`, a later operator action must use a separate state transition or review command rather than rewriting the completed run.

| Current phase | Concurrent event | Required result |
|---|---|---|
| running, no handoff | operator cancel | `cancelled/operator_cancelled`; no review packet. |
| running, handoff submitted, finalizer not committed | operator cancel | `cancelled/operator_cancelled`; handoff row remains diagnostic; no Human Review. |
| after_run executing | issue transitioned Blocked/Cancelled/Duplicate | reconciliation cancel wins; no Human Review. |
| review packet files writing, DB not committed | operator cancel | cancellation wins; packet may be `partial`/`failed`, not `generated`. |
| finalizer DB transaction committed | later operator cancel request | reject as not active; operator must use review/state command. |

## Issue state transitions

Issue state transitions must use `/api/v1/issues/{issue_ref}/transition` or the equivalent CLI/operator command, except orchestrator-owned dispatch/finalizer transitions.

| From | To | Actor | Guard / side effect |
|---|---|---|---|
| `Inbox` | `Ready` | operator | Required issue fields valid. |
| `Ready` | `Working` | orchestrator | Dispatch claim transaction succeeds. |
| `Rework` | `Working` | orchestrator | Dispatch claim transaction succeeds. |
| `Working` | `Human Review` | run finalizer | Handoff exists and review packet status is `generated`. |
| `Human Review` | `Rework` | operator | Reviewer supplies reason or feedback comment. |
| `Human Review` | `Done` | operator | Latest review packet status is `generated`; no active run. |
| any non-terminal | `Blocked` | operator or agent tool | If active run exists, reconciliation cancels it. Agent tool also sets `dispatch_paused=true`. |
| any non-terminal | `Cancelled` | operator | If active run exists, reconciliation cancels it. |
| any non-terminal | `Duplicate` | operator | If active run exists, reconciliation cancels it. Duplicate target is recommended and may be required by UI. |
| `Blocked` | `Ready` | operator | Block reason resolved; blockers inactive or removed. |
| `Done` / `Cancelled` / `Duplicate` | non-terminal | operator | Allowed only through explicit reopen command; does not reuse old active runs. |

Terminal states:

```text
Done
Cancelled
Duplicate
```

`Human Review` is not terminal. Workspaces are retained for all states in v1.

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
POST /api/v1/issues/{issue_ref}/dispatch-pause
POST /api/v1/issues/{issue_ref}/dispatch-resume
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
| IS6-009 | active run reconciliation cancels runs whose issues leave active states |
| IS6-010 | canonical failure code table is the source for run terminal reasons, dispatch pause reasons, and dashboard run labels |
| IS6-011 | issue transition matrix defines allowed state changes and active-run side effects |
| IS6-012 | operator cancellation and approval `cancel_run` pause dispatch and require explicit resume before redispatch |
| IS6-013 | normal scheduler claims only `Ready` and `Rework`; `Working` is for active-run reconciliation and explicit recovery, not ordinary redispatch |
| IS6-014 | `hooks.after_run` runs before successful handoff review packet generation so packet captures after-run workspace changes |
| IS6-015 | run outcome precedence table resolves cancel/finalizer/state-change races |
| G1 | pause/resume added |
| G3 | failures pause dispatch |
| G4 | stale active runs marked interrupted |
| G7 | non-active issue transitions cancel active runs |
| G8 | `Human Review` is the only supported v1 handoff target |
