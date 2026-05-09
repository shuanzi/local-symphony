# v1 REST API 与 CLI 规格

## 1. REST API 原则

v1 API 前缀：

```text
/api/v1
```

API 分为：

```text
Query API：读取状态
Command API：有副作用动作
```

Command API 需要：

```text
session token
CSRF protection
```

## 2. 必须 API

### Health / State

```http
GET /api/v1/health
GET /api/v1/state
GET /api/v1/events
```

### Issues

```http
GET  /api/v1/issues
POST /api/v1/issues
GET  /api/v1/issues/:id
POST /api/v1/issues/:id/transition
POST /api/v1/issues/:id/comments
POST /api/v1/issues/:id/dispatch
```

### Runs

```http
GET  /api/v1/runs
GET  /api/v1/runs/:id
GET  /api/v1/runs/:id/events
POST /api/v1/runs/:id/cancel
```

### Approvals

```http
GET  /api/v1/approvals
POST /api/v1/approvals/:id/decide
```

### Reviews

```http
GET  /api/v1/reviews/:issue_id
POST /api/v1/reviews/:issue_id/send-to-rework
POST /api/v1/reviews/:issue_id/mark-done
```

### Workflow

```http
GET  /api/v1/workflow
POST /api/v1/workflow/reload
```

### Diagnostics

```http
GET  /api/v1/diagnostics
POST /api/v1/diagnostics/export
```

## 3. v1 暂缓 API

```http
POST /api/v1/git/:issue_id/push
POST /api/v1/git/:issue_id/create-pr
POST /api/v1/workspaces/:issue_id/delete
POST /api/v1/workspaces/:issue_id/snapshot
POST /api/v1/db/backup
POST /api/v1/db/migrate
POST /api/v1/audit/export
```

## 4. 统一错误格式

```json
{
  "error": {
    "code": "workflow_validation_failed",
    "message": "WORKFLOW.md has invalid YAML front matter.",
    "details": {},
    "request_id": "req_123"
  }
}
```

## 5. SSE

```http
GET /api/v1/events
GET /api/v1/runs/:id/events/stream
GET /api/v1/issues/:id/events/stream
```

事件格式：

```json
{
  "id": "evt_000123",
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

## 6. CLI 必须命令

### App

```bash
symphony init
symphony serve
symphony open
symphony status
```

### Issue

```bash
symphony issue create
symphony issue list
symphony issue show LOC-123
symphony issue transition LOC-123 Ready
symphony issue comment LOC-123 --body "..."
```

### Run

```bash
symphony run LOC-123
symphony run list
symphony run show run_001
symphony run cancel run_001
```

### Review / Workflow / Diagnostics

```bash
symphony review LOC-123
symphony workflow validate
symphony diagnostics export
```

### Agent tool

```bash
symphony tool issue get
symphony tool issue comment
symphony tool artifact attach
symphony tool handoff
```

## 7. v1 暂不实现 CLI

```bash
symphony db backup
symphony db migrate
symphony db restore
symphony audit export
symphony pr create
```

## 8. Tool CLI 输出

所有 `symphony tool` 默认输出 JSON。

成功：

```json
{
  "success": true,
  "tool": "handoff",
  "issue_identifier": "LOC-123",
  "state": "Human Review"
}
```

失败：

```json
{
  "success": false,
  "error_code": "permission_denied",
  "message": "This run cannot transition terminal states."
}
```
