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

---

## Round 8 — protected-content copy/typechange/special-file leaks (commit `c669420` review)

Codex 第 8 轮 review（针对 commit `c669420`）提了 7 个 finding（2 P1 + 5 P2），全部围绕 protected-content（secrets）在 review packet 收集和 `cumulative_diff_sha` 哈希中的多个绕过路径。逻辑高度相关，合并修复。

### R8-1 / R8-2 — modified tracked protected-copy leak (P1 / P2)

**问题**：当受保护文件被复制覆盖到一个**已 tracked** 的非保护文件（`cp .env config.txt`，`config.txt` 已 tracked），porcelain 报 `M config.txt`（不是 `A`）。protected-content 哈希检查只对 ADDED (`A`) record 运行（review.go `isAddedRecord` / orchestrator.go `HasPrefix "A"`），`M` record 漏过 → `git diff HEAD -- config.txt` 把受保护字节（`SECRET=new`）emit 进 `changes.patch` / hash 进 `cumulative_diff_sha`。

**修复**：把 content-hash 检查扩展到 MODIFIED (`M`) record。
- review.go `collectChanges`：新增 `isModifiedTrackedRecord` + `matchesModifiedTracked`。`matchesModifiedTracked` 仅在 `unknown=false`（source REMAINS）时对 M record 的 workspace content 做 content-hash match（命中 `existingHashes` 则 deny）；`unknown=true` 时**不** fail-closed M record（保留无关的 in-progress modification，遵循 `TestGenerateDoesNotReincludeProtectedRenameFromHandoff` 已批准的 trade-off）。
- orchestrator.go `filteredTrackedDiffPathspecs`：同样对 `M` record 在 `!hashesUnknown && len(existingHashes)>0` 时做 content-hash match，命中则 skip。

**Regression test**：`TestGenerateSuppressesModifiedTrackedCopyOfProtectedFile`（review）/ `TestFilteredTrackedDiffSkipsModifiedTrackedCopyOfProtectedFile`（orchestrator）。两者都验证 `M config.txt`（modified-source copy）被抑制、无关 `M feature.txt`（old→new）被保留。

### R8-3 / R8-4 — protected typechange (T) fail-closed (P1 / P2)

**问题**：受保护 tracked 文件被 typechange（`T`，例如 modify 后替换成 symlink）。其 modified bytes 不可恢复（与 D/R/C 同类不可恢复性）。但 `protectedDeletedContent`（review.go）只检查 `deleted()` (D) 和 `renamedOrCopied()` (R/C)，`deletedProtectedContentHashes`（orchestrator.go）用 `--diff-filter=DRC`，都排除 `T` → `unknown`/`hashesUnknown` 保持 false → copied secret（added `public.txt`）漏进 `changes.patch` / `cumulative_diff_sha`。

**修复**：把受保护 `T` record 当作 fail-closed（`unknown=true` / `hashesUnknown=true`），与 D/R/C 一致。
- review.go：新增 `typechange()` 方法（`code[0]=='T' || code[1]=='T'`），`protectedDeletedContent` 对受保护 `T` record（path 在 `record.paths[0]`）返回 `unknown=true`。
- orchestrator.go：`deletedProtectedContentHashes` 把 `--diff-filter=DRC` 改为 `--diff-filter=DRCT`，并对 `T` record 检查受保护 path → fail-closed。

**Regression test**：`TestGenerateFailsClosedOnProtectedTypechange`（review）/ `TestFilteredTrackedDiffFailsClosedOnProtectedTypechange`（orchestrator）。场景：modify `.env` → copy 到 added `public.txt` → `rm .env; ln -s public.txt .env`（typechange）。验证 `SECRET=new` 不泄漏、`public.txt` 被 fail-closed 抑制。

### R8-5 / R8-6 — special-file blocking in workspace hashers (P2)

**问题**：`reviewHashWorkspaceFile`（review.go:670）和 `hashWorkspaceFile`（orchestrator.go:1009）直接 `os.Open` + `io.Copy`，不检查 `Lstat().Mode().IsRegular()`。受保护 path 若是 FIFO / device / 指向 `/dev/zero` 的 symlink，会**无限阻塞** review packet 生成 / rework prompt 生成。

