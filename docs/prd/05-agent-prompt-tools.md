# Agent、Prompt、Tool Gateway 与 Handoff

> **Implementation warning:** This PRD file is product background only. Do not implement API, DB schema, CLI, state-machine, security, or test contracts from this file. Start from `../AGENT_IMPLEMENTATION_GUIDE.md`; executable contracts are `../../api/openapi.yaml` and `../../db/schema/*.sql`.


> 产品背景说明：本文件仅保留产品上下文。若与 `docs/implementation/`、`docs/schema/`、`docs/config/` 或 `docs/api/` 冲突，后者为准；不要从本 PRD 复制实现级 enum、schema、命令策略或模板。


## 1. Agent runtime

v1 只支持 Codex app-server。

不做：

```text
Claude Code adapter
Gemini CLI adapter
通用 MCP-only runtime
browser agent runtime
multi-agent swarm
```

## 2. Codex 会话生命周期

默认：

```text
每个 run attempt 启动一个独立 codex app-server 子进程
该 run 内复用同一个 live thread
run 结束后关闭子进程
```

流程：

```text
Start codex app-server subprocess
Initialize app-server client
Start thread
Start turn with rendered prompt
Stream events until completed / failed / cancelled / timeout
If handoff missing, optionally start bounded continuation turn
Generate review packet
Stop subprocess
Release orchestrator claim
```

## 3. Turn 策略

默认：

```yaml
agent:
  max_turns_per_run: 2
  max_handoff_continuations: 1
  turn_timeout_ms: 3600000
  stall_timeout_ms: 300000
```

规则：

```text
每个 run 默认一个主 turn
如果 turn 成功结束但没有 handoff，则最多追加一个 continuation turn
continuation 后仍无 handoff，则 run = completed_without_handoff
issue 不进入 Human Review
```

## 4. Prompt 架构

最终 prompt 由三部分组成：

```text
[1] Symphony Runtime Envelope
[2] Rendered WORKFLOW.md Prompt
[3] Context Pack
```

### 4.1 Runtime Envelope

由系统生成，不允许项目覆盖。

包含：

```text
当前 issue
当前 workspace
当前 branch / base
禁止 push / PR / Done
必须使用 symphony tool handoff
安全与审批边界
```

### 4.2 WORKFLOW Prompt

来自 repo-owned `WORKFLOW.md` markdown body。

可由项目定义：

```text
代码风格
测试策略
实现偏好
交接要求
项目约定
```

### 4.3 Context Pack

包含：

```text
issue JSON
acceptance criteria
blockers / labels / priority
git metadata
previous run summary
available tools
```

## 5. Prompt variables

默认变量：

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

严格模式：

```text
unknown variable → template_render_error
unknown filter → template_render_error
invalid interpolation → template_render_error
```

## 6. Prompt snapshot

每次 run 保存 redacted prompt snapshot。

路径：

```text
<repo>/.symphony/artifacts/<issue>/run_<run_id>/prompt/
├── context.json
├── rendered_prompt.redacted.md
├── prompt_meta.json
└── tool_manifest.md
```

保存：

```text
workflow_file_sha
workflow_config_hash
rendered_prompt_hash
context_pack_json
runtime_envelope_version
tool_manifest_version
redaction_policy_version
```

## 7. Tool Gateway

v1 主通道：

```text
symphony tool CLI + local IPC + run-scoped token
```

不把 Codex dynamic tools / MCP 作为 v1 核心路径。

## 8. Agent 可用工具

```bash
symphony tool issue get
symphony tool issue comment --body "..."
symphony tool artifact attach <path> --type test-output
symphony tool handoff --json ./handoff.json
```

所有命令默认输出 JSON。

成功：

```json
{
  "success": true,
  "tool": "handoff",
  "issue_identifier": "LOC-123",
  "handoff_status": "received",
  "handoff_id": "handoff_123"
}
```

`handoff_status=received` 只表示 tool gateway 已接受 handoff。issue 只有在 run finalizer 成功生成 review packet 后才进入 `Human Review`。

失败：

```json
{
  "success": false,
  "error_code": "permission_denied",
  "message": "This run cannot transition terminal states."
}
```

## 9. Run-scoped token

每个 run 生成短期 capability token。

scope：

```text
project_id + issue_id + run_id
```

允许：

```text
read current issue
comment current issue
attach current run artifact
submit handoff for current run
create follow-up issue
set Blocked with reason
```

禁止：

```text
mark Done
delete issue
modify unrelated issue
modify project settings
git push / PR
workspace delete
```

## 10. Handoff

handoff 是两阶段流程。`symphony tool handoff` 本身是原子化的 submit 工具，但它不直接把 issue 移动到 `Human Review`。Human Review transition 由 run finalizer 在 review packet 成功生成后完成。

命令：

```bash
symphony tool handoff --json ./handoff.json
```

`handoff.json`：

```json
{
  "summary": "Implemented local tracker CRUD and state transitions.",
  "changed_files": [
    "internal/tracker/local.go",
    "internal/tracker/schema.sql"
  ],
  "tests": [
    {
      "command": "go test ./internal/tracker/...",
      "result": "passed"
    }
  ],
  "risks": [
    "Migration rollback is not implemented in v1."
  ],
  "verification": [
    "Create issue",
    "Move to Ready",
    "Dispatch run",
    "Check review packet and Human Review transition"
  ],
  "followups": [
    {
      "title": "Add migration rollback tests",
      "priority": 3
    }
  ]
}
```

Tool submit 原子流程：

```text
Validate token scope
Validate issue belongs to run
Validate handoff payload
Store handoff payload
Attach declared artifacts, if any
Add issue comment with handoff summary
Record tool_call row
Record handoff row
Emit handoff.submitted run event
Return JSON result with handoff_status=received
```

Run finalizer 流程：

```text
Read latest handoff for run
Generate review packet
Insert review_packet.status = generated
Set run.status = completed
Transition issue → Human Review
Clear dispatch pause
Emit review.packet_generated run event
```

如果 review packet 生成失败：

```text
run.status = failed
issue 不进入 Human Review
issue.dispatch_paused = true
failure_code = review_packet_failed
```
