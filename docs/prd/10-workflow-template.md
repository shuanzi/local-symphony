# WORKFLOW.md v1 模板

下面是 v1 默认 `WORKFLOW.md` 模板。

```yaml
---
tracker:
  kind: local
  project: default
  active_states:
    - Ready
    - Working
    - Rework
  terminal_states:
    - Done
    - Cancelled
    - Duplicate

workspace:
  root: ~/.symphony/workspaces/{{ project.id }}
  cleanup:
    done_retention_days: 14
    require_snapshot_before_delete: false

git:
  enabled: true
  mode: worktree
  repo_root: .
  base_ref: origin/main
  branch_prefix: symphony
  agent_commit: manual
  auto_push: false
  auto_rebase: false
  submodules: false
  provider:
    kind: none

agent:
  max_turns_per_run: 2
  max_handoff_continuations: 1
  handoff_required: true
  handoff_state: Human Review
  pause_on_missing_handoff: true

codex:
  command: codex app-server
  transport: stdio
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

tools:
  gateway: cli
  require_handoff_tool: true
  allow_dynamic_tools: false
  allow_mcp: false
  agent_can_create_followups: true
  agent_can_set_blocked: true
  agent_can_set_terminal_state: false

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
---
You are working on local issue {{ issue.identifier }}.

Use only the current workspace.

Do not push branches.
Do not create pull requests.
Do not mark the issue Done.

When implementation is ready:
1. Run relevant tests.
2. Summarize changed files.
3. Record risks and verification steps.
4. Call `symphony tool handoff`.
```

## 配置说明

### tracker

定义本地 tracker 类型、active states 和 terminal states。

### workspace

定义 workspace root。v1 默认代码 workspace 放在全局 `~/.symphony/workspaces/<project_id>/`，不放在 repo 内。

### git

定义 worktree 模式、base ref、branch prefix 与发布策略。

v1 默认：

```text
agent 不 commit
agent 不 push
agent 不 rebase
Git provider = none
```

### agent

定义 run turn 数、handoff 策略和 missing handoff 行为。

### codex

定义 Codex app-server 启动方式。

v1 默认：

```text
stdio transport
experimental_api = false
```

### approvals

定义审批策略。

v1 默认：

```text
mode = balanced
network default deny
protected paths enabled
```

### tools

定义本地工具策略。

v1 默认：

```text
CLI gateway
dynamic tools disabled
MCP disabled
agent cannot set terminal state
```

### security

定义本地安全基线。

### observability

定义日志和 redaction 策略。
