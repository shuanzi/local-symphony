---
tracker:
  kind: local
  project: default
  dispatch_candidate_states: [Ready, Rework]
  reconciliation_active_states: [Ready, Working, Rework]
  terminal_states: [Done, Cancelled, Duplicate]

polling:
  interval_ms: 30000

workspace:
  root: "~/.symphony/workspaces/{{ project.id }}"
  cleanup:
    enabled: false

hooks:
  after_create: null
  before_run: null
  after_run: "go test ./..."
  before_remove: null
  timeout_ms: 300000
  max_output_bytes: 65536

git:
  enabled: true
  mode: worktree
  repo_root: .
  base_ref: auto
  branch_prefix: symphony
  agent_commit: manual
  auto_push: false
  auto_rebase: false
  submodules: false
  provider:
    kind: none

agent:
  max_concurrent_agents: 3
  max_turns_per_run: 2
  max_handoff_continuations: 1
  handoff_required: true
  handoff_state: Human Review
  pause_on_missing_handoff: true

codex:
  command: "codex app-server"
  transport: stdio
  startup_timeout_ms: 60000
  turn_timeout_ms: 3600000
  stall_timeout_ms: 300000
  read_timeout_ms: 5000
  experimental_api: false
  require_committed_fixture: true

tools:
  gateway: cli
  require_handoff_tool: true
  allow_dynamic_tools: false
  allow_mcp: false
  agent_can_create_followups: true
  agent_can_set_blocked: true
  agent_can_set_terminal_state: false
  artifact_max_bytes: 10485760

security:
  mode: balanced-secure
  require_loopback_api: true
  allow_remote_api: false
  require_session_token: true
  require_csrf: true
---

You are working on Local Symphony issue {{ issue.identifier }}: {{ issue.title }}.

Rules:
- Work only inside the current issue workspace: {{ workspace.path }}.
- Do not modify the main repo working tree.
- Do not push branches, create PRs, merge, publish, or mark the issue Done.
- Do not commit unless the operator explicitly requests it outside this run.
- Use only the provided `symphony tool ...` commands for issue state, artifacts, followups, block, and handoff.
- When finished, write `handoff.json` and run:

```bash
symphony tool handoff submit --json ./handoff.json
```

Required handoff fields:

```json
{
  "summary": "What was completed.",
  "changed_files": [],
  "tests": [],
  "risks": [],
  "verification": [],
  "followups": [],
  "target_state": "Human Review"
}
```

The handoff only submits completion data. The system will run after_run and generate a review packet before moving the issue to Human Review.
