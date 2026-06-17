# D5 / R13 Codex Review — Round 2

- **Worktree**: `/Users/xiquandai/Documents/code/local-symphony-d5-release`
- **Commit under review**: `41dabb6` — D5 R1: fix F1 (OUT_DIR guard) and F2 (npm ci) per codex review
- **Base**: `main` (`f15ba39` at review time)
- **Reviewer**: `codex review` (Codex v0.130.0, model `gpt-5.5`, reasoning effort `xhigh`, sandbox `danger-full-access`, agent profile `~/.codex/agents/reviewer.toml`)
- **Command**:
  ```bash
  cd /Users/xiquandai/Documents/code/local-symphony-d5-release
  codex review --commit 41dabb6 --title "D5 R13: round 1 review fixes (F1 OUT_DIR guard, F2 npm ci)"
  ```
  > The `codex review` CLI rejects combining `--base <BRANCH>` with `--commit <SHA>` (exit 2). When reviewing a single commit, pass `--commit` only. The D5 commit is the only delta relative to `main`, so the diff is identical.
- **Timestamp**: 2026-06-09T13:30+0800
- **Exit code**: 0 (clean run, reviewer surfaced findings)
- **Full stdout/stderr log**: `docs/productization/D5_CODEX_REVIEW_ROUND2_RAW.txt` (2146 lines, 91.7 KB)

## Round 1 → Round 2 收敛

Round 1 (`573b1f0`) 找到 2 个 finding（F1 P1 + F2 P2），实施 agent 提交 `41dabb6` 修复。Round 2 在新 commit 上找到 3 个 finding：

- **F1（round 1 P1 OUT_DIR 风险）**：原始问题（破坏性 `rm -rf $OUT_DIR/web` 命中源 tree）已被修复（改用 scoped `rm -rf $OUT_DIR/web/dist`）。但 **guard 矫枉过正**：默认 `OUT_DIR=$ROOT/dist` 现在被新的 "every child of $ROOT" 检查拒绝，破坏文档化的默认命令。**仍需修复**（升级版 → F1-r2 P1）。
- **F2（round 1 P2 npm 锁文件）**：原始问题（`latest`-pin 非可复现）未真正解决 —— `npm ci` 已替换 `npm install`，但 `web/package-lock.json` 实际未被 git 跟踪（`.gitignore` 第 9 行排除了它），`web/pnpm-lock.yaml` 又被删了。`git archive` / 干净 checkout / CI 都会找不到 lockfile，`npm ci` 立即失败。**仍未修复**（升级版 → F2-r2 P1）。
- **新增 F3（round 2 P2）**：`[ ! -d $ROOT/web/node_modules ]` 守护让已有 `node_modules` 时跳过 `npm ci`，导致 stale 依赖被打包，release 不可复现。

总体来看，**0 finding 收敛** — 2 个原 finding 都未真正闭环，反而暴露了 1 个新 finding。

## 摘要

Codex 返回 **3 unique findings**（stdout 末尾被 assistant 重复 echo 一次）。

| ID | Severity | File | Status |
|---|---|---|---|
| F1-r2 | **P1** | `scripts/build-release.sh:40-41` | **仍未修复**（round 1 F1 矫枉过正） |
| F2-r2 | **P1** | `scripts/build-release.sh:63` | **仍未修复**（round 1 F2 表面修复、实际未闭环） |
| F3-r2 | **P2** | `scripts/build-release.sh:62-63` | **新增 finding** |

## F1-r2 [P1] Allow the documented default OUT_DIR

- **File**: `scripts/build-release.sh:40-41`
- **Reviewer claim**: When callers use the documented `bash scripts/build-release.sh` path, `OUT_DIR` defaults to `$ROOT/dist`; the new early-exit guard treats every child of `$ROOT` as an overlap and exits 2 before building. That breaks the normal release command and the layout described at the top of the script, even though the later deletion is now scoped to `$OUT_DIR/web/dist`.
- **Codex reproduction**:
  ```
  $ bash scripts/build-release.sh
  [build-release] refusing to run: OUT_DIR=/Users/xiquandai/Documents/code/local-symphony-d5-release/dist
                            overlaps with source ROOT=/Users/xiquandai/Documents/code/local-symphony-d5-release
  exit=2
  ```
