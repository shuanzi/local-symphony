# 本地 Tracker、Issue 状态机与调度规则

> 产品背景说明：本文件仅保留产品上下文。若与 `docs/implementation/`、`docs/schema/`、`docs/config/` 或 `docs/api/` 冲突，后者为准；不要从本 PRD 复制实现级 enum、schema、命令策略或模板。


## 1. Tracker source of truth

v1 使用 SQLite 作为本地 tracker 的唯一 source of truth。

Markdown / JSON 只用于：

```text
导入
导出
人工审查
备份包中的可读副本
```

不使用 Linear，不模拟 Linear API。

## 2. Issue 状态机

完整状态集：

```text
Inbox
Ready
Working
Rework
Blocked
Human Review
Done
Cancelled
Duplicate
```

active states：

```text
Ready
Working
Rework
```

terminal states：

```text
Done
Cancelled
Duplicate
```

主路径：

```text
Inbox → Ready → Working → Human Review → Done
                              │
                              └── Rework → Working
```

## 3. 状态语义

| 状态 | 可 dispatch | 说明 |
|---|---:|---|
| Inbox | 否 | 捕获想法，尚未准备好 |
| Ready | 是 | 可被 agent 执行 |
| Working | 是 | 已被 agent 或人工领取，仍可继续执行 |
| Rework | 是 | 人审退回，agent 可继续修 |
| Blocked | 否 | 依赖或外部条件阻塞 |
| Human Review | 否 | agent 已交付，等待人审 |
| Done | 否 | 完成 |
| Cancelled | 否 | 取消 |
| Duplicate | 否 | 重复 |

## 4. Issue 字段与 NormalizedIssue

v1 issue 表本身只保存 tracker 核心字段。API、prompt、UI 和 orchestrator 使用 normalized issue DTO。

DTO 至少包含：

```text
id
identifier
title
description
acceptance_criteria
priority
state
labels
blocked_by
blocks
dispatch_paused
dispatch_pause_reason
workspace.branch_name
workspace.path
workspace.base_ref
workspace.base_sha
latest_run
latest_review_packet
created_at
updated_at
```

`branch_name`、`workspace_path`、`base_ref`、`base_sha` 来自 `workspaces` 表，不重复存入 `issues` 表。具体 DTO 见 `docs/schema/normalized-issue-v1.md`.

## 5. Blocker 规则

issue 支持 blocker 关系：

```text
source_issue_id blocks target_issue_id
```

API/DTO 可以展示：

```text
blocked_by: [issue_id...]  # 从 blocks 关系反向推导
blocks:     [issue_id...]  # 从 blocks 关系正向查询
```

调度规则：

```text
只要 issue 存在未 terminal 的 blocker，就不能 dispatch。
```

## 6. Dispatch eligibility

issue 满足以下条件才可 dispatch：

```text
issue.state in active_states
issue.state not in terminal_states
not already running
not already claimed
no unfinished blockers
global concurrency slot available
workspace path valid
workflow config valid
Codex available
```

## 7. 排序规则

默认排序：

```text
priority ascending
created_at oldest
identifier ascending
```

## 8. Agent 写 issue 的方式

agent 不直接访问 SQLite。

agent 通过：

```text
symphony tool ...
```

经由本地 IPC / run-scoped token 修改当前 issue。

允许：

```text
read current issue
add comment
attach artifact
submit handoff for review-packet finalizer
create follow-up issue
set Blocked with reason
```

`issue.block` 只能把当前 issue 标为 `Blocked` 并写入 reason/comment。创建 blocker relation 必须走 operator blocker command，或由 `followup.create` 在同一 run 内创建 follow-up 后按明确定义的 relation 规则写入。

禁止：

```text
delete issue
modify unrelated issue
mark Done
modify project settings
push / PR / merge
create arbitrary blocker relations
```

`issue.block` 只表示 agent 将当前 issue 标记为 Blocked 并写入原因，不代表 agent 可以随意创建 blocker relation。Blocker relation 由 operator 通过 issue blocker API 管理。
