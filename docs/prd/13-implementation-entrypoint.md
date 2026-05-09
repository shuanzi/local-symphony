# Implementation Spec 阶段入口建议

最终方案冻结后，下一阶段应进入 Implementation Spec。建议按以下顺序展开。

## 1. Repo structure

建议代码结构：

```text
local-symphony/
├── cmd/
│   ├── symphony/
│   └── symphonyd/
├── internal/
│   ├── app/
│   ├── config/
│   ├── db/
│   ├── tracker/
│   ├── orchestrator/
│   ├── workspace/
│   ├── git/
│   ├── agent/
│   │   └── codex/
│   ├── tools/
│   ├── approvals/
│   ├── review/
│   ├── observability/
│   ├── security/
│   └── api/
├── web/
├── migrations/
├── examples/
├── docs/
└── WORKFLOW.example.md
```

## 2. Go backend module layout

优先定义：

```text
config loader
DB interfaces
tracker service
orchestrator command queue
workspace service
Codex adapter interface
tool gateway service
review packet generator
```

## 3. SQLite schema v1

先实现最小表：

```text
schema_version
projects
issues
issue_comments
issue_relations
workspaces
run_attempts
run_events
approval_requests
tool_calls
handoffs
review_packets
workflow_snapshots
prompt_snapshots
settings
```

## 4. OpenAPI v1 contract

先定义：

```text
health/state
issues
runs
approvals
reviews
workflow
diagnostics
```

## 5. CLI command spec

先实现：

```text
symphony init
symphony serve
symphony issue create/list/show/transition/comment
symphony run/show/cancel
symphony tool issue get/comment/artifact/handoff
```

## 6. WORKFLOW.md parser spec

必须支持：

```text
YAML front matter
Markdown prompt body
strict variable checking
strict filter checking
last known good config
invalid workflow 阻止新 dispatch
```

## 7. Orchestrator state machine

先实现：

```text
manual dispatch
claim
workspace prepare
run start
run event recording
handoff transition
run cancel
```

自动 poll 可以在手动 dispatch 可用后增强。

## 8. Codex adapter protocol wrapper

先实现：

```text
process start
stdio JSONL read/write
thread/turn lifecycle
event normalization
approval request forwarding
turn completed / failed / timeout
stderr diagnostics
```

## 9. Tool Gateway contract

先实现：

```text
local IPC / loopback fallback
run-scoped token
tool auth middleware
JSON request/response
handoff atomic transaction
```

## 10. Review Packet generator

先实现：

```text
git diff
changed-files.txt
review.json
review.md
tool-calls.jsonl
commands.jsonl
approvals.jsonl
prompt snapshot reference
```

## 11. React dashboard page-level plan

实现顺序：

```text
Overview
Board
Issue Detail
Run Detail
Approval Inbox
Review Packet
Workflow
Diagnostics
```

## 12. 工程优先级

建议先打通最小纵向链路，而不是按模块横向做全功能：

```text
M0 skeleton
→ create issue
→ create worktree
→ start Codex
→ tool handoff
→ review packet
→ dashboard review
```

这条链路跑通后，再补齐审批、安全、诊断和失败路径。
