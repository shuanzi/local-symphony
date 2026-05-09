# UI、API、SSE、日志与可观测性

## 1. UI 边界

UI 是 control surface，不是 correctness layer。

UI 不参与：

```text
orchestrator 正确性
issue claim
agent run lifecycle
workspace cleanup
Codex protocol processing
DB 直接写入
```

UI 只能通过 REST/SSE 与 daemon 交互。

## 2. v1 主页面

| 页面 | 目的 |
|---|---|
| Overview | 系统总览、运行中 agent、失败、审批、workflow health |
| Board | 本地 issue board，按状态列展示 |
| Issue Detail | issue 内容、依赖、评论、状态历史、runs、artifacts |
| Run Detail | run timeline、logs、commands、approvals、tool calls |
| Approval Inbox | 待人工审批的命令、文件改动、网络访问 |
| Review Packet | Human Review 阶段 diff、测试、风险、handoff |
| Workflow | `WORKFLOW.md` validation、effective config、reload history |
| Diagnostics | daemon health、Codex auth/version、Git、DB、storage、export |

v1 不单独做 Workspace/Git 页面，但可以在 Issue Detail / Review 页面展示基础 Git 信息。完整 Workspace/Git 页面后续增强。

## 3. Overview

展示：

```text
system health
daemon status
workflow validation status
Codex availability
Git repo status
running agents
paused failed issues
manual resume candidates
failed runs
pending approvals
recent events
```

## 4. Board

列：

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

操作：

```text
创建 issue
编辑 issue
移动状态
添加 blocker
手动 dispatch
打开 run
打开 review packet
```

状态变化必须走 command API。

## 5. Run Detail

展示 timeline：

```text
run_created
workspace_prepared
prompt_rendered
codex_started
thread_started
turn_started
command_requested
approval_requested
approval_decided
tool_called
handoff_submitted
review_packet_generated
turn_completed
run_completed
```

UI 展示归一化 event，不直接把 raw Codex protocol log 当主 UI。

## 6. Approval Inbox

分类：

```text
Command approval
File change approval
Network approval
```

操作：

```text
Approve once
Approve for run
Deny
Cancel run
Open run detail
```

v1 默认安全策略：

```text
pending approval 不允许无限期挂起
超时后 fail 或 pause run
所有审批写 run event / lightweight event record
```

## 7. Review Packet 页面

展示：

```text
handoff summary
tests run
risks
verification steps
changed files
diff
commands
approval decisions
tool calls
prompt snapshot hash
Send to Rework
Mark Done
```

规则：

```text
没有 review packet，不允许 Done
没有 handoff 时不进入 Human Review；v1 默认 completed_without_handoff 并暂停 dispatch。partial review packet 仅用于诊断，不满足 Done 条件
Mark Done 默认只能由 operator 触发
```

## 8. Workflow 页面

展示：

```text
Raw WORKFLOW.md
Parsed config
Effective config
last loaded at
last valid workflow sha
validation errors
rendered prompt preview for selected issue
```

invalid workflow：

```text
阻止新 dispatch
继续使用 last known good config
UI 明确显示错误
```

## 9. Diagnostics 页面

展示：

```text
daemon version / pid / uptime
project path
app db path
project db path
Codex command / version / startup check
Git repo root / remote / base ref
storage size
workflow validation
redacted diagnostic export
```

## 10. API

v1 REST prefix：

```text
/api/v1
```

API 分为：

```text
Query API：读取状态
Command API：有副作用动作
```

Command API 需要 session token + CSRF。

## 11. SSE

默认 endpoint：

```http
GET /api/v1/events/stream
GET /api/v1/runs/:id/events/stream
GET /api/v1/issues/:id/events/stream
```

`GET /api/v1/events` 是 JSON query endpoint，不是 SSE endpoint。

事件格式：

```json
{
  "id": "evt_00000123",
  "type": "run.approval_requested",
  "severity": "warning",
  "project_id": "proj_abc",
  "issue_id": "loc_123",
  "issue_identifier": "LOC-123",
  "run_id": "run_001",
  "summary": "Command requires approval: npm install",
  "created_at": "2026-05-08T10:00:00+08:00",
  "data": {}
}
```

规则：

```text
事件必须有递增 event_id
前端重连时带 Last-Event-ID
SSE 只传归一化事件，不传大块 raw log
```

## 12. 日志

v1 日志分层：

```text
structured app log
run event log
raw Codex protocol log reference
review packet artifacts
```

默认 redaction。

v1 不做完整 compliance-grade audit log。