**修复**：两个 helper 都先 `os.Lstat`，非 regular file 和 symlink 返回 `ok=false`（不 open、不读）。镜像 `readUntrackedPatchData` 的 Lstat+IsRegular guard。

**Regression test**：`TestReviewHashWorkspaceFileSkipsSpecialFiles`（review）/ `TestHashWorkspaceFileSkipsSpecialFiles`（orchestrator）。直接调 helper（不经过 Generate/computeCumulativeDiffSHA），用 FIFO + symlink + regular file 验证 FIFO/symlink 返回 `ok=false`、regular file 正常 hash。直接调 helper 确保测试本身不会因 FIFO 阻塞挂起。

### R8-7 — binary untracked protected-copy SHA256 leak (P2)

**问题**：review.go `matchesUntracked` 中，`readUntrackedPatchData` 对小型**二进制** copy of protected file（含 NUL）返回 `(data, size, binaryOrLarge, nil)` —— `data != nil` 且 `reason != nil`。旧代码 `if reason != nil || data == nil { return false }` 在比较 hash 前就返回 false（preserve），`untrackedInfo` 随后 `sha = SHA256Bytes(data)` → 受保护字节的 content-derived fingerprint 泄漏进 `untracked-files.json` / `review.json`。orchestrator 的 `cumulativeUntrackedDigest` 路径用 streaming hash 不受此影响（已正确 suppress）。

**修复**：`matchesUntracked` 改为只在 `data == nil`（symlink / non-regular / large，无字节返回且 `untrackedInfo` 留 `sha=""`）时 preserve；`data != nil`（text **或** binary，有字节）一律先对 `existingHashes` 做 content-hash match，命中则 suppress。这样小型二进制 protected copy 被抑制（无 patch、无 sha）。

**Regression test**：`TestGenerateSuppressesBinaryUntrackedCopyOfProtectedContent`。场景：protected `.env` 含 NUL（二进制）→ cp 到 untracked `safe.txt`。验证 `SECRET=binary` 原文和其 SHA256 fingerprint 都不泄漏、`safe.txt` 无 patch 无 sha、无关 `notes.txt` 被保留。

### 验收对照（Round 8）

| Finding | Severity | Regression test | 修复位置 | 状态 |
| --- | --- | --- | --- | --- |
| R8-1 modified tracked protected-copy (review) | P1 | `TestGenerateSuppressesModifiedTrackedCopyOfProtectedFile` | `internal/review/review.go` `collectChanges` + `matchesModifiedTracked` + `isModifiedTrackedRecord` | ✅ |
| R8-2 modified tracked protected-copy (cumulative) | P2 | `TestFilteredTrackedDiffSkipsModifiedTrackedCopyOfProtectedFile` | `internal/orchestrator/orchestrator.go` `filteredTrackedDiffPathspecs` M-record 分支 | ✅ |
| R8-3 protected typechange fail-closed (review) | P1 | `TestGenerateFailsClosedOnProtectedTypechange` | `internal/review/review.go` `typechange()` + `protectedDeletedContent` | ✅ |
| R8-4 protected typechange fail-closed (cumulative) | P2 | `TestFilteredTrackedDiffFailsClosedOnProtectedTypechange` | `internal/orchestrator/orchestrator.go` `deletedProtectedContentHashes` `--diff-filter=DRCT` + T 分支 | ✅ |
| R8-5 special-file blocking (review hasher) | P2 | `TestReviewHashWorkspaceFileSkipsSpecialFiles` | `internal/review/review.go` `reviewHashWorkspaceFile` Lstat guard | ✅ |
| R8-6 special-file blocking (cumulative hasher) | P2 | `TestHashWorkspaceFileSkipsSpecialFiles` | `internal/orchestrator/orchestrator.go` `hashWorkspaceFile` Lstat guard | ✅ |
| R8-7 binary untracked protected-copy SHA256 leak | P2 | `TestGenerateSuppressesBinaryUntrackedCopyOfProtectedContent` | `internal/review/review.go` `matchesUntracked` data!=nil 分支 | ✅ |

