# IS-016 — Rework Flow and Review Packet Versioning

## Status

Frozen.

## Goal

Define exact behavior for `Human Review → Rework → Working → Human Review` loops, workspace reuse, diff semantics, and review packet immutability.

## Rework state transition

`POST /api/v1/reviews/{issue_ref}/send-to-rework` and `symphony review send-to-rework` are the only v1 review rework entrypoints.

Required guards:

```text
issue.state = Human Review
latest review_packet.status = generated
no active run for issue
operator supplies non-empty reason or feedback comment
```

Required side effects:

```text
issue.state = Rework
issues.dispatch_paused = false
issues.dispatch_pause_reason = null
issues.dispatch_paused_at = null
insert issue_state_history Human Review → Rework
insert operator/system comment with feedback
emit review.sent_to_rework event
```

Send-to-rework does not delete, reset, clean, or rebase the workspace.

## Dispatch from Rework

When a Rework issue is dispatched:

```text
dispatch_reason = rework
same workspace row is reused
same branch is reused
same base_sha is retained
before_run hook runs
new prompt includes latest review feedback and previous review packet summary
```

The scheduler may claim `Rework` issues when dispatch is not paused and blockers are inactive.

## Cumulative diff semantics

Review packets are cumulative from the workspace `base_sha` to the current workspace tree.

This applies to first review and all rework reviews:

```text
changes.patch = diff(base_sha, current workspace tree)
changed_files.json = current changed files relative to base_sha
untracked_files.json = current untracked files relative to base_sha
```

The packet is **not** an incremental diff from the previous review packet.

Rationale: reviewers need the complete candidate change set for Done, and the workspace is intentionally not reset between rework attempts.

## Review packet immutability

Review packet rows and files are immutable after creation. A new Human Review entry creates a new `review_packets` row.

Rules:

```text
latest packet = most recent generated review_packet for issue
Mark Done uses latest generated packet
older packets remain visible in review history
older packets are never overwritten by rework
failed or partial packets remain diagnostic records and do not satisfy Mark Done
```

## Prompt context for rework

Prompt builder must include:

```text
current issue fields
current state = Rework
review feedback comment/reason
latest generated review packet metadata
previous handoff summary when available
workspace/git aliases from NormalizedIssue
```

The prompt must not include raw unredacted logs or raw prompt artifacts.

## Handoff after rework

The rework run must submit a new handoff for its own run. Handoff idempotency is per run.

The finalizer then:

```text
1. runs after_run hook if workspace exists
2. generates a new cumulative review packet from base_sha
3. marks run completed
4. transitions issue Working → Human Review
```

## Done after rework

`mark-done` requires:

```text
issue.state = Human Review
latest generated review_packet exists
no active run for issue
```

`mark-done` does not commit, push, merge, delete workspace, or create PR in v1.

## Cancellation and failure during rework

Failures during a rework run follow IS-006:

```text
run.status = failed or cancelled
issues.dispatch_paused = true
issue.state remains Working unless a separate transition/reconciliation changes it
workspace is retained
latest previous generated review packet remains available but does not describe the failed run
```

Operator may resume dispatch and run another rework attempt.

## Frozen decisions

| ID | Decision |
|---|---|
| IS16-001 | send-to-rework requires Human Review and latest generated packet. |
| IS16-002 | Rework dispatch reuses workspace, branch, and base_sha. |
| IS16-003 | review packet diffs are cumulative from base_sha, not incremental. |
| IS16-004 | review packets are immutable; older packets remain visible. |
| IS16-005 | Mark Done uses latest generated packet only. |
| IS16-006 | rework failure pauses dispatch and retains workspace. |
