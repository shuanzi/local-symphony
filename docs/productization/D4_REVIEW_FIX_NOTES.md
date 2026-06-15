# D4 R-fix — PR #27 review findings 收口

实施记录，覆盖 PR #27 codex review 的 5 个 finding（1 P1 + 4 P2）。

## 1 个 P1

### F2 — `internal/orchestrator/orchestrator.go:184` (P1)
**问题**：Run fail 在 review packet 生成之前时，`rework_prompt.redacted.md` 留在 disk 上含**完整** rendered prompt（被标为 `[redacted]`）。rework branch 更糟，`rework_prompt.redacted.md` 从不被 review generator 覆盖，违反 raw-prompt logging 边界（rendered prompt 可含 raw issue description 和 workflow prompt）。

**修复**：
- `rework_prompt.redacted.md` 改为 metadata-only 文件，包含 `redaction: metadata-only`、`prompt_length_bytes`、`prompt_sha256`、`review_reason_redacted`。**不再**写入 raw prompt 文本。
- 同步修复 non-rework 路径的 `rendered_prompt.redacted.md`，保持一致。

**Failing test**：`TestRedactedArtifactDoesNotContainRawPrompt`（先 FAIL 再 PASS）
**Regression test**：`TestReworkPromptSnapshotExcludesRawArtifactMarkers` 改读 `rework_prompt.redacted.md`（rework dispatch 实际写入的文件）；`TestReworkPromptIncludesLatestReviewReason` 改读 `rework_snapshots` 表的 `ReviewReason` / `SafeSummarySHA256` / `PromptHash` 来验证 rework 注入确实发生（而非依赖 disk 上的 raw prompt）。

## 4 个 P2

### F1 — `internal/orchestrator/orchestrator.go:401` (P2)
**问题**：`cumulative_diff_sha` 只 hash 了 `baseSHA` + `HEAD`。当 agent 留 uncommitted changes（review path 收集 `git status`/worktree diffs）时，不同累计 worktree diff 产生**相同** `cumulative_diff_sha`，误导 prompt/diagnostic correlation。

**修复**：`computeCumulativeDiffSHA` 把 `git status --porcelain=v1 -z -uall` 输出和 `git diff HEAD` 输出也 hash 进去。不同 uncommitted worktree 状态产生不同 hash。

**Failing test**：`TestCumulativeDiffHashCoversUncommittedChanges`（用真 git workspace 验证 clean vs dirty 状态产生不同 hash）。

### F3 — `internal/orchestrator/orchestrator.go:348` (P2)
**问题**：rework 注入时当前 run 还没产 review packet。`BuildSafeSummaryFromIssue` 用了当前 run 的 fields（如 `SourceIssueState`），fallback 到 issue 的 latest packet 但仍用当前 run 的 fields → 第一次 rework after Ready 渲染时 previous packet 看起来像从 `Rework` 来，corrupt prompt/snapshot metadata。

**修复**：
- 新增 `BuildSafeSummaryFromIssueWithPrev` 入口。`prev` 非 nil 时，`sourceRun = prev`，packet 走 `prev.ID` 查询，`SourceIssueState` 取自 prev。
- Orchestrator `injectReworkContext` 改为直接调用 `BuildSafeSummaryFromIssueWithPrev(o.Store, issue, run, prevRun)`，不再做"先 try 当前 run，失败再 try prev"的两步 fallback（这种 fallback 在 Rework 场景下永远命中第一步的"没有 packet"分支，下游又被错误地塞了当前 run 的 state）。

**Failing test**：`TestReworkSafeSummaryUsesPreviousRunFields`（先 FAIL 再 PASS）。

### F4 — `internal/review/review.go:228` (P2)
**问题**：artifact row for `review.json` 写入时的 size 和 SHA 是在 post-transaction rewrite (加 `safe_summary`) 之前。stale metadata 跟 disk file 不一致。

**修复**：在 post-transaction `safe_summary` rewrite 之后，recompute review.json 的 SHA256 + size，并通过 `UPDATE artifacts SET size_bytes=?, sha256=? WHERE review_packet_id=? AND kind='review_packet' AND path=?` 把 metadata 同步到 post-rewrite 内容。

**Failing test**：`TestReviewArtifactMetadataMatchesPostRewrite`（先 FAIL 再 PASS）。

### F5 — `internal/review/safe_summary.go:465` (P2)
**问题**：substring 匹配 blocklist 误拦正常路径。例如 `internal/secrets/store.go` 或 `ui/prompt_snapshot_view.tsx`。这些合法路径使整 rework dispatch fail `FailurePromptRenderFailed`。