### 验收门禁（Round 8）

```
go test ./internal/review ./internal/orchestrator ./internal/store ./internal/db   PASS
go test ./...                                                                       PASS
python3 scripts/validate_contracts.py                                               PASS (contract validation passed)
bash scripts/acceptance-local.sh                                                    PASS (acceptance-local passed)
go vet ./internal/review/ ./internal/orchestrator/                                  PASS
```

### 设计权衡说明

- **M record 在 `unknown=true` 时不 fail-closed**（与 A record 不同）：`unknown=true` 时 `existingHashes` 为 nil（受保护文件 pre-operation worktree bytes 不可恢复），无法做 content-hash match；但 fail-closed **所有** M record 会丢掉无关的 in-progress modification（如 protected rename 期间的 `app.txt` old→new），破坏已批准的 trade-off（`TestGenerateDoesNotReincludeProtectedRenameFromHandoff`）。因此 M record 仅在 `unknown=false`（source REMAINS）时做 content-hash match，`unknown=true` 时保留。"protected delete 的字节被复制到一个 tracked M file" 是一个 residual evasion，A/untracked 的 fail-closed 仅覆盖新文件——这是有意识接受的残余风险，与 review finding 描述的 source-REMAINS 场景一致。
- **T record 归入 fail-closed**：typechange 的 pre-typechange worktree bytes 不可恢复（regular file 被 symlink 替换后字节消失），且 copy 只会匹配已不存在的 modified bytes 而非 HEAD/index/symlink-target 版本，content-hash match 无法保证安全。因此与 D/R/C 同等 fail-closed。

---

## Round 9 — symlinked-protected-source + configured-protected-paths (commit `5b976c5` review)

Codex 第 9 轮 review（针对 commit `5b976c5`）提了 3 个 finding（2 P1 + 1 P2），都是 round-8 Lstat 修复和长期存在的 config 透传遗漏引入/暴露的 protected-content 泄漏。

### R9-A / R9-B — follow regular symlink targets in workspace hashers (P1 / P2)

**问题**：round-8 的 Lstat guard 让 `reviewHashWorkspaceFile`（review.go）/ `hashWorkspaceFile`（orchestrator.go）对**所有** symlink 返回 `ok=false`。但受保护 path 若是**指向 regular secret 文件的 symlink**（`.env -> ../shared/env`），其 workspace bytes 现在永远不会被 hash 进 `existingHashes` → 通过该 symlink 复制出的 added/untracked file 无法被 content-hash 抑制 → secret 泄漏进 `changes.patch`/`review.json`/`cumulative_diff_sha`。

**修复**：两个 helper 改用 `os.Stat`（follow symlink）：symlink 指向 regular file 则 hash 其 target 字节；symlink 指向 FIFO/device/socket（如 `-> /dev/zero`）则 Stat 解析出非 regular mode → skip（不 open、不阻塞）；broken/looping symlink Stat 失败 → `ok=false`。受保护 symlinked 文件的字节重新进入 `existingHashes`。

- review 路径：porcelain 把 verbatim copy 报为 `A`，A-record content-hash check 命中 `existingHashes`（含 symlinked protected bytes）→ 抑制。
- orchestrator 路径：`git diff --find-copies-harder` 把 verbatim copy 报为 `C`（source = 非 protected 的 regular target），所以 per-record protected-source check 不触发、A-record check 也不运行（status 是 C 不是 A）。**R9 额外把 content-hash check 扩展到 `C` 目标**（与 A 同 fail-closed + content-hash 逻辑），使 C-record 副本也被抑制。

**Regression test**：
- 单元：`TestReviewHashWorkspaceFileSkipsSpecialFiles` / `TestHashWorkspaceFileSkipsSpecialFiles` 扩展（FIFO/symlink-to-FIFO skip；symlink-to-regular follow+hash；broken symlink skip）。
- 端到端：`TestGenerateSuppressesCopyViaSymlinkedProtectedFile`（review，porcelain A 路径）/ `TestFilteredTrackedDiffSkipsCopyViaSymlinkedProtectedFile`（orchestrator，C 路径）。

