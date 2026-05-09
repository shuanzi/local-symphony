# WORKFLOW.md Reference v1

## Status

Frozen.

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

Unknown variables and filters fail rendering.

## Default starter

See `starter-WORKFLOW.md` in this directory.
