# IS-010 — Review Packet and Artifact Generator

## Status

Frozen.

## Goal

Define the Human Review deliverable: review packet files, generation sequence, database records, Mark Done gating, and artifact safety.

## Inputs

Review generator receives:

```text
issue
run_attempt
workspace
handoff
git diff/status
approval_requests
tool_calls
run_events
prompt_snapshot metadata
agent final message
```

## Output directory

```text
<repo>/.symphony/artifacts/<issue_identifier>/run_<run_id>/
```

Files:

```text
review.md
review.json
changes.patch
changed-files.txt
untracked-files.json
test-output.txt
agent-final-message.md
commands.jsonl
tool-calls.jsonl
approvals.jsonl
codex-events.redacted.jsonl
prompt/context.json
prompt/rendered_prompt.redacted.md
prompt/prompt_meta.json
```

## Generation sequence

```text
1. Ensure artifact dir exists.
2. Collect git status.
3. Generate changed-files.txt.
4. Generate changes.patch.
5. Collect untracked files.
6. Read handoff.
7. Export tool calls.
8. Export approvals.
9. Export redacted run events.
10. Write review.json.
11. Write review.md.
12. Insert artifacts rows.
13. Insert review_packets row.
14. Emit review.packet_generated.
```

Critical files required for `status=generated`:

```text
review.md
review.json
changes.patch
changed-files.txt
```

Non-critical files:

```text
agent-final-message.md
test-output.txt
codex-events.redacted.jsonl
```

If a critical step fails:

```text
review_packet.status = failed or omitted
run.status = failed
issue does not enter Human Review
issue.dispatch_paused = true
failure_code = review_packet_failed
```

## review.json shape

```json
{
  "issue": {
    "id": "iss_...",
    "identifier": "LOC-1",
    "title": "..."
  },
  "run": {
    "id": "run_...",
    "status": "completed"
  },
  "git": {
    "branch_name": "symphony/LOC-1-...",
    "base_ref": "auto",
    "base_sha": "...",
    "head_sha": "...",
    "dirty": true
  },
  "handoff": {
    "summary": "...",
    "tests": [],
    "risks": [],
    "verification": []
  },
  "changed_files": [],
  "untracked_files": [],
  "approvals": [],
  "tool_calls": [],
  "prompt_snapshot": {
    "id": "prm_...",
    "rendered_prompt_hash": "..."
  }
}
```

## review.md sections

```markdown
# LOC-1 Review Packet

## Summary
## Acceptance Criteria
## Handoff
## Changed Files
## Tests
## Risks
## Verification Steps
## Approvals
## Tool Calls
## Git
## How to Continue
```

## Human Review transition

The finalizer transitions the issue to `Human Review` only when:

```text
handoff exists for run
handoff.target_state = Human Review
critical review packet files are written
review_packets.status = generated
run terminal outcome is otherwise successful
```

Any other handoff target is invalid in v1 and must fail before finalizer transition.

## Mark Done gating

`review mark-done` requires:

```text
issue.state = Human Review
latest review_packet.status = generated
review_packet.run_id belongs to latest completed handoff run
```

Partial/failed review packets can be viewed but cannot Mark Done.

## Artifact API safety

Artifact content endpoint must ensure:

```text
artifact path is project-local relative path
resolved path under .symphony/artifacts or .symphony/exports
no path traversal
no raw prompt export
no raw Codex log export in v1
```

## Frozen decisions

| ID | Decision |
|---|---|
| IS10-001 | review packet generation is finalizer responsibility |
| IS10-002 | critical files required for generated status |
| IS10-003 | review.md and review.json both required |
| IS10-004 | Git diff generated against workspace base_sha |
| IS10-005 | Mark Done requires latest generated review packet |
| IS10-006 | partial packets are view-only |
| IS10-007 | artifact endpoint requires containment checks |
| IS10-008 | no raw prompt/raw Codex export in v1 |
| IS10-009 | finalizer only supports `Human Review` target state in v1 |
