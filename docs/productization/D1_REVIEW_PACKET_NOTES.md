# D1 / R10 — Review Packet API 与 dashboard 可审查性

## 范围

- worktree 分支：`codex/v1-productization-d1-review-packet`
- 基线提交：`f15ba39` (main, v1.1 WIP 收口)
- 实施提交：`2cc1888` — *D1: Review Packet API returns structured projection + raw refusal*
- 改动面（11 个文件，+886 / −39）：
  - `internal/review/review.go` + `internal/review/review_test.go`
  - `internal/httpapi/httpapi.go` + `internal/httpapi/httpapi_test.go`
  - `internal/store/store.go`
  - `internal/cli/rest_handlers.go` + `internal/cli/cli_test.go`
  - `schemas/review_packet.schema.json`
  - `api/openapi.yaml`
  - `web/src/types.ts` + `web/src/App.tsx`

## 实施内容

### 1. Review Packet 结构化投影

- `internal/review/review.go` 中 `Generate` 现在把以下字段写进 `review.json` 顶层：
  - `summary`、`acceptance_criteria`
  - `handoff`（保留既有对象）
  - `changed_files`、`diff`、`tests`、`risks`、`verification`
  - `approvals`、`tool_calls`（从 `approval_requests` / `tool_calls` 拉取，运行在同一 transaction 中以避免 lock）
  - `git`、`how_to_continue`
  - `raw_prompt_exposed / raw_codex_log_exposed / raw_secret_exposed`（始终为 `false`，作为契约 marker）
- 新增 `reviewSidecarEntries(tx, handoffID)` 工具函数：从 `handoffs` 表找到 `run_id`，再查 `approval_requests` 与 `tool_calls`，**只返回 id / status / hash / 时间戳等元数据**，从不暴露 input / output 原始 payload。
- `internal/store/store.go` 新增 `ApprovalsForRun(runID)`、`ToolCallsForRun(runID)` 与 `ReviewPacketProjection(issueRef)`，后者把 row metadata 与 `review.json` 合并成与 HTTP 投影同形的结构化 map。`safeContainedPath` 提供轻量级路径沙箱校验（相对路径 + 必须在 `.symphony/artifacts` 或 `.symphony/exports` 下）。

### 2. HTTP 投影 & 拒绝语义

- `internal/httpapi/httpapi.go` 中 `/api/v1/reviews/{issue_ref}` 现在调用 `s.reviewStructuredProjection(row, files)`：
  - 总是返回 `summary / acceptance_criteria / handoff / changed_files / diff / tests / risks / verification / approvals / tool_calls / git / how_to_continue` 这些字段（默认空值或默认值）。
  - `review.json` 缺失或不可解析时 fallback 到 metadata-only 投影，**不**回退到读 DB 里的 raw 字段。
  - `review.json` 路径必须 containment 在 `.symphony/artifacts` 或 `.symphony/exports` 根下（`safeContainedFilePathAllowMissing`）。
- 抽离 `isRawArtifactRefusalKind` 处理 `codex_log / codex_events / prompt_snapshot / prompt_rendered / prompt_context / secret_artifact / secrets`。
- `/api/v1/artifacts/{id}/content` 在以上任何一种 kind 上都返回 403 + `raw_log_access_not_supported`（**绝不** `http.ServeFile`）。
- `ReviewPacketArtifact` 中的 `content_url` 在 raw kind 上为 `null`（已包含 `raw_prompt_exposed / raw_codex_log_exposed / raw_secret_exposed` 标志位）。
- `internal/cli/rest_handlers.go` `reviewGetLocal` 切换到 `st.ReviewPacketProjection(ref)`，offline CLI `symphony review LOC-1` 与 daemon-backed 路径返回相同结构化字段。

### 3. Dashboard

- `web/src/types.ts`：`ReviewPacketSummary` 新增结构化字段；新增 `ReviewPacketHandoff` 子类型。
- `web/src/App.tsx` `ReviewPacketPage`：
  - 新增 *Structured review packet* Section：summary、acceptance_criteria、handoff target、tests/risks/verification（ListBlock）、changed_files、approvals/tool_calls 计数、how_to_continue；并显示 *Raw prompt / Raw Codex log / Raw secret exposed* 三个 Pill。
  - Diff 体用 `<details>` 折叠展示，避免默认污染页面。
  - 新增 `ListBlock` 辅助组件。
  - 既有 Artifacts & redaction boundary 区不变，content_url=null 的 raw artifact 仍走 refusal box。

### 4. Schema / OpenAPI 同步

- `schemas/review_packet.schema.json`：
  - `required` 增加 `summary / acceptance_criteria / diff / tests / risks / verification / how_to_continue / raw_prompt_exposed / raw_codex_log_exposed / raw_secret_exposed`。
  - `properties` 增加对应 schema（`diff: string`、`tests/risks/verification: string[]`、`how_to_continue: string`、三个 `raw_*: boolean`）。
- `api/openapi.yaml`：
  - `ReviewPacketSummary` 的 `required` 加上所有结构化字段。
  - 新增 `ReviewPacketHandoff` 子 schema；`/artifacts/{id}/content` 描述明确 403 / `raw_log_access_not_supported` 触发条件。
  - `Artifact.kind` 枚举扩展为 `prompt_snapshot / prompt_rendered / prompt_context / prompt_meta / prompt_tool_manifest / codex_events / secret_artifact / secrets` 等。

### 5. CLI `review path` 严格 metadata

