# D5 / R13 Codex Review — Round 3

- **Worktree**: `/Users/xiquandai/Documents/code/local-symphony-d5-release`
- **Commit under review**: `cdf08ed` — D5 R2: fix F1-r2/F2-r2/F3-r2 per codex review round 2
- **Base**: `main` (`f15ba39` at review time)
- **Reviewer**: `codex review` (Codex v0.130.0, model `gpt-5.5`, reasoning effort `xhigh`, sandbox `danger-full-access`, agent profile `~/.codex/agents/reviewer.toml`)
- **Command**:
  ```bash
  cd /Users/xiquandai/Documents/code/local-symphony-d5-release
  codex review --commit cdf08ed --title "D5 R13: round 2 review fixes (F1-r2 narrow guard, F2-r2 commit lockfile, F3-r2 always npm ci)"
  ```
  > The `codex review` CLI rejects combining `--base <BRANCH>` with `--commit <SHA>` (exit 2). When reviewing a single commit, pass `--commit` only. The D5 commit is the only delta relative to `main`, so the diff is identical.
- **Timestamp**: 2026-06-09T14:30+0800
- **Exit code**: 0 (clean run, reviewer surfaced findings)
- **Session log**: `~/.codex/sessions/2026/06/09/rollout-2026-06-09T14-27-37-019eab10-acf0-7ff1-bbe6-7fe9e5e19d11.jsonl`
- **Captured stdout/stderr**: `/Users/xiquandai/.claude/projects/-Users-xiquandai-Documents-code-local-symphony/7e606abd-bab6-4fe7-8844-d0c176b6250a/tool-results/bej5zsqdn.txt` (3469 lines, 148 KB — also contains all reviewer shell probes)

## Round 1 → Round 2 → Round 3 收敛

| Round | Reviewed commit | Findings | Outcome |
|---|---|---|---|
| 1 | `573b1f0` | 2 (1 P1 + 1 P2) | 实施 agent 提交 `41dabb6` "修复" |
| 2 | `41dabb6` | 3 (2 P1 + 1 P2) | R1 修复被识破为假修复 + 新增 1 finding |
| 3 | `cdf08ed` | **1 (1 P1)** | F1-r2 / F3-r2 已真正修复；F2-r2 出现新维度问题（见下文） |

**F1-r2 状态：✅ 已真正修复**。F3-r2 状态：✅ 已真正修复。
**F2-r2 状态：🟡 维度变化** — R2 把 `web/package-lock.json` 真正提交进 git 并删了 `web/pnpm-lock.yaml`，但**新提交的 lockfile 用 npm 重新解析时把 Vite 钉到 8.0.16，而 Vite 8 要求 Node 20.19+/22.12+，与项目文档化的 Node 18 LTS 不兼容**。这是同一 F2-r2 问题的"另一面"：之前是 lockfile 不存在，现在是 lockfile 不能在文档化环境跑。

## 摘要

Codex 返回 **1 unique finding**（stdout 末尾被 assistant 重复 echo 一次，与 round 2 行为一致）。

| ID | Severity | File | Status |
|---|---|---|---|
| F1-r3 | **P1** | `web/package-lock.json:910` | **F2-r2 修复的延伸问题**：lockfile 存在但不能跑在文档化 Node 18 环境 |

`overall_correctness`: `patch is incorrect` · `overall_confidence_score`: 0.9

## F1-r3 [P1] Keep the lockfile compatible with documented Node 18

