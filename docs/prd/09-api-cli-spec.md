# v1 REST API 与 CLI 规格概览

> 产品背景说明：本文件仅保留产品上下文。若与 `docs/implementation/`、`docs/schema/`、`docs/config/` 或 `docs/api/` 冲突，后者为准；不要从本 PRD 复制实现级 enum、schema、命令策略或模板。


## 状态

产品背景文档。具体 API 以以下文档为准：

```text
docs/implementation/IS-003-openapi-v1.md
docs/api/openapi-v1-outline.md
api/openapi.yaml（实现后）
```

具体 CLI 与 Tool Gateway 以以下文档为准：

```text
docs/implementation/IS-004-cli-tool-gateway.md
```

## REST API 原则

```text
API prefix: /api/v1
Query API 读取状态。
Command API 执行副作用动作。
Command API 需要 session token 和 CSRF protection。
UI 只通过 REST/SSE 与 daemon 交互。
```

## v1 必须 API 组

`{issue_ref}` 可接受内部 id（如 `iss_...`）或人类可读 identifier（如 `LOC-1`）；实现以 IS-003 为准。

```http
GET  /api/v1/health
GET  /api/v1/state
GET  /api/v1/events
GET  /api/v1/events/stream

POST /api/v1/auth/exchange
GET  /api/v1/auth/session
POST /api/v1/auth/logout

GET  /api/v1/issues
POST /api/v1/issues
GET  /api/v1/issues/{issue_ref}
POST /api/v1/issues/{issue_ref}/transition
POST /api/v1/issues/{issue_ref}/comments
GET  /api/v1/issues/{issue_ref}/blockers
POST /api/v1/issues/{issue_ref}/blockers
DELETE /api/v1/issues/{issue_ref}/blockers/{blocker_issue_ref}
POST /api/v1/issues/{issue_ref}/dispatch
POST /api/v1/issues/{issue_ref}/dispatch-pause
POST /api/v1/issues/{issue_ref}/dispatch-resume
GET  /api/v1/issues/{issue_ref}/events/stream

GET  /api/v1/runs
GET  /api/v1/runs/{run_id}
GET  /api/v1/runs/{run_id}/events
GET  /api/v1/runs/{run_id}/events/stream
POST /api/v1/runs/{run_id}/cancel

GET  /api/v1/approvals
POST /api/v1/approvals/{approval_id}/decide

GET  /api/v1/reviews/{issue_ref}
POST /api/v1/reviews/{issue_ref}/send-to-rework
POST /api/v1/reviews/{issue_ref}/mark-done

GET  /api/v1/artifacts/{artifact_id}
GET  /api/v1/artifacts/{artifact_id}/content

GET  /api/v1/workflow
POST /api/v1/workflow/reload

GET  /api/v1/diagnostics
POST /api/v1/diagnostics/export
```

## SSE 规则

```text
/api/v1/events 是 JSON query endpoint。
/api/v1/events/stream 是全局 SSE endpoint。
/api/v1/runs/{run_id}/events/stream 是 run-scoped SSE endpoint。
/api/v1/issues/{issue_ref}/events/stream 是 issue-scoped SSE endpoint。
```

SSE 只传 normalized event，不传大块 raw logs。

## 统一错误格式

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

## CLI 必须命令组

```bash
symphony init
symphony serve
symphony open
symphony status

symphony issue create
symphony issue list
symphony issue show LOC-123
symphony issue update LOC-123
symphony issue transition LOC-123 Ready
symphony issue comment LOC-123 --body "..."
symphony issue blocker add LOC-2 --blocked-by LOC-1
symphony issue blocker remove LOC-2 --blocked-by LOC-1
symphony issue dispatch LOC-123
symphony issue dispatch-pause LOC-123 --reason "..."
symphony issue dispatch-resume LOC-123 --reason "..."

symphony run LOC-123
symphony run list
symphony run show run_001
symphony run events run_001 --follow
symphony run cancel run_001 --reason "..."

symphony approval list
symphony approval decide appr_001 --approve-once
symphony approval decide appr_001 --deny --reason "..."

symphony review LOC-123
symphony review send-to-rework LOC-123 --reason "..."
symphony review mark-done LOC-123 --reason "..."
symphony review path LOC-123

symphony workflow validate
symphony workflow reload
symphony workflow show

symphony diagnostics
symphony diagnostics export
```

## Agent tool CLI

```bash
symphony tool issue get
symphony tool issue comment --body "..."
symphony tool issue block --reason "..."
symphony tool artifact attach <path> --type test-output
symphony tool followup create --json ./followup.json
symphony tool handoff --json ./handoff.json
```

所有 `symphony tool` 命令只输出 JSON。

成功示例：

```json
{
  "success": true,
  "tool": "handoff",
  "issue_identifier": "LOC-123",
  "handoff_status": "received",
  "next_step": "review_packet_finalizer"
}
```

`handoff_status=received` 不代表 issue 已进入 `Human Review`。`Human Review` 只由 run finalizer 在 review packet 生成成功后设置。

## v1 暂不实现

```http
POST /api/v1/git/{issue_ref}/push
POST /api/v1/git/{issue_ref}/create-pr
POST /api/v1/workspaces/{issue_ref}/delete
POST /api/v1/workspaces/{issue_ref}/snapshot
POST /api/v1/db/backup
POST /api/v1/db/migrate
POST /api/v1/audit/export
```

```bash
symphony db backup
symphony db migrate
symphony db restore
symphony audit export
symphony pr create
```
