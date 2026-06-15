# D4 / R16 — Rework prompt 上下文产品化

实施记录，覆盖 v1 REAL Productization 阶段 D 的 D4 / R16 工作包。

## 改动面

| 路径 | 用途 |
| --- | --- |
| `db/schema/v1_project.sql` | 新增 `rework_snapshots` 表；扩展 `run_attempts.dispatch_reason` CHECK 允许 `manual_rework` |
| `internal/db/schema.go` | 在 fallback `fallbackProjectSchema` 中镜像新增表 + 扩展 CHECK 约束 |
| `internal/review/safe_summary.go` | 新增 `SafeSummary` DTO、`BuildSafeSummaryFromRun/Issue`、`ToMarkdown`、`Seal`、`ScanForRawArtifacts`、`refusalKindBlocklist` |
| `internal/review/safe_summary_test.go` | 新增 — 9 个子测试覆盖 DTO 字段、扫描、rejection、JSON round-trip、refusal kind blocklist 锁定 |
| `internal/review/review.go` | 在 review packet 落盘后注入 `safe_summary` 子字段；review packet generator 优先 UPDATE 已有 prompt_snapshots 行以保留 D4 hash |
| `internal/agent/codex/rework_prompt.go` | 新增 `ReworkContextInput`、`BuildReworkPrompt`、`BuildReworkSnapshotRecord`；enforce refusal kind scan 与确定性 hash |
| `internal/agent/codex/rework_prompt_test.go` | 新增 — 6 个子测试覆盖 reason/safe summary 注入、rejection、deterministic hash |
| `internal/orchestrator/orchestrator.go` | rework dispatch 路径：`injectReworkContext` 注入 reason + safe summary；写 `prompt/rework_prompt.redacted.md`；stamp `rework_snapshots`；`computeCumulativeDiffSHA` |
| `internal/orchestrator/rework_prompt_test.go` | 新增 — 6 个子测试覆盖 D4 acceptance criteria |
| `internal/store/store.go` | 新增 `ReworkSnapshotRecord`、`CreateReworkSnapshot`、`GetReworkSnapshot`、`ListReworkSnapshotsForIssue`、`LatestCompletedRunForIssue`、`LatestReviewPacketIDForRun`、`LatestReviewReasonForIssue` |
| `schemas/review_packet.schema.json` | 新增 `safe_summary` 子 schema |
| `api/openapi.yaml` | 新增 `ReviewPacketSafeSummary`、`ReworkSnapshot` schemas |
| `docs/testing/CONTRACT_VALIDATION_MANIFEST.json` | 新增 D4 / R16 相关 security topic |

## 验收对照

| 验收项 | 证据 |
| --- | --- |
| 从 Human Review send-to-rework 后，下一轮 Rework prompt 包含 latest review reason | `TestReworkPromptIncludesLatestReviewReason` |
| prompt 包含 previous review packet safe summary | `TestReworkPromptIncludesLatestReviewReason` + `TestBuildSafeSummaryFromRunHydratesFromArtifacts` |
| prompt snapshot 记录 metadata/hash | `TestReworkPromptSnapshotRecordsMetadata`、`TestReworkPromptDeterministicAcrossRuns` |
| Rework 继续复用 workspace、branch、base_sha，并保留 cumulative diff 语义 | `TestReworkCumulativeDiffPreservedAcrossIterations` |
| 禁止 raw prompt/log/secret artifact 内容进入 prompt | `TestSafeSummaryScanForRawArtifactsRejectsRefusalKindTokens` + `TestReworkPromptSnapshotExcludesRawArtifactMarkers` + `TestBuildSafeSummaryRejectsReviewPacketsWithRawRefusalKinds` |
| redacted prompt snapshot 可用于 review packet 和 diagnostics 追踪 | `review.Generator.Generate` 写入的 `safe_summary` 字段；orchestrator 的 `rework_snapshots` 表 |

## 运行验证

```text
$ go test -timeout 120s ./...
?   	local-symphony/cmd/symphony	[no test files]
?   	local-symphony/internal/agent	[no test files]
ok  	local-symphony/internal/agent/codex	34.224s
ok  	local-symphony/internal/agent/fake	1.039s
ok  	local-symphony/internal/app	6.444s
ok  	local-symphony/internal/cli	6.401s
ok  	local-symphony/internal/config	3.228s
?   	local-symphony/internal/core	[no test files]
ok  	local-symphony/internal/daemonclient	4.080s
ok  	local-symphony/internal/db	2.249s
ok  	local-symphony/internal/gitx	1.521s
ok  	local-symphony/internal/httpapi	17.123s
ok  	local-symphony/internal/observability	6.622s
ok  	local-symphony/internal/orchestrator	17.695s
?   	local-symphony/internal/platform	[no test files]
ok  	local-symphony/internal/review	10.630s
ok  	local-symphony/internal/security	5.427s
ok  	local-symphony/internal/store	7.563s
ok  	local-symphony/internal/toolgateway	6.555s
?   	local-symphony/internal/tracker/local	[no test files]
ok  	local-symphony/internal/workspace	4.595s

$ python3 scripts/validate_contracts.py
... contract validation passed

$ bash scripts/acceptance-local.sh
acceptance-local passed

$ SYMPHONY_TEST_CODEX=1 go test -timeout 60s -run Integration ./internal/agent/codex/
ok  	local-symphony/internal/agent/codex	1.664s
```

## 已知限制

- `cumulative_diff_sha` 当前从 `git rev-parse HEAD` 派生；如果 workspace 不是 git 仓库，hash 留空，semantic 退化为 “base_sha 稳定”。
- `LatestReviewReasonForIssue` 优先匹配 `issue_state_history` 中 `to_state='Rework'` 的 operator 行；如果数据被手工编辑，fallback 到最近 operator comment。
- `injectReworkContext` 必须在 review packet 落盘之后才能拿到 `safe_summary`；本工作包把 safe summary 写入 `review.json` 的时机放在 `InsertReviewPacketTx` 事务之后。
- v1 范围内 `rework_snapshots` 不参与 diagnostics 导出（保留为内部指针表，避免和 D1 review packet diagnostics 重复）。
- 范围严格限制 v1 既有禁用项：auto push/PR/publish/merge、auto commit、auto workspace cleanup/reset/rebase、auto retry queue/timer、dynamic tools/MCP、remote dashboard、multi-tenant RBAC、secret 管理、raw prompt/raw Codex log/raw secret 暴露。

## 提交

(待 `git commit` 在 worktree 分支 `codex/v1-productization-d4-rework` 上落)