**修复**：把 `containsCI`（纯 substring 匹配）改为 `containsCIToken` —— 强 token-boundary 检查。`isBoundaryByteCI` 只把 **whitespace + sentence/structural punctuation** 视为边界字符（`.`, `,`, `;`, `:`, `!`, `?`, `(`, `)`, `[`, `]`, `{`, `}`, `"`, `'`, `` ` ``），**不**把 `/` `_` `-` alnum 视为边界。这样：
- `internal/secrets/store.go` 中的 `secrets` 因为两侧是 `/`（非 boundary），被识别为嵌入在更大 token 中 → 不被拦截。
- `ui/prompt_snapshot_view.tsx` 中的 `prompt_snapshot` 同理 → 不被拦截。
- prose 中 `leaked raw_prompt token` 的 `raw_prompt` 因为两侧是空格（boundary），仍然被拦截。

**Failing test**：`TestSafeSummaryAllowsSecretPathAsChangedFile`（先 FAIL 再 PASS）。
**Regression test**：`TestSafeSummaryScanForRawArtifactsRejectsRefusalKindTokens` 调整 ChangedFiles fixture，从 `tc.kind + "/x.txt"`（path-bounded）改为 `tc.kind + " helper.go"`（word-bounded），确保 prose 形态的 raw marker 仍然被拦截。

## 验收对照

| Finding | Failing test | 修复位置 | 状态 |
| --- | --- | --- | --- |
| F1 (P2) | `TestCumulativeDiffHashCoversUncommittedChanges` | `internal/orchestrator/orchestrator.go` `computeCumulativeDiffSHA` | ✅ |
| F2 (P1) | `TestRedactedArtifactDoesNotContainRawPrompt` | `internal/orchestrator/orchestrator.go` `runWorker` reworked 路径 + non-rework 路径 | ✅ |
| F3 (P2) | `TestReworkSafeSummaryUsesPreviousRunFields` | `internal/review/safe_summary.go` 新增 `BuildSafeSummaryFromIssueWithPrev` + `internal/orchestrator/orchestrator.go` `injectReworkContext` | ✅ |
| F4 (P2) | `TestReviewArtifactMetadataMatchesPostRewrite` | `internal/review/review.go` post-transaction rewrite 之后的 `UPDATE artifacts` | ✅ |
| F5 (P2) | `TestSafeSummaryAllowsSecretPathAsChangedFile` | `internal/review/safe_summary.go` `containsCIToken` + `isBoundaryByteCI` | ✅ |

## 运行验证

```text
$ go test -count=1 ./internal/orchestrator ./internal/review ./internal/store ./internal/agent/codex
ok  	local-symphony/internal/agent/codex	26.105s
ok  	local-symphony/internal/orchestrator	12.759s
ok  	local-symphony/internal/review	6.669s
ok  	local-symphony/internal/store	4.175s

$ go test -p 1 -count=1 ./...
（全包通过，无 regression）

$ python3 scripts/validate_contracts.py
contract validation passed

$ bash scripts/acceptance-local.sh
acceptance-local passed
```

## 文件改动面

| 路径 | 改动 |
| --- | --- |
| `internal/orchestrator/orchestrator.go` | F1 hash 扩展；F2 metadata-only redacted artifact；F3 改用 `BuildSafeSummaryFromIssueWithPrev` |
| `internal/orchestrator/rework_prompt_test.go` | 新增 F1/F2 failing test + `newReworkDispatchIssueWithGitWorkspace` / `gitInit` / `gitCommit` helper；改 `TestReworkPromptSnapshotExcludesRawArtifactMarkers` / `TestReworkPromptIncludesLatestReviewReason` 不再依赖 raw prompt disk artifact |
| `internal/review/review.go` | F4 post-transaction UPDATE artifacts |
| `internal/review/review_test.go` | 新增 F4/F5 failing test + `sha256Hex` / `mustReviewPacketIDForRun` helper |
| `internal/review/safe_summary.go` | F3 新增 `BuildSafeSummaryFromIssueWithPrev` + `buildSafeSummaryFromIssueCore`；F5 替换 `containsCI` → `containsCIToken` + `isBoundaryByteCI` |
| `internal/review/safe_summary_test.go` | 新增 F3 failing test；改 `TestSafeSummaryScanForRawArtifactsRejectsRefusalKindTokens` ChangedFiles fixture 为 word-bounded |
