# v1 数据模型概念说明

> 产品背景说明：本文件仅保留产品上下文。若与 `docs/implementation/`、`docs/schema/`、`docs/config/` 或 `docs/api/` 冲突，后者为准；不要从本 PRD 复制实现级 enum、schema、命令策略或模板。


## 状态

本文档已被 implementation/schema 文档取代，不再作为字段级实现合同。

权威文档：

```text
docs/implementation/IS-002-sqlite-schema-v1.md
docs/schema/project-schema-v1.md
docs/schema/app-schema-v1.md
docs/schema/normalized-issue-v1.md
```

实现 agent 不应从本文档复制字段、enum 或 SQL。本文档只保留产品层概念。

## 概念域

v1 数据模型覆盖：

```text
project metadata
local issues
labels and comments
issue relations
issue state history
workspaces
run attempts
run events
approvals
run-scoped tool tokens
tool calls
handoffs
artifacts
review packets
workflow snapshots
prompt snapshots
settings / schema version
```

## 关键约束

```text
SQLite project DB 是 local tracker 的 source of truth。
issue 上不重复存储 workspace path、branch、base ref、base sha；这些由 workspaces 表提供。
blocker relation 在 DB 中只存 blocks 单向关系；blocked_by 是查询/DTO 投影。
run_attempts 使用 attempt_no；旧草案中的 attempt-number 命名不得采用。
review_packets.status 使用 generated / partial / failed。
workspaces.status 使用 planned / creating / ready / in_use / error / cleanup_pending / removed。
run_attempts.status 使用 pending / preparing_workspace / rendering_prompt / starting_agent / running / completed / completed_without_handoff / failed / cancelled。
```

## Normalized issue

API、orchestrator、prompt 和 UI 使用 normalized issue DTO。字段和组装规则见：

```text
docs/schema/normalized-issue-v1.md
```

## Review packet gate

`Human Review` 和 `Done` 的关键约束：

```text
handoff.submit 只持久化 handoff 数据。
review packet finalizer 生成 status=generated 的 review packet 后，issue 才能进入 Human Review。
Mark Done 要求 issue.state = Human Review 且 latest review_packet.status = generated。
```