### R9-C — honor configured protected paths in review collection (P1)

**问题**：review.go 在 6 处用 `security.IsProtectedPath`（**仅** built-in 默认），忽略 workflow `approvals.protected_paths`。所以自定义 protected path（如 `secrets/**`）在 review 收集中不被保护：`reviewSafePath` 放行、`existingProtectedContentHashes` 不 hash、`protectedDeletedContent`/`protectedCopyDestinations` 不识别。一个 modified/copied `secrets/token.txt` 能 emit 进 `changes.patch`/`review.json`，尽管系统其它部分（orchestrator）把它当 protected。这是**长期存在**的 gap，round-8 Lstat 修复交互后更显眼。

**修复**：`Generate` 通过 `loadProtectedPaths(g.Store.RepoRoot)`（`config.Load`，缺 WORKFLOW.md 时回退 `security.DefaultPolicy().ProtectedPaths`）读取 workflow protected_paths，透传给 `collectChanges` → `reviewSafePath`/`reviewDiffPaths`/`protectedDeletedContent`/`existingProtectedContentHashes`/`protectedCopyDestinations`，全部改用 `security.IsProtectedPathWithConfig(path, protectedPaths)`，与 orchestrator 一致。

**Regression test**：`TestGenerateHonorsConfiguredProtectedPaths`。在 repo root 写 WORKFLOW.md（`protected_paths: ["secrets/**"]`），git workspace 里 commit `secrets/token.txt`、modify、cp 到 added `public.txt`。验证 `secrets/token.txt` 与 `public.txt`（custom-protected 副本）均不泄漏，无关 added `feature.txt` 被保留。

### 验收对照（Round 9）

| Finding | Severity | Regression test | 修复位置 | 状态 |
| --- | --- | --- | --- | --- |
| R9-A follow regular symlink targets (review hasher) | P1 | `TestReviewHashWorkspaceFileSkipsSpecialFiles` + `TestGenerateSuppressesCopyViaSymlinkedProtectedFile` | `internal/review/review.go` `reviewHashWorkspaceFile` Lstat→Stat | ✅ |
| R9-B follow regular symlink targets (cumulative hasher) | P2 | `TestHashWorkspaceFileSkipsSpecialFiles` + `TestFilteredTrackedDiffSkipsCopyViaSymlinkedProtectedFile` | `internal/orchestrator/orchestrator.go` `hashWorkspaceFile` Lstat→Stat + `filteredTrackedDiffPathspecs` C-record content-hash 分支 | ✅ |
| R9-C honor configured protected paths in review | P1 | `TestGenerateHonorsConfiguredProtectedPaths` | `internal/review/review.go` `loadProtectedPaths` + 透传 protectedPaths 到 collectChanges/reviewSafePath/protectedDeletedContent/existingProtectedContentHashes/protectedCopyDestinations/reviewDiffPaths | ✅ |

### 验收门禁（Round 9）

```
go test ./...                                                                       PASS
python3 scripts/validate_contracts.py                                               PASS (contract validation passed)
go vet ./internal/review/ ./internal/orchestrator/                                  PASS
bash scripts/acceptance-local.sh                                                    PASS (acceptance-local passed)
```

### 设计权衡说明（Round 9）

