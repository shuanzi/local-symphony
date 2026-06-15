# Codex Fixture Policy

## Rule

A Codex protocol version is supported only when committed fixtures exist under:

```text
internal/agent/codex/testdata/schema/<codex-version>/
internal/agent/codex/testdata/transcripts/<codex-version>/
```

## Required fixture contents

```text
schema.json or generated TypeScript types
schema/<codex-version>/compatibility.json
happy-path transcript
missing-handoff transcript
protocol-error transcript
command approval transcript
file-change approval transcript
network approval transcript
static compatibility metadata when generated protocol/schema version differs from installed Codex version
```

阶段 A 的 prelaunch gate 至少校验并消费 `happy-path.jsonl`。为真实 Codex version 宣布生产可用前，必须随着对应能力落地补齐上面列出的场景 transcript；未提交 fixture 的版本仍必须 fail-closed。

## Compatibility metadata

每个 supported Codex version 必须提交：

```text
internal/agent/codex/testdata/schema/<codex-version>/compatibility.json
```

`compatibility.json` 是 prelaunch gate 的静态事实来源，字段如下：

```json
{
  "codex_version": "0.0.0-test",
  "protocol_version": "protocol-test-v1",
  "schema_version": "schema-test-v1",
  "supported_notifications": ["handshake", "thread_started", "turn_started", "turn_progress", "handoff", "approval_requested", "approval_resolved", "tool_call", "turn_completed", "turn_failed"],
  "supported_requests": ["initialize", "thread.start", "turn.start"],
  "experimental_api": false
}
```

- `codex_version` 必须与 `codex --version` 解析出的 version 完全一致。
- `protocol_version` 和 `schema_version` 是 committed schema/transcript 对应的协议版本，不允许从真实 `codex app-server` handshake 动态发现。
- `supported_notifications` 和 `supported_requests` 声明 adapter 已经用 fixture 覆盖的 protocol message/request。
- `experimental_api=false` 时，任何请求 experimental API 的真实 Codex dispatch 都必须在启动 app-server 前以 `unsupported_codex_version` 失败。

## Fixture layout

测试用 fixture 版本使用 `0.0.0-test`，目录布局如下：

```text
internal/agent/codex/testdata/
  schema/
    0.0.0-test/
      compatibility.json
      schema.json
  transcripts/
    0.0.0-test/
      happy-path.jsonl
```

新增真实 Codex version 时必须新增对应的 `schema/<codex-version>/` 与 `transcripts/<codex-version>/`，并提交完整 transcript 覆盖当前 policy 中列出的场景。

## Dispatch behavior

```text
1. Detect installed Codex version without launching the long-lived real `codex app-server` process.
2. Resolve committed fixture metadata/static compatibility metadata for that installed Codex version.
3. Read expected generated protocol/schema version from that metadata.
4. Resolve fixture directory for the installed Codex version plus generated protocol/schema version.
5. If fixture or compatibility metadata is missing, record a failed run attempt with `unsupported_codex_version`, restore the issue to its `source_issue_state`, and pause dispatch.
6. This failure must occur before launching the real `codex app-server` process.
7. If fixture exists, run adapter compatibility checks against committed metadata.
8. Only then launch `codex app-server`.
```

The prelaunch gate does not discover generated protocol/schema version from a real app-server handshake. That version comes from committed fixture metadata or static compatibility metadata. If the post-launch initialize handshake later contradicts the selected metadata or exposes a schema mismatch, fail the run with `codex_protocol_error`.

## CI

Default CI uses fake runner only. Real Codex tests run only when explicitly enabled:

```bash
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
```

**阶段 D 收口状态（2026-06-09）**：D3 / R14 preflight summary 与 fixture 消费一致；阶段 D 状态入口以当前 tree 内的 `docs/productization/D6_DOCS_CLOSE_NOTES.md` 为准。D5 / R13 release packaging 与本 fixture policy 互不干扰；当前 tree 不声明 release script 或 web lockfile 行为。web 端不消费 `internal/agent/codex` fixture，real Codex fixture 仍只在 `internal/agent/codex/testdata/` 下。
