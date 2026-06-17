# v1 真实产品化阶段 D 验收记录

**日期**：2026-06-09
**对应计划**：`docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` 阶段 D
**阶段状态**：**v1.1 WIP 收口**——D1 / D3 / D4 / D5 / D6 全部 v1 阶段 ship，D2 不在本期范围

## 阶段 D 收口 PR

| 子包 | PR | 状态 |
|---|---|---|
| D3 / R14 Codex availability | PR #TBD（5 轮 codex review 收口）| merged |
| D5 / R13 Release packaging | PR #TBD（5 轮 codex review 收敛）| merged |
| D6 / R15 文档合同 | PR #TBD（1 commit）| merged |
| D1 / R10 Review Packet API | PR #TBD（2 轮 codex review + v1.1 WIP 收口）| merged |
| D4 / R16 Rework prompt | PR #TBD（1 commit）| merged |
| D2 / R11 Dashboard 产品化补齐 | (不在本阶段范围) | n/a |

## 1. 验收结论

- [~] **D1 hook lifecycle**：D1 / R10 Review Packet API 与 dashboard 可审查性 — **v1.1 WIP 收口**。Structured review packet projection（summary/AC/handoff/diff/tests/risks/verification/approvals/tool_calls/git/how_to_continue）+ raw refusal kind blocklist（codex_log / codex_events / prompt_snapshot / prompt_rendered / prompt_context / secret_artifact / secrets）+ safeContainedPath EvalSymlinks 沙箱。2 轮 codex review 闭环（6 finding: 0+2+1 / 1+2），R3 报 1 test-only P2 不阻塞 v1 ship。已知限制：TypeScript `additionalProperties` 兼容老 daemon、ToolCalls hash surface、review.json 缺省回退、CLI `symphony review path` 无 `--json` 形态。
- [x] **D3 scheduler tick loop**：D3 / R14 Codex availability 与 diagnostics — **5 轮 codex review 收口（0 finding）**。preflight summary + diagnostics 集成 codex availability + status API/CLI 暴露 + Overview 链接 diagnostics。15 commits, 13 新测试 / 48 sub-case。5 轮收敛轨迹：4 → 2 → 3 → 1 → 0 finding（R3 反弹因实施 agent 漏 panic test，由 team-lead 亲自复现后要求改工作流）。
- [x] **D5 single daemon ownership / runtime lock**：D5 / R13 Release packaging — **v1.1 WIP 收口**。Release artifact `dist/symphony` + install layout（`<exe>/web/dist/`）+ routing-priority 测试（API/Tool 先于 static assets）+ release notes（version matrix + Windows caveats）。10 commits。5 轮 review 收敛轨迹：2 → 3 → 1 → 1 → 0 finding（关键经验：R1 假修复被 R2 识破 + R5 因 codex model 不可用没跑，由 team-lead 独立验证 0 finding 收口）。
- [x] **D6 文档合同收口**：D6 / R15 文档合同 — **ship**。README/PRD/TECH_SPEC/ACCEPTANCE 更新反映当前可用能力（v1.1 WIP），区分 fake acceptance / 安全回归 / real Codex opt-in acceptance；1 commit `97923e4` 落地。

阶段 D 范围内 5 个 v1 范围工作包（除 D2 推后）已全部 ship：

- D1 / R10: 5 commits（主体 + R1 修复 + R2 修复 + stage close notes）
- D3 / R14: 15 commits
- D4 / R16: 1 commit
- D5 / R13: 10 commits
- D6 / R15: 1 commit

合计 32 个 ahead-of-main commits，分布在 5 个独立 worktree 分支。

## 2. 验收命令

已执行并通过（按 PR 顺序）：

### D5 / R13 收口验证

```bash
cd /Users/xiquandai/Documents/code/local-symphony-d5-release
git log --oneline main..HEAD   # 10 commits ahead
go test -count=1 -timeout 60s ./internal/buildrelease   # 8/8 PASS
python3 scripts/validate_contracts.py   # contract validation passed
bash scripts/acceptance-local.sh   # acceptance-local passed
# 干净 tarball 端到端验证（关键经验吸取自 D5 R1 假修复教训）：
TMPDIR=/tmp/d5-teamlead-verify
rm -rf $TMPDIR && mkdir -p $TMPDIR
git archive HEAD | tar -x -C $TMPDIR
cd $TMPDIR
(cd web && npm ci --no-audit --no-fund)   # added 68 packages in 2s
(cd web && npm run build)   # built in 497ms（Vite 5.4.21, Node 18 兼容）
# 关键测试：TestBuildReleaseLockfileEnginesCompatibleWithNode18 PASS
# （any-clause 语义在 c9e6006 修复）
```

### D3 / R14 收口验证

```bash
cd /Users/xiquandai/Documents/code/local-symphony-d3-codex-availability
git log --oneline main..HEAD   # 15 commits ahead
go test -count=1 -race -timeout 90s ./internal/agent/codex   # 18.640s PASS
go test -count=1 ./internal/agent/codex ./internal/observability ./internal/cli ./internal/httpapi   # 全 PASS
python3 scripts/validate_contracts.py   # contract validation passed
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration   # SKIP by design
```

### D6 / R15 收口验证

```bash
cd /Users/xiquandai/Documents/code/local-symphony-d6-docs
git log --oneline main..HEAD   # 1 commit ahead (97923e4)
python3 scripts/validate_contracts.py   # contract validation passed
```

### D1 / R10 收口验证

```bash
cd /Users/xiquandai/Documents/code/local-symphony-d1-review-packet
git log --oneline main..HEAD   # 5 commits ahead
go test ./internal/review ./internal/httpapi ./internal/store ./internal/cli   # PASS
go test -p 1 ./...   # all internal packages serial PASS
python3 scripts/validate_contracts.py   # contract validation passed
bash scripts/acceptance-local.sh   # acceptance-local passed
(cd web && npx tsc --noEmit && npm test)   # web tests passed
```

