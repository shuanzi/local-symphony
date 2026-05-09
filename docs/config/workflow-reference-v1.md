# WORKFLOW.md Reference v1

## Status

Frozen.

## Authority

`starter-WORKFLOW.md` is the only maintained default template. PRD documents may summarize it but must not copy a divergent full template.

## Format

```markdown
---
# YAML front matter
---
Markdown prompt body
```

If no front matter exists, the whole file is prompt body. Empty prompt body blocks dispatch.

## Config field interpolation

Config fields do **not** support `{{ ... }}` interpolation. Only prompt body supports Liquid-style variables.

Supported explicit environment variable resolution:

```yaml
workspace:
  root: "$SYMPHONY_WORKSPACE_ROOT"
```

Only full-string `$VAR_NAME` is expanded.

## Supported sections

```yaml
tracker: {}
polling: {}
workspace: {}
hooks: {}
git: {}
agent: {}
codex: {}
approvals: {}
tools: {}
security: {}
observability: {}
server: {}
ui: {}
prompt: {}
```

Unknown keys warn. Wrong type, invalid enum, or missing required field errors block dispatch.

Local v1 intentionally extends the upstream Symphony workflow schema. Core upstream keys remain compatible where applicable; Local v1 extension keys are documented here and must not be silently interpreted as Linear-specific settings.


## Compatibility aliases

Local v1 prefers these local field names:

```yaml
agent:
  max_turns_per_run: 2
```

For compatibility with upstream Symphony-style config, the loader may accept:

```yaml
agent:
  max_turns: 2
```

If both are present, `max_turns_per_run` wins and the loader emits a warning. This alias does not change Local v1 continuation semantics: one main turn plus at most `max_handoff_continuations` handoff continuation turns.

Hard v1 constraints:

```text
agent.handoff_required = true
agent.pause_on_missing_handoff = true
agent.handoff_state = Human Review
agent.max_handoff_continuations ∈ {0, 1}
agent.max_turns_per_run = 1 + agent.max_handoff_continuations
```

Violating these constraints is a workflow validation error, not a warning.

## Local default overrides

Local v1 intentionally uses these defaults instead of upstream SPEC defaults where they differ:

```text
tracker.kind = local
hooks.timeout_ms = 300000
agent.max_turns_per_run = 2
workspace.root = <global-workspace-root>/<project_id>
git.base_ref = auto
failure behavior = dispatch pause; no automatic retry queue/timers
handoff target state = Human Review only
workspace cleanup = retained in v1; no terminal cleanup
protected_paths = full IS-009 default list
```

## Prompt variables

Root:

```text
issue
attempt
project
workspace
git
run
tools
previous_runs
```

Examples:

```liquid
{{ issue.identifier }}
{{ issue.title }}
{{ issue.acceptance_criteria | bullet_list }}
{{ git.branch_name }}
{{ issue.branch_name }}
```

## Filters

```text
default
join
json
bullet_list
indent
truncate
slug
short_hash
markdown_quote
```

Unknown variables and filters fail rendering. `issue.branch_name` and `git.branch_name` are both required aliases when workspace metadata exists.

## Reload semantics

```text
running attempts keep the workflow snapshot captured at dispatch time
new attempts use the latest valid workflow snapshot
invalid reload preserves the last valid effective config
if no valid config exists, dispatch is blocked while UI/diagnostics remain available
```

## Default starter

See `starter-WORKFLOW.md` in this directory. It is the only default template authority; PRD documents must not duplicate a divergent template.