- **File**: `web/package-lock.json:910`（Vite 节点 `engines.node: "^20.19.0 || >=22.12.0"`）
- **Reviewer confidence**: 0.93
- **Reviewer claim**: The release build documents Node.js 18 LTS as the supported build environment (`docs/RELEASE_NOTES.md`: "Node.js (web build) | 18+ (LTS)"; `README.md`: "Node.js 18+"). The newly committed `web/package-lock.json` resolves Vite to 8.0.16, whose engine requires `^20.19.0 || >=22.12.0`. Under the documented Node 18.20.8, `npm run build` aborts with the Vite Node-version error, so `scripts/build-release.sh` still cannot produce the dashboard bundle in a supported environment.
- **Reviewer-probed evidence** (captured in stdout before the final review comment):
  - `git show cdf08ed^:web/package.json` — all deps still pinned to `"latest"`, so `npm install` from scratch picks whatever the registry currently has as latest.
  - `git show cdf08ed:web/package-lock.json` — Vite 8.0.16, `@vitejs/plugin-react` 6.0.2 — both require Node 20.19+/22.12+.
  - `cd web && npx -y node@18 ./node_modules/vite/bin/vite.js build` — **exits 1** with "Vite requires Node.js version 20.19+ or 22.12+. Please upgrade your Node.js version" + `ReferenceError: CustomEvent is not defined`.
  - `cd web && npm run build` — exits 0 on Node v25.9.0 (reviewer's local Node); reviewer notes the build only succeeds on a non-documented environment.
- **Reviewer fix options**:
  1. Pin/regenerate the web lockfile to Node-18-compatible Vite/plugin versions (e.g. Vite 5.x line, which is the last Vite 18-compatible major).
  2. Raise the documented Node requirement to `20.19+` or `22.12+` in `README.md` and `docs/RELEASE_NOTES.md` as part of the same release change.
- **Why this is a real regression (team-lead verification)**:
  - Pre-R1 web was on `pnpm@9.0.0` with a tracked `web/pnpm-lock.yaml`; that lockfile (per R2 review) had resolved Vite to a Node-18-compatible version.
  - R2 replaced `pnpm-lock.yaml` (Node-18 compatible) with `package-lock.json` (regenerated with `npm install --package-lock-only` on Node v25.9.0 → Vite 8.x → Node 20.19+ requirement).
  - The build happy-path only worked in team-lead's verification because their local Node is v25.9.0; a CI runner or contributor on Node 18.20.8 will hit the engine error.
  - This was a real regression introduced by the F2-r2 fix's "regenerate lockfile from `latest`" step.

## 不在 review finding 范围但建议同时修的（不阻塞 D5 收口）

实施 agent 在 R2 fix 中把 R1 的 `if [ ! -d "$ROOT/web/node_modules" ]` 守护删了，F3-r2 修复已完成。Codex 没在 round 3 重复提 F3。`TestBuildReleaseAlwaysRunsNpmCi` 通过。✅

## 下一步

- D5 实施 agent 需要修 F1-r3。推荐方案：把 `web/package.json` 里的 `"latest"` 钉到 Node-18 兼容的具体版本（`vite: ^5.4.10`、`@vitejs/plugin-react: ^4.3.4`、`typescript: ^5.6.3`、`react: ^18.3.1`、`react-dom: ^18.3.1`、`@types/react: ^18.3.12`、`@types/react-dom: ^18.3.1`），然后 `cd web && rm package-lock.json && npm install --package-lock-only` 重新生成 lockfile，并加一个 `TestBuildReleaseLockfileEnginesCompatibleWithNode18` 回归测试。
- 修完跑 round 4 review。
- D5 R13 **未收口**：3 轮从 2 → 3 → 1 finding，趋势在收敛，但 F2-r2 仍未真正闭环。

---

## 修复结果（round-3 修复 commit `4cd80ec`）

按 test-first 流程修复：

### F1-r3 outcome — fixed (commit `4cd80ec`)

- **What changed** (web/package.json):
  - 钉 `vite: ^5.4.10`（resolved 5.4.21；Vite 5 是 Node 18 兼容的最后一个 major）
  - 钉 `@vitejs/plugin-react: ^4.3.4`
  - 钉 `react: ^18.3.1`、`react-dom: ^18.3.1`
  - 钉 `@types/react: ^18.3.12`、`@types/react-dom: ^18.3.1`
  - 钉 `typescript: ^5.6.3`
  - 删误导性 `packageManager: pnpm@9.0.0`（release 走 npm ci）
  - 加 `engines.node: ">=18.0.0"` 在源头上声明支持 Node 18

- **What changed** (web/package-lock.json):
  - `rm web/package-lock.json && cd web && npm install --package-lock-only --no-audit --no-fund` 重新生成
  - 977 行，lockfileVersion 3，vite resolved to 5.4.21
  - 不再含 `^20.19.0 || >=22.12.0` engines 约束（除合法 `^18 || >=20` 子句）

- **Test coverage** (`internal/buildrelease/safety_test.go`):
  - `TestBuildReleaseLockfileEnginesCompatibleWithNode18` 解析 clean `git archive HEAD` 提取的 `web/package-lock.json`，断言任何 package 的 `engines.node` 字段都必须允许 Node 18。test-first FAIL→PASS。
  - 多子句 `A || B` 约束按 npm 的 first-matching-clause 语义校验（任一子句允许 Node 18 即通过）。
  - 单子句 `>=X` / `^X` 当 X major >= 20 时报失败。

- **End-to-end 验证**:
  - `cd web && npm ci` → 68 packages in 2s, exit 0
  - `cd web && npm run build` → vite v5.4.21 build, 32 modules transformed, exit 0
  - Vite 5 line produces a 205 KB JS bundle (vs Vite 8's 254 KB) — Node 18 兼容的副产物

**Commit on `codex/v1-productization-d5-release`**: `4cd80ec`

可以启动 codex review round 4。