- **Lstat → Stat（follow symlink）**：round-8 用 Lstat 是为了不阻塞 FIFO/device/symlink。Stat 同样能拒绝 FIFO/device（Stat follow symlink 后解析出非 regular mode → skip，不 open），同时让指向 regular secret 的 symlink 被 hash 进 `existingHashes`。symlink-to-`/dev/zero` 解析为 device → skip（不阻塞），满足 round-8 的防阻塞目标。
- **C-record content-hash check（orchestrator）**：`--find-copies-harder` 把 verbatim copy 报为 `C <src> <dst>`。当受保护文件是 symlink 指向非 protected regular 文件时，copy 的 source 是那个非 protected regular 文件，per-record protected-source check 不触发。把 content-hash check 扩展到 C 目标（与 A 同 fail-closed 语义）使这种 copy 被抑制。C 目标本身是新文件，`hashesUnknown=true` 时 fail-closed 与 A 一致。
- **review 路径不需要 C-record check**：review 用 porcelain（不 detect copy），verbatim copy 一律报为 `A`，A-record content-hash check 已覆盖。review 的 `protectedCopyDestinations`（用 `--find-copies-harder`）只用于额外标记 protected-source copy，R9-C 已让它用 `IsProtectedPathWithConfig`。
- **config 替换而非追加**：`applyMap` 对 `protected_paths` 是替换（`c.Approvals.ProtectedPaths = v`），不是追加。这是既有行为，orchestrator 一直如此；R9-C 只让 review 与之一致，不改变 config 语义。

---

## Round 10 — run-snapshot protected-path policy + typechange-on-non-protected-path + `=` delimiter (commit `46e8daa` review)

Codex 第 10 轮 review（针对 commit `46e8daa`）提了 3 个 finding（2 P1 + 1 P2）。

### R10-D — read protected paths from the run snapshot, not the live config (P1)

**问题**：round-9 的 `loadProtectedPaths` 在 review 生成时 `config.Load(repoRoot)` 重读**实时** WORKFLOW.md。若 run dispatch 后、review 生成前 WORKFLOW.md 被编辑/删除，review 用的是新策略而非 run 实际 dispatch 时的策略。`secrets/**` 这种自定义 protected path 可能回落到默认或不同策略 → `collectChanges` 把 protected file/copy emit 进 `changes.patch`/`review.json`，违反 run 实际 governed 的策略（TOCTOU）。

**修复**：`loadProtectedPaths(st, run, repoRoot)` 优先从 run 的 `WorkflowSnapshotID` 读 `workflow_snapshots.config_json`（dispatch 时捕获的 `EffectiveConfig`），解析 `approvals.protected_paths`。只有 snapshot 缺失/不可读（如老 run 无 snapshot）才回退到 `config.Load`（实时），再回退到 `security.DefaultPolicy().ProtectedPaths`。新增 `Store.GetWorkflowSnapshotConfigJSON(wfID)`。

**Regression test**：`TestGenerateUsesRunSnapshotProtectedPaths`。给 run attach 一个 `protected_paths=["secrets/**"]` 的 snapshot，再在 disk 写一个 `protected_paths=[]` 的实时 WORKFLOW.md，验证 review 仍按 snapshot 的 `secrets/**` 策略抑制 protected file/copy，无关 `feature.txt` 保留。

### R10-F — suppress protected bytes in tracked typechanges (P1)

**问题**：受保护文件被复制成一个已 tracked **非保护**文件的 symlink target（`rm config.txt; ln -s "$(cat .env)" config.txt`），porcelain 报 `T config.txt`（**非保护** path 上的 typechange）。round-8 的 protected-path typechange fail-closed 只对**保护** path 上的 `T` 触发；非保护 path 上的 `T` 漏进 `changed`，`git diff HEAD -- config.txt` 把 symlink target 文本（`+SECRET=real`）emit 进 `changes.patch`/`cumulative_diff_sha`。

**修复**：对**非保护** path 上的 `T` record 做 content-hash check —— 读 `git diff` 会 emit 的字节（symlink 则 `os.Readlink` 的 target 文本；否则 workspace 文件内容），hash 后对 `existingHashes` 匹配，命中则 deny/skip。`unknown=true` 时**不** blanket fail-closed `T`（保留无关 typechange，与 M trade-off 一致）。
- review：`collectChanges` 新增 `T` 分支 + `matchesTypechangeTracked`。
- orchestrator：`filteredTrackedDiffPathspecs` 新增 `T` 分支 + `hashTypechangeEmittedBytes`。

