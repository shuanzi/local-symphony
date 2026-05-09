# Implementation Spec 阶段入口建议

> 产品背景说明：本文件仅保留产品上下文。若与 `docs/implementation/`、`docs/schema/`、`docs/config/` 或 `docs/api/` 冲突，后者为准；不要从本 PRD 复制实现级 enum、schema、命令策略或模板。


## Status

Superseded as implementation contract.

Authoritative implementation file:

```text
docs/implementation/IS-001-repo-structure.md
```

本文档只保留阶段入口说明。代码结构、package 名称、schema 位置、OpenAPI 位置和 CLI 分组必须以 `IS-001`、`IS-002`、`IS-003`、`IS-004` 为准。

## 1. Repo structure

v1 使用单 binary：

```text
cmd/symphony/
```

不要生成独立 daemon binary。`symphony serve` 是同一个 `symphony` binary 的 daemon/server mode。

Implementation package layout must follow `IS-001`，包括但不限于：

```text
internal/app/
internal/config/
internal/db/
internal/tracker/
internal/orchestrator/
internal/workspace/
internal/gitx/
internal/agent/codex/
internal/toolgateway/
internal/review/
internal/httpapi/
internal/cli/
internal/security/
internal/observability/
internal/platform/
db/schema/
web/
```

## 2. Implementation order

Recommended implementation order:

```text
1. repo skeleton and CLI command groups
2. SQLite schema and migration/bootstrap loader
3. WORKFLOW.md parser and effective config
4. local tracker service and normalized issue DTO
5. API skeleton + session/auth + health/state
6. orchestrator actor + manual dispatch
7. workspace/git worktree manager
8. fake agent runner
9. Tool Gateway and run-scoped tokens
10. two-stage handoff + review packet finalizer
11. Codex adapter behind Runner interface
12. dashboard pages and SSE timelines
13. security policy engine and approval bridge
14. diagnostics/export and E2E acceptance tests
```

## 3. SQLite schema v1

Do not implement from this PRD. Use:

```text
docs/implementation/IS-002-sqlite-schema-v1.md
docs/schema/app-schema-v1.md
docs/schema/project-schema-v1.md
```

Important field names:

```text
run_attempts.attempt_no
review_packets.status = generated | partial | failed
issue_relations.relation_type = blocks | duplicates | followup_of
workspaces.status = planned | creating | ready | in_use | error | cleanup_pending | removed
```

## 4. OpenAPI v1 contract

Use:

```text
docs/implementation/IS-003-openapi-v1.md
docs/api/openapi-v1-outline.md
```

When implemented, `api/openapi.yaml` becomes the source for generated clients and handler conformance tests.

## 5. CLI command spec

Use:

```text
docs/implementation/IS-004-cli-tool-gateway.md
```

Single binary command groups:

```text
symphony init
symphony serve
symphony open
symphony status
symphony issue ...
symphony run ...
symphony approval ...
symphony review ...
symphony workflow ...
symphony diagnostics ...
symphony tool ...
```

## 6. Handoff implementation reminder

Do not implement a direct handoff transition.

Correct sequence:

```text
symphony tool handoff
  -> validates run-scoped token
  -> records handoff/tool_call/comment/event
  -> returns handoff_status=received
run finalizer
  -> generates review packet
  -> inserts review_packet.status=generated
  -> sets run.status=completed
  -> transitions issue to Human Review
```

## 7. Retry implementation reminder

Do not implement automatic retry timers in v1.

Correct behavior:

```text
failure -> issues.dispatch_paused = true
operator dispatch-resume clears the pause
next dispatch may use dispatch_reason=retry or rework depending on command/context
```


## Implementation package hygiene

When handing these documents to an implementation agent, the package should contain only source documents and intended project files.

Exclude:

```text
.git/
*.patch
stale diffs
uncommitted local scratch files
```

If Git history is intentionally included, hand off a clean commit/tag and state that current HEAD is the only implementation source.
