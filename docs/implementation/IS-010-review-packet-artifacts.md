# IS-010 — Review Packet and Artifact Generator

## Status

Frozen.

## Goal

Define the Human Review deliverable: review packet files, generation sequence, database records, Mark Done gating, rework packet versioning, and artifact safety.

## Inputs

Review generator runs after `hooks.after_run` has been attempted when a workspace exists. This ensures formatter output, test reports, and after-run diagnostics can be captured in the review packet.

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
prompt/tool_manifest.md
```

## Generation sequence

Use the file/DB atomicity rules in `docs/implementation/IS-014-store-contract.md`: write into a temporary artifact directory, rename into place, then insert DB rows.

```text
1. Ensure artifact dir exists.
2. Collect git status.
3. Collect tracked changed files and untracked files.
4. Generate changed-files.txt with tracked and untracked workspace-relative paths.
5. Generate changes.patch with tracked diffs plus untracked new-file patch content.
6. Write untracked-files.json, even when empty.
7. Read handoff.
8. Export tool calls.
9. Export approvals.
10. Export redacted run events.
11. Copy prompt snapshot metadata files, including tool_manifest.md when available.
12. Write review.json.
13. Write review.md.
14. Insert artifacts rows.
15. Insert review_packets row.
16. Emit review.packet_generated.
```

Critical files required for `status=generated`:

```text
review.md
review.json
changes.patch
changed-files.txt
untracked-files.json
```

Non-critical files:

```text
agent-final-message.md
test-output.txt
codex-events.redacted.jsonl
prompt/context.json
prompt/rendered_prompt.redacted.md
prompt/prompt_meta.json
prompt/tool_manifest.md
```

Artifact rows must use these `kind` values for the core files:

| File | artifact.kind | review_packets column |
|---|---|---|
| `review.md` / `review.json` | `review_packet` | `review_md_path` / `review_json_path` |
| `changes.patch` | `patch` | `patch_path` |
| `changed-files.txt` | `changed_files` | `changed_files_path` |
| `untracked-files.json` | `untracked_files` | `untracked_files_path` |
| `prompt/*` | `prompt_snapshot` | `prompt_snapshot_id` via `prompt_snapshots` |

If a critical step fails:

```text
review_packet.status = failed or omitted
run.status = failed
issue does not enter Human Review
issue.dispatch_paused = true
failure_code = review_packet_failed
```

## Untracked file guarantee

A review packet with untracked files is not `generated` unless the untracked file contents are represented in `changes.patch`. `untracked-files.json` is always written and uses this shape:

```json
[
  {
    "path": "src/new-file.ts",
    "size_bytes": 1234,
    "sha256": "...",
    "patch_included": true
  }
]
```

Protected paths, path traversal, or files outside the workspace must fail review generation with `review_packet_failed`; they must not be silently omitted from the packet.

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
    "base_ref": "origin/main",
    "base_ref_config": "auto",
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
    "id": "ps_...",
    "rendered_prompt_hash": "...",
    "tool_manifest_path": "prompt/tool_manifest.md"
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


## Rework review packets

Review packets are immutable. Rework creates a new packet instead of overwriting the previous one.

For first review and rework reviews alike, `changes.patch`, `changed-files.txt`, and `untracked-files.json` are cumulative from the workspace `base_sha` to the current workspace tree. They are not incremental from the previous review packet. See `docs/implementation/IS-016-rework-flow.md`.

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
protected path access denied
no raw prompt export
no raw Codex log export in v1
```

## Frozen decisions

| ID | Decision |
|---|---|
| IS10-001 | review packet generation is finalizer responsibility |
| IS10-002 | critical files required for generated status |
| IS10-003 | review.md and review.json both required |
| IS10-004 | Git diff generated against workspace base_sha and includes untracked new-file content |
| IS10-005 | Mark Done requires latest generated review packet |
| IS10-006 | partial packets are view-only |
| IS10-007 | artifact endpoint requires containment checks |
| IS10-008 | no raw prompt/raw Codex export in v1 |
| IS10-009 | finalizer only supports `Human Review` target state in v1 |
| IS10-010 | review.json stores resolved `base_ref` plus `base_ref_config` when config used `auto` |
| IS10-011 | prompt snapshot files use the `prompt/` subdirectory and include `tool_manifest.md` when available |
| IS10-012 | review packet generation occurs after `hooks.after_run` so the packet captures after-run workspace/artifact output |
| IS10-013 | rework review packets are immutable and cumulative from `base_sha`, not incremental from the prior packet |
| IS10-014 | file/DB atomicity follows IS-014 temp-write then rename then DB transaction rules |