**Regression test**：`TestGenerateSuppressesProtectedBytesInTrackedTypechange`（review）/ `TestFilteredTrackedDiffSkipsProtectedBytesInTrackedTypechange`（orchestrator）。验证 `T config.txt`（symlink target = secret）被抑制、`SECRET=real` 不泄漏、无关 `M feature.txt` 保留。

### R10-E — treat `=` as a raw-marker boundary (P2)

**问题**：`safe_summary.go` 的 `isBoundaryByteCI` 不把 `=` 当 boundary，所以 `kind=raw_prompt` / `artifact=codex_log` 这种 key/value 形态的 raw-artifact marker 不被 `scanRefusalKind` 识别 → `Seal` 接受 → `BuildReworkPrompt` 把 raw marker 渲染进下一个 prompt，绕过 D4 raw-prompt/log 屏蔽。

**修复**：`isBoundaryByteCI` 增加 `=` 为 boundary 字节。`=` 不出现在 path segment 或 identifier 中，所以不会引入 path false positive（`docs/raw_prompt.md` 仍由 `isPathPunctuationBoundary` 的 `.` 处理保护）。

**Regression test**：`TestSafeSummaryScanRejectsKeyValueDelimitedRefusalKinds`。验证 `kind=raw_prompt`/`artifact=codex_log`/`ref=prompt_snapshot`/`source=secret_artifact`/`type=codex_events` 均被 `Seal` 拒绝，且 `docs/raw_prompt.md` 合法路径仍被接受。

### 验收对照（Round 10）

| Finding | Severity | Regression test | 修复位置 | 状态 |
| --- | --- | --- | --- | --- |
| R10-D run-snapshot protected-path policy | P1 | `TestGenerateUsesRunSnapshotProtectedPaths` | `internal/review/review.go` `loadProtectedPaths(st,run,repoRoot)` + `internal/store/store.go` `GetWorkflowSnapshotConfigJSON` | ✅ |
| R10-F typechange-on-non-protected-path | P1 | `TestGenerateSuppressesProtectedBytesInTrackedTypechange` / `TestFilteredTrackedDiffSkipsProtectedBytesInTrackedTypechange` | `internal/review/review.go` `matchesTypechangeTracked` + `internal/orchestrator/orchestrator.go` `hashTypechangeEmittedBytes` + `T` 分支 | ✅ |
| R10-E `=` delimiter | P2 | `TestSafeSummaryScanRejectsKeyValueDelimitedRefusalKinds` | `internal/review/safe_summary.go` `isBoundaryByteCI` 加 `=` | ✅ |

### 验收门禁（Round 10）

```
go test ./...                                                                       PASS
python3 scripts/validate_contracts.py                                               PASS (contract validation passed)
go vet ./internal/review/ ./internal/orchestrator/ ./internal/store/                PASS
bash scripts/acceptance-local.sh                                                    PASS (acceptance-local passed)
```

### 设计权衡说明（Round 10）

- **snapshot 优先于 live config**：review 必须用 run dispatch 时捕获的策略，而非实时 WORKFLOW.md。snapshot 的 `config_json` 是 dispatch 时 `EffectiveConfig` 的精确快照。回退链（snapshot → live `config.Load` → `DefaultPolicy`）保证老 run（无 snapshot）不崩，且回退仍用 defaults（安全）。
- **`T` record 不 blanket fail-closed**：与 M 一致——`unknown=true` 时保留无关 typechange（如 app 配置文件类型变更），仅 `unknown=false` 时对 emit 字节做 content-hash match。"protected delete 字节被塞进非保护 path 的 typechange symlink target" 是 `unknown=true` 下的 residual evasion，A/untracked 的 fail-closed 不覆盖 typechange——这是有意识接受的残余风险（与 M 同类），与 finding 描述的 source-REMAINS 场景一致。
- **`=` boundary 不影响 path**：`=` 不在 path segment / identifier 中出现，加它只影响 key/value prose 形态。path 中的 `.` 仍由 `isPathPunctuationBoundary` 特殊处理（path 内的点不是 boundary）。

---

## Round 11：typechange symlink target 尾部换行符归一化

**Trigger**: codex 审查 commit `f20a9d1`，1 条新 finding（G，P1）。

