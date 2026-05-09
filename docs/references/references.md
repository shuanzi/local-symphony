# References

The implementation docs were derived from the frozen product discussion and these upstream references:

## OpenAI Symphony SPEC

```text
https://github.com/openai/symphony/blob/main/SPEC.md
```

Used for concepts such as:

```text
Workflow Loader
Issue tracker normalization
Orchestrator responsibilities
Workspace lifecycle
Agent runner lifecycle
Prompt rendering
Dynamic reload
Status surface / observability
Security posture expectations
```

## OpenAI Symphony repository

```text
https://github.com/openai/symphony
```

Used for context that the reference implementation is an engineering preview / prototype and production systems should implement hardened versions.

## Codex app-server documentation

```text
https://developers.openai.com/codex/app-server/
```

Used for:

```text
codex app-server
target app-server transport/framing
JSON-RPC style communication where supported by the selected Codex version
approvals
experimental WebSocket / dynamic tools caveats
```

## Codex approvals and security documentation

```text
https://developers.openai.com/codex/agent-approvals-security
```

Used for:

```text
workspace-write sandbox
network default deny
approval policies
protected paths
safety posture
```

## Git worktree documentation

```text
https://git-scm.com/docs/git-worktree
```

Used for:

```text
git worktree lifecycle
worktree add/remove/prune behavior
```

## SQLite Online Backup API

```text
https://sqlite.org/backup.html
```

Referenced for deferred v1.1 backup planning. v1 does not implement automatic backup.

## Tauri capabilities and sidecar references

```text
https://v2.tauri.app/security/capabilities/
https://v2.tauri.app/develop/sidecar/
```

Used for future v2 desktop packaging direction.
