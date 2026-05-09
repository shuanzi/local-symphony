# Normalized Issue DTO v1

## Purpose

`NormalizedIssue` is the stable issue shape shared by orchestrator dispatch, prompt rendering, API responses, dashboard state, and review packet metadata.

It adapts the upstream Symphony normalized issue model to the Local v1 SQLite tracker.

## Shape

```json
{
  "id": "iss_123",
  "identifier": "LOC-123",
  "title": "Add local tracker",
  "description": "Implement CRUD and state transitions.",
  "acceptance_criteria": [
    "Create issue",
    "Transition Ready -> Working"
  ],
  "priority": 3,
  "state": "Ready",
  "url": null,
  "labels": ["backend", "tracker"],
  "blocked_by": [
    {
      "id": "iss_100",
      "identifier": "LOC-100",
      "state": "Working"
    }
  ],
  "blocks": [
    {
      "id": "iss_200",
      "identifier": "LOC-200",
      "state": "Ready"
    }
  ],
  "dispatch_paused": false,
  "dispatch_pause_reason": null,
  "branch_name": "symphony/LOC-123-add-local-tracker-a1b2c3",
  "workspace_path": "/Users/me/.symphony/workspaces/proj_1/LOC-123",
  "base_ref": "origin/main",
  "base_sha": "abc123",
  "workspace": {
    "id": "ws_123",
    "path": "/Users/me/.symphony/workspaces/proj_1/LOC-123",
    "branch_name": "symphony/LOC-123-add-local-tracker-a1b2c3",
    "base_ref": "origin/main",
    "base_sha": "abc123",
    "status": "ready"
  },
  "latest_run": {
    "id": "run_123",
    "status": "completed",
    "attempt_no": 1
  },
  "latest_review_packet": {
    "id": "rev_123",
    "status": "generated"
  },
  "git": {
    "branch_name": "symphony/LOC-123-add-local-tracker-a1b2c3",
    "base_ref": "origin/main",
    "base_sha": "abc123"
  },
  "created_at": "2026-05-09T00:00:00Z",
  "updated_at": "2026-05-09T00:00:00Z"
}
```

## Rules

```text
labels are normalized to lowercase
url is null or a local dashboard URL
workspace is null until a workspace row exists
latest_run is null until a run exists
latest_review_packet is null until a review packet exists
branch_name, workspace_path, base_ref, and base_sha are top-level compatibility aliases and are null until a workspace row exists
git mirrors the git-related workspace fields for prompt ergonomics and is null until a workspace row exists
created_at and updated_at are RFC3339 UTC strings
```



## Upstream-compatible aliases

Local v1 exposes both upstream-compatible top-level fields and local nested fields.

Required aliases:

| Alias | Same value as | Reason |
|---|---|---|
| `issue.branch_name` | `issue.workspace.branch_name` / `git.branch_name` | Upstream prompt compatibility. |
| `issue.workspace_path` | `issue.workspace.path` / `workspace.path` | Prompt/API convenience. |
| `issue.base_ref` | `issue.workspace.base_ref` / `git.base_ref` | Upstream-style issue context. |
| `issue.base_sha` | `issue.workspace.base_sha` / `git.base_sha` | Review/diff context. |

Strict prompt rendering must support both of these examples:

```liquid
{{ issue.branch_name }}
{{ git.branch_name }}
```

## Blocker derivation

`issue_relations` stores only the direct relation type:

```text
source_issue_id blocks target_issue_id
```

Derived views:

```text
target.blocked_by includes source
source.blocks includes target
```

Only non-terminal blockers affect dispatch eligibility.

## Storage mapping

| DTO field | Source |
|---|---|
| id, identifier, title, description, priority, state | `issues` |
| acceptance_criteria | `issues.acceptance_criteria_json` |
| labels | `issue_labels` |
| blocked_by / blocks | `issue_relations` joined to `issues` |
| workspace.* | `workspaces` |
| branch_name, workspace_path, base_ref, base_sha | aliases from `workspaces` |
| git.* | aliases from `workspaces` |
| latest_run | latest `run_attempts` for issue |
| latest_review_packet | latest `review_packets` for issue |