### 发现列表

| 编号 | 发现 | 严重度 | 回归测试 | 涉及文件 | 状态 |
|------|------|--------|----------|----------|------|
| R11-G | shell-trimmed symlink target hashes | P1 | 已有 `TestFilteredTrackedDiffSkipsProtectedBytesInTrackedTypechange` / `TestGenerateSuppressesProtectedBytesInTrackedTypechange` 覆盖核心路径 | `internal/review/review.go` `matchesTypechangeTracked` + `internal/orchestrator/orchestrator.go` `hashTypechangeEmittedBytes` | ✅ |

### 详情

**G (P1) — shell-trimmed symlink target hashes**：

`ln -s "$(cat .env)" config.txt` 场景中，shell 命令替换 `$(cat .env)` 会截去尾部换行符。当 `.env` 末尾是 `\n`（`"SECRET=real\n"`）时，symlink target 是 `"SECRET=real"`（无换行），但 `existingHashes` 存的是 `SHA256("SECRET=real\n")`（从磁盘文件读取）。`matchesTypechangeTracked` 和 `hashTypechangeEmittedBytes` 的 hash 比对失败 → typechange 的 patch 泄漏 secret。

**修复**：

- `review.go` `matchesTypechangeTracked`：在 `os.Readlink` 返回后，先检查 `target` 的 hash，再检查 `target+"\n"` 的 hash（覆盖 shell 截断场景）。
- `orchestrator.go` `hashTypechangeEmittedBytes`：返回类型从 `(string, bool)` 改为 `([]string, bool)`，返回多个 hash 变体（`target` + `target+"\n"`）。调用方遍历所有 hash 比 `existingHashes`。
- `target+"\n" != target` 的 guard 确保在 target 自身含 `\n` 时不重复计算。

### 验收门

```
go test ./...                                   PASS
go vet ./...                                    PASS
validate_contracts.py                           PASS
acceptance-local.sh                             PASS
```

---

## Round 12：多换行截断 + 快照持久化 + 未跟踪符号链接

**Trigger**: codex 审查 commit `a6d2bbd`，4 条新发现（H P1，I/J/K P2）。

### 发现列表

| 编号 | 发现 | 严重度 | 回归测试 | 涉及文件 | 状态 |
|------|------|--------|----------|----------|------|
| R12-H | 多换行截断（review） | P1 | 已有 `TestGenerateSuppressesProtectedBytesInTrackedTypechange` | `internal/review/review.go` `existingProtectedContentHashes` | ✅ |
| R12-I | 多换行截断（orchestrator） | P2 | 已有 `TestFilteredTrackedDiffSkipsProtectedBytesInTrackedTypechange` | `internal/orchestrator/orchestrator.go` `existingProtectedContentHashes` | ✅ |
| R12-J | rework 快照写入失败静默忽略 | P2 | 已有 `TestReworkPrompt` 套件 | `internal/orchestrator/orchestrator.go` `runWorker` | ✅ |
| R12-K | 未跟踪符号链接目标未比对 existingHashes | P2 | `TestCumulativeDiffSHA` 套件 | `internal/orchestrator/orchestrator.go` `computeCumulativeDiffSHA` | ✅ |

### 详情

**H/I (P1/P2) — 多换行截断**：shell 命令替换 `$(cat .env)` 会截去**所有**尾部换行符，不止一个。当 `.env` 末尾是 `"SECRET=real\n\n"` 时，symlink target 是 `"SECRET=real"`（两个换行符都被截去），但 round-11 的修复只检查 `target+"\n"`（一个换行符），仍不匹配。

**修复方案**：将尾部换行符截断归一化逻辑移至 `existingProtectedContentHashes` —— 在构建保护文件的哈希集合时，同时添加 `bytes.TrimRight(content, "\n")` 的哈希。这样 `matchesTypechangeTracked` 和 `hashTypechangeEmittedBytes` 无需任何特殊处理，`existingHashes` 中已有截断后的哈希，symlink target 的哈希自然匹配（无论截去多少个换行符）。