- **Impact**: 默认命令直接失败；用户必须显式传 `OUT_DIR=/tmp/release` 才能用。
- **Reviewer fix suggestion**: allow the documented default — either by exempting `$ROOT/dist` (the canonical case the script's own header advertises) from the guard, or by computing `OUT_DIR` to a sibling before the guard runs.
- **Validation**: 与 round 1 实施 agent 的 `OUT_DIR=$PWD` 验证冲突 —— 实施 agent 只验证了"用户显式给 `OUT_DIR=$PWD` 时拒绝"，没有验证"默认 `OUT_DIR=$ROOT/dist` 是否被误拒"。**未闭环**。

## F2-r2 [P1] Commit the lockfile required by npm ci

- **File**: `scripts/build-release.sh:63`
- **Reviewer claim**: In a clean checkout of this commit, `web/package-lock.json` is not present or tracked while the only tracked lockfile (`web/pnpm-lock.yaml`) was deleted, so the new `npm ci` call fails immediately with npm's "can only install with an existing package-lock.json" error whenever `node_modules` is absent. The ignored local package-lock in a developer workspace can mask this, but clean release builds and the new buildrelease test fail until the npm lockfile is actually committed or the script keeps using a tracked lockfile.
- **Codex reproduction**:
  ```
  $ git archive 41dabb6 | tar -x -C "$tmp"
  $ (cd "$tmp/web" && npm ci --no-audit --no-fund)
  npm error code EUSAGE
  npm error The `npm ci` command can only install with an existing package-lock.json or
  npm error npm-shrinkwrap.json with lockfileVersion >= 1.
  exit=1
  ```
  以及 buildrelease 单测：
  ```
  $ git archive 41dabb6 | tar -x -C "$tmp"
  $ (cd "$tmp" && go test ./internal/buildrelease)
  --- FAIL: TestBuildReleaseLockfileStoryIsConsistent (0.00s)
      safety_test.go:239: web/package-lock.json missing; `npm ci` cannot run.
  FAIL
  ```
- **Root cause**: `.gitignore` 第 9 行 `web/package-lock.json` 让 lockfile 永远不会被 git 跟踪。`pnpm-lock.yaml` 被删后，仓库里不存在任何 lockfile，`npm ci` 找不到锁。
- **Impact**: 干净 checkout / CI / release 流水线全部跑不通 `npm ci` 步骤；`TestBuildReleaseLockfileStoryIsConsistent` 在干净 tar 包里直接 FAIL。
- **Reviewer fix suggestion**: commit a current `web/package-lock.json` (remove the `web/package-lock.json` gitignore line, regenerate / check in a lockfile that matches `web/package.json`), OR keep using a tracked lockfile by going back to `pnpm install --frozen-lockfile` against the now-deleted `pnpm-lock.yaml` (revert its deletion) so the script always has a real tracked lockfile to use.

## F3-r2 [P2] Run npm ci even when node_modules exists

- **File**: `scripts/build-release.sh:62-63`
- **Reviewer claim**: When a developer already has `web/node_modules` from a previous `npm install`/`pnpm install` or older lockfile, the existing `if [ ! -d "$ROOT/web/node_modules" ]` guard skips the frozen install entirely and `npm run build` uses whatever dependencies happen to be on disk. That leaves release artifacts non-reproducible in the common non-clean workspace case; `npm ci` should be the step that recreates `node_modules` from the lockfile for release builds.
- **Impact**: 只要开发者机器上之前有 `web/node_modules`，release 仍然会基于磁盘上的依赖打包（这些依赖可能来自旧的 `pnpm install` 解析的 `latest`），与 F2 同根 —— release 不可复现。
- **Reviewer fix suggestion**: drop the `if [ ! -d ... ]` cache check and unconditionally run `npm ci` so the lockfile is the single source of truth for release builds.

## 修复建议（给 D5 实施 agent）

下一轮必须解决 3 个 finding。建议顺序：

1. **F2-r2 + F3-r2 联动修**：在 `web/` 下重新生成 `web/package-lock.json`（基于当前 `web/package.json` 的 `latest` 解析），从 `.gitignore` 删除 `web/package-lock.json` 那一行，提交；同时移除 `if [ ! -d $ROOT/web/node_modules ]` 守护，让 `npm ci` 永远跑。这两步一起做才能闭环 F2 + F3。
2. **F1-r2 收窄 guard**：guard 改成"只在 `$OUT_DIR == $ROOT` 或 `$OUT_DIR` 是 `$ROOT/web` / `$ROOT/scripts` / `$ROOT/internal` / `$ROOT/cmd` / `$ROOT/docs` 等源子目录时拒绝"；显式放行 `$ROOT/dist`（脚本头部描述的标准输出位置）。或者：默认 `OUT_DIR` 改为 `$ROOT/../dist`（源 tree 的 sibling），与文档化路径解耦。
3. **回归测试补充**：
   - `TestBuildReleaseAcceptsDefaultOUTDir`（`OUT_DIR=$ROOT/dist` 必须通过 guard）
   - `TestBuildReleaseAlwaysRunsNpmCi`（mock `node_modules` 存在的情况下，验证 `npm ci` 被调用）
   - 把现有的 `TestBuildReleaseLockfileStoryIsConsistent` 在 clean tarball 下重跑（用 `git archive` 路径）

## 下一步

将 finding 列表转发给 `d5-release-packaging@v1-phase-d-productization` 实施 agent，等待 round 1 + round 2 共 5 个 finding 全部修复后跑 round 3 review。

---

## 修复结果（round-2 修复 commit `cdf08ed` + fallback commit `9d1e57d`）

按 test-first 流程全部修复，3 个 finding 联动：

### F1-r2 outcome — fixed (commit `cdf08ed`)

- **What changed**: 收窄 `scripts/build-release.sh` 的 OUT_DIR guard。
  - 显式放行 `$ROOT/dist`（脚本头部描述的标准输出位置）
  - 显式拒 `$ROOT`（等号）
  - 显式拒 `$ROOT/{web,scripts,internal,cmd,docs,schemas,api,examples,tests,db}` 及其子树
  - 其他任何位置（sibling of $ROOT）继续允许
- **Test coverage**:
  - `TestBuildReleaseAcceptsDefaultOUTDir`（`$ROOT/dist` 必须通过）— test-first FAIL→PASS
  - `TestBuildReleaseRejectsOUTDirEqualToRoot`（$ROOT 等号仍拒）— 仍 PASS
  - `TestBuildReleaseRejectsOUTDirUnderRoot`（$ROOT/web 子树拒）— 更新后 test-first FAIL→PASS
  - `TestBuildReleaseDoesNotBlanketDeleteOUTDirWeb`（scoped rm-rf 仍保护 sibling 目录）— 仍 PASS
- **Re-verification**:
  - `bash scripts/build-release.sh` 默认路径 exit 0
  - `OUT_DIR=$PWD/internal/foo` exit 2（仍拒源子目录）
  - `OUT_DIR=$PWD` exit 2（仍拒 ROOT 等号）
  - `OUT_DIR=/tmp/should-allow` exit 0

### F2-r2 outcome — fixed (commit `cdf08ed`)

- **What changed**:
  - 从 `.gitignore` 删 `web/package-lock.json` 那行
  - 重新生成 `web/package-lock.json`（基于 `web/package.json` 的 `latest` 解析）
  - 提交 `web/package-lock.json`（972 行，lockfileVersion 3）
- **Test coverage**:
  - `TestBuildReleaseLockfileStoryIsConsistent` 重写为基于 `git archive HEAD` 的 clean extract 校验：clean 环境中 `web/package-lock.json` 必须存在；`web/pnpm-lock.yaml` 必须不存在；`.gitignore` 不能排除 `web/package-lock.json`。test-first FAIL→PASS。
  - `extractCleanTarball` 在非 git worktree（`git archive` 不可用）时回退到 in-place repoRoot 并打 t.Logf；这让该测试在 clean-tarball 验证里也能跑（fallback 在 commit `9d1e57d`）。
- **Re-verification**: clean tarball extract 含 `web/package-lock.json`（972 行）。

### F3-r2 outcome — fixed (commit `cdf08ed`)

- **What changed**: 删 `scripts/build-release.sh` 的 `if [ ! -d "$ROOT/web/node_modules" ]` 守护，`npm ci` 现在永远跑。
- **Test coverage**: `TestBuildReleaseAlwaysRunsNpmCi` 在 pre-existing `web/node_modules` 情况下用 logging `npm` stub 验证 `npm ci` 是被调用的 argv。test-first FAIL→PASS。

### 干净 tarball 端到端验证

```bash
$ TMPDIR=$(mktemp -d)
$ git archive HEAD | tar -x -C "$TMPDIR"
$ cd "$TMPDIR"
$ bash scripts/build-release.sh        # 默认 OUT_DIR=$ROOT/dist exit 0
$ go test -count=1 -timeout 60s ./internal/buildrelease   # 7 个测试全 PASS
$ python3 scripts/validate_contracts.py   # contract validation passed
$ bash scripts/acceptance-local.sh        # acceptance-local passed
```

**Commits on `codex/v1-productization-d5-release`**:
- `cdf08ed` — D5 R2: fix F1-r2/F2-r2/F3-r2 per codex review round 2
- `9d1e57d` — D5 R2: skip git archive when not in a git worktree (测试 fallback)

可以启动 codex review round 3。
