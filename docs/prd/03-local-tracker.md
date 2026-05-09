# 本地 Tracker、Issue 状态机与调度规则

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

## 4. Issue 字段

v1 issue 至少包含：

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
created_at
updated_at
branch_name
workspace_path
base_ref
base_sha
```

## 5. Blocker 规则

issue 支持：

```text
blocked_by: [issue_id...]
blocks:     [issue_id...]
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
handoff to Human Review
create follow-up issue
set Blocked with reason
```

禁止：

```text
delete issue
modify unrelated issue
mark Done
modify project settings
push / PR / merge
```