- `review.go` `existingProtectedContentHashes`：在 workspace 文件 hash 后，`os.ReadFile` 读取文件内容，计算 `TrimRight` 后的哈希加入集合。
- `orchestrator.go` `existingProtectedContentHashes`：同上。
- `review.go` `matchesTypechangeTracked`：回退 round-11 的 `target+"\n"` 特殊检查，恢复为简洁的单一哈希检查。
- `orchestrator.go` `hashTypechangeEmittedBytes`：恢复为 `(string, bool)` 签名，移除 round-11 的 `[]string` 变体。

**J (P2) — rework 快照持久化失败静默忽略**：`CreatePromptSnapshot` 和 `CreateReworkSnapshot` 的错误被 `_, _` 丢弃。当 DB 约束/触发器失败或磁盘满时，rework 元数据（review reason、safe-summary hash、prompt hash）静默丢失，后续诊断无法关联 rework。

**修复**：`CreatePromptSnapshot` 失败 → `FailRun` 并返回。`CreateReworkSnapshot` 失败 → `FailRun` 并返回。（与工作流快照错误的处理方式一致。）

**K (P2) — 未跟踪符号链接目标未比对 existingHashes**：`computeCumulativeDiffSHA` 中未跟踪符号链接的目标文本直接写入 `cumulative_diff_sha`，未检查 `existingHashes`。`ln -s "$(cat .env)" leak` 在未跟踪路径上的秘密泄漏。

**修复**：在写入目标文本前，对其哈希比对 `existingHashes`，匹配时写入哨兵 `"suppressed:existing-protected-content-match"`（与普通文件处理一致）。

### 验收门

```
go test ./...                                   PASS
go vet ./...                                    PASS
validate_contracts.py                           PASS
acceptance-local.sh                             PASS
```


---

## Round 13: M fail-closed + modified symlink target + blob rtrim

**Trigger**: codex review commit `b7c2220`, 6 findings (L/N/P P1, M/O/Q P2).

| ID | Finding | Severity | Files | Status |
|----|---------|----------|-------|--------|
| R13-L | M unknown=true not fail-closed (review) | P1 | review.go matchesModifiedTracked | done |
| R13-M | M hashesUnknown not fail-closed (orchestrator) | P2 | orchestrator.go filteredTrackedDiffPathspecs | done |
| R13-N | Modified symlink target hashed as file content | P1 | review.go matchesModifiedTracked | done |
| R13-O | Same, orchestrator path | P2 | orchestrator.go M guard | done |
| R13-P | HEAD/index blob missing rtrim variant | P1 | review.go existingProtectedContentHashes | done |
| R13-Q | Same, orchestrator path | P2 | orchestrator.go existingProtectedContentHashes | done |

### Details

**L/M**: `cp .env config.txt && git rm .env` produces `D .env` + `M config.txt`. Round-8 M guard only checked when !unknown. Fix: always build existingHashes (HEAD/index blobs recoverable via git show even for deleted files). Content-hash-match M records even when unknown=true.

**N/O**: Tracked symlink `config -> safe.txt` modified to `config -> SECRET` reports `M config`. reviewHashWorkspaceFile follows symlinks and hashes target file content, but git diff emits target TEXT. Fix: check os.Readlink first for M symlinks, hash target text.

**P/Q**: Round-12 only added rtrim to workspace hashes. Staged blob `SECRET

` -> `$(git show :.env)` strips both newlines. Fix: add reviewHashGitBlobRTrim / hashGitBlobRTrim, compute rtrim hash for HEAD/index blobs.

### Design trade-offs

- **existingHashes always built**: HEAD/index blobs recoverable even when unknown=true. Workspace version of deleted file naturally skipped (os.ReadFile fails). Enables M/T content-hash matching without blanket fail-closed.
- **Symlink target first**: os.Readlink on M symlinks before falling back to file content hash. Matches what git diff actually emits.

### Acceptance gate

```
go test ./...                                   PASS
go vet ./...                                    PASS
validate_contracts.py                           PASS
acceptance-local.sh                             PASS
```
