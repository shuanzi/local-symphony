---
tracker:
  kind: local
  active_states:
    - Ready
    - Working
    - Rework
  terminal_states:
    - Done
    - Cancelled
    - Duplicate

polling:
  interval_ms: 30000

workspace:
  cleanup:
    done_retention_days: 14
    require_snapshot_before_delete: false

hooks:
  after_create: null
  before_run: null
  after_run: null
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
  command: codex app-server
  transport: stdio
  startup_timeout_ms: 60000
  turn_timeout_ms: 3600000
  stall_timeout_ms: 300000
  experimental_api: false

approvals:
  mode: balanced
  network:
    default: deny
    allowlist: []
  protected_paths:
    - ".env"
    - ".env.*"
    - "**/*.pem"
    - "**/*.key"
    - ".ssh/**"
    - ".aws/**"
    - ".kube/**"
    - ".npmrc"
    - ".pypirc"
    - ".netrc"

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

observability:
  structured_logs: true
  log_level: info
  redact_secrets: true
  raw_codex_log_retention_days: 30

server:
  host: 127.0.0.1
  port: 0
  open_browser_on_start: false

ui:
  default_view: overview

prompt:
  max_context_bytes: 200000
  include_previous_runs: true
  previous_run_limit: 3
  include_tool_manifest: true
  save_prompt_snapshot: redacted
---
You are working on local issue {{ issue.identifier }}.

Title:
{{ issue.title }}

Description:
{{ issue.description | default: "No description provided." }}

Acceptance criteria:
{{ issue.acceptance_criteria | bullet_list }}

Use only the current workspace.

Do not:
- Push branches.
- Create pull requests.
- Mark the issue Done.
- Modify files outside the workspace.

When implementation is ready:
1. Run relevant tests.
2. Create a `handoff.json` file.
3. Run `symphony tool handoff --json ./handoff.json`.

The handoff tool submits handoff data only. Do not assume the issue is in Human Review until the review-packet finalizer succeeds.

The handoff JSON should include:
- summary
- changed_files
- tests
- risks
- verification
- followups, if any