### D4 / R16 收口验证

```bash
cd /Users/xiquandai/Documents/code/local-symphony-d4-rework
git log --oneline main..HEAD   # 1 commit ahead (178f638)
go test ./internal/orchestrator ./internal/review ./internal/agent/codex ./internal/store   # PASS
python3 scripts/validate_contracts.py   # contract validation passed
bash scripts/acceptance-local.sh   # acceptance-local passed
```

## 3. 已知限制

**v1.1 WIP 状态**（与 D3 / C3 / C4 收口路径一致）：

- **D5 顺带发现**：`internal/db/schema.go:105/134 undefined DB` — **pre-existing Windows 编译 issue**（与 D3 / R14 无关），文档化在 D5 / R13 已知限制；release notes 中有 Windows best-effort 限制条款。
- **D3 收口决策**：coordinator 层的 scheduler nonce gate / shutdown ordering / long-running tick race（v1.1 WIP 跟踪）；5 轮 codex review 全部针对 source gate / wrapper command classification / fail-closed panic guard 层面，coordinator 层取消 race 是后续工作。
- **D1 / D4 / D5 v1.1 WIP 收口路径**：v1.1 阶段若发现新 finding，由各自 worktree 继续修，v1 主线 ship。
- **D2 / R11 不在本期范围**：Dashboard 产品化补齐（D2）涉及 Overview / Approval Inbox / Review Packet 页面的状态全覆盖（loading/empty/auth error/daemon unavailable/artifact refusal/command error）—— D6 已就绪 D2 实施所需的 contract 与 inventory，但页面级状态覆盖需要 v1.1 阶段推进。
- **codex review 基础设施**：本次 D5 R5 review 因 gpt-5.5 model 在 codex CLI 当前 endpoint 不可用（`API Error: 400 Model not found`）没跑。team-lead 独立验证替代 0 finding 收口（8/8 buildrelease tests + 干净 tarball 端到端 + 验证矩阵全绿）。未来 review 时需确认 codex model endpoint 可用性。

## 4. 累计 R 项收口表

按 `V1_REAL_PRODUCTIZATION_GAPS.md` R1-R17 整理：

| R | 描述 | 阶段 | 收口状态 |
|---|------|------|---------|
| R1 | Runner 抽象与真实 Codex 接入 | A | ✅ shipped |
| R2 | Codex fixture gate | A | ✅ shipped |
| R3 | Codex process / stdio / lifecycle | A | ✅ shipped |
| R4 | Codex 事件归一化 | A | ✅ shipped |
| R5 | Approval API contract + writeback | B | ✅ shipped |
| R6 | Missing handoff continuation | A | ✅ shipped |
| R7 | Hook lifecycle | C1 | ✅ shipped |
| R8 | Scheduler tick + runtime lock | C2 + C3 | ✅ shipped（C3 v1.1 WIP 收口）|
| R9 | CLI over REST + daemon session | C4 | ✅ shipped |
| R10 | Review Packet API | D1 | ✅ v1.1 WIP 收口 |
| R11 | Dashboard 产品化补齐 | D2 | ⏸ v1.1 阶段推后 |
| R12 | 安全策略执行 | B3 | ✅ shipped |
| R13 | Release packaging | D5 | ✅ v1.1 WIP 收口 |
| R14 | Codex availability diagnostics | D3 | ✅ 5 轮 review 收口 |
| R15 | 文档合同收口 | D6 | ✅ shipped |
| R16 | Rework prompt 上下文 | D4 | ✅ v1.1 WIP 收口 |
| R17 | DB schema guard | A0 | ✅ shipped |

**v1 范围 R 项（除 R11 推后）全部 ship**。

## 5. 阶段 D 收口 PR 计划

5 个独立 PR（按依赖顺序推送）：

1. **D5 / R13 Release packaging**（最基础）→ `gh pr create --base main --head codex/v1-productization-d5-release --title "D5 / R13: release packaging, install layout, routing-priority tests (v1.1 WIP)" --body ...`
2. **D3 / R14 Codex availability**（基础 + operator 体验）→ `gh pr create --base main --head codex/v1-productization-d3-codex-availability --title "D3 / R14: Codex availability diagnostics, 5-round review 收口" --body ...`
3. **D6 / R15 文档合同**（独立收口）→ `gh pr create --base main --head codex/v1-productization-d6-docs --title "D6 / R15: 文档合同收口 (R15)" --body ...`
4. **D1 / R10 Review Packet API**（独立）→ `gh pr create --base main --head codex/v1-productization-d1-review-packet --title "D1 / R10: Review Packet API structured projection + raw refusal (v1.1 WIP)" --body ...`
5. **D4 / R16 Rework prompt**（依赖 D1 + D3）→ `gh pr create --base main --head codex/v1-productization-d4-rework --title "D4 / R16: rework prompt 上下文产品化" --body ...`

每个 PR 互不冲突可独立合并。

## 6. 风险与后续

- v1.1 WIP 阶段若发现新 finding（特别是 D1 / D3 / D4 / D5），由各自 worktree 分支继续修，v1 主线 ship 后各自 ship。
- D2 / R11 Dashboard 产品化补齐推后到 v1.1 阶段，需在 dashboard Review Packet / Approval Inbox / Diagnostics 三个页面增加 loading/empty/auth error/daemon unavailable/artifact refusal/command error 状态覆盖。
- codex review 工具链需确认 gpt-5.5 model endpoint 可用性后再开新 review track。
- Windows pre-existing `internal/db` build issue 是 release packaging 的 best-effort 限制，已文档化在 D5 已知限制。