- `internal/cli/cli_test.go` 新增 `TestReviewPathSurfacesOnlyMetadataAndPathDiagnostics`：
  - 写一个 sentinel raw secret 文件到 `root` 目录
  - 调 `symphony review path LOC-1 --project DIR`
  - 断言：exit 0；stdout **不**含 sentinel；stdout **不**含 `"diff":` / `"summary":`（避免 inline 投影）；stdout 含 `"status":` / `"root_path":`。
  - 旧路径 `symphony review LOC-1`（非 `path`）走 `ReviewPacketProjection` 完整结构化输出。

### 6. Review packet failure 不进入 Human Review

- 在 `internal/review/review_test.go` 中加 `TestReviewFailureDoesNotTransitionIssueToHumanReview`：
  - 制造 `review.md` 目录冲突，使 `Generate` 失败。
  - 模拟 orchestrator 走 `FailRun(runID, FailureReviewPacketFailed, ..., RunFailed)` 路径（与 `internal/orchestrator/orchestrator.go:262` 一致，**不**调 `CompleteRunWithReview`）。
  - 断言：`issue.State != HumanReview`，`issue.DispatchPaused == true`，run 上没有 generated review packet。

## 验证命令与结果

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/review/ -count=1` | ok 5.85s |
| `go test ./internal/httpapi/ -count=1` | ok 9.39s |
| `go test ./internal/store/ -count=1` | ok 2.97s |
| `go test ./internal/cli/ -count=1` | ok 2.16s |
| `go test -p 1 ./internal/...` | ok all internal packages serial pass |
| `python3 scripts/validate_contracts.py` | contract validation passed |
| `bash scripts/acceptance-local.sh` | acceptance-local passed |
| `cd web && npm install --no-save && npx tsc --noEmit && npm test` | web tests passed |

新加的窄范围失败 → 修复后通过的测试：

- `TestGenerateWritesStructuredFieldsToReviewJSONArtifact` (review)
- `TestReviewFailureDoesNotTransitionIssueToHumanReview` (review)
- `TestReviewPacketReturnsStructuredFieldsFromReviewJSON` (httpapi)
- `TestReviewPacketArtifactsRedactRawPromptAndCodexLog` (httpapi)
- `TestReviewPathSurfacesOnlyMetadataAndPathDiagnostics` (cli)

## 已知限制

- Dashboard TypeScript 类型用 `additionalProperties: true` 兼容 server 在失败状态下少字段；服务端 fallback 已保证必填字段存在。
- `ToolCallsForRun` 返回的 `input_hash` / `output_hash` 是 raw payload 的 SHA256，已经在 `tool_calls` 表里持久化（来自 C3 阶段），**不**等于原始 prompt / Codex 输出内容；dashboard 渲染时只展示 hash。
- `reviewStructuredProjection` 仍然在缺失 `review.json` 时返回默认值；这意味着人工污染 `review.json` 后若文件不可解析，dashboard 会显示空 handoff 与空 how_to_continue（仍不会泄露 raw 内容）。
- 本次未引入 `symphony review path --json` 形态，CLI 仍走 `Main` → `printResult` → `ok(...)`，shape 与 v1.1 之前保持一致；openapi/contract 校验未新增 CLI 形态校验。
- OpenAPI `ReviewPacketSummary` 加 `additionalProperties: true` 以兼容老 daemon（v1.1 WIP 之前的 C3 行为），不破坏 v1.1 客户端。

## v1.1 WIP 收口决策（D1 / R10）

D1 / R10 在 v1 阶段经历了 codex review 2 轮：

- **Round 1**（在 commit `2cc1888` 上跑）落 3 finding（0 P1 + 2 P2 + 1 P3），已在 commit `0aee74c` 修毕：per-artifact `raw_*_exposed` schema 声明、local store `safeContainedPath` 补 `EvalSymlinks`、OpenAPI `ReviewPacketSummary` 加 `git` 字段。
- **Round 2**（在 commit `0aee74c` 上跑）落 3 finding（1 P1 + 2 P2 regression，全部为 R1 修复引入的边角 case），已在 commit `2709a94` 修毕：file schema 顶层 `required` 不再含 `artifacts`（该字段由 HTTP /reviews 路径从 DB 派生，不写进 review.json 文件本身）；`Generator.Generate` 只在 promptID 非空时写 `prompt_snapshot` 字段以满足 `^ps_` pattern；`ReviewPacketArtifact.kind.enum` 补齐为 `Artifact.kind` 的超集；daemon handler 的 `raw_secret_exposed` 谓词与 local store 对齐。

D1 / R10 在 v1 阶段不再开新 review（codex-review-d1-round2 agent 已关闭，无法再跑 R3）。v1.1 WIP 收口策略与 D3 / R14、D6 / R15 保持一致：

- 主要 contract（结构化 review packet、raw refusal、EvalSymlinks、git 字段、schema 一致性）全部已通过 schema validator + openapi 校验 + 单元测试保障。
- 任何后续 R3 残余的"边角 finding"应在 v1.1 WIP 阶段继续按需修复，**不**阻塞 v1 主线 ship。
- D1 / R10 的已知限制保留在本文档 "已知限制" 节，覆盖 schema `additionalProperties` 兼容、ToolCalls hash surface、`review.json` 缺省回退等非阻塞项。

## 提交列表

- `2cc1888` D1: Review Packet API returns structured projection + raw refusal
- `d0677a1` D1: notes for review packet structured projection work
- `0aee74c` D1 R1: fix codex review round 1 findings (0 P1 + 2 P2 + 1 P3)
- `2709a94` D1 R2: fix codex review round 2 findings (1 P1 + 2 P2)
