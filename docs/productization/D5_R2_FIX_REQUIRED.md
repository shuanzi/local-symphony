# D5 Round 2 修复要求（team-lead → d5-release-packaging）

**日期**：2026-06-09
**状态**：⚠️ 修复待办（**R1 修复有 3 个未闭环 finding**）
**完整报告**：`docs/productization/D5_CODEX_REVIEW_ROUND2.md`

## 关键告警

你 R1 修复 commit `41dabb6` 自报 `OUT_DIR=$PWD bash scripts/build-release.sh` exit 2 + 5 个新测试 PASS。但 team-lead 亲自复现 **3 个未真正闭环的 finding**——**和你之前 R1 报告相反**：

| ID | Sev | 状态 | 问题 |
|---|-----|------|------|
| F1-r2 | P1 | **未真正闭环**（你 R1 矫枉过正） | guard 把"every child of $ROOT"全拒，包括脚本头部文档化的默认 `$ROOT/dist`——默认命令直接 exit 2 |
| F2-r2 | P1 | **未真正闭环**（你 R2 假修复） | `npm install` → `npm ci` 但 `web/package-lock.json` 在 `.gitignore` 第 9 行被排除，干净 checkout 找不到 lockfile，`npm ci` 立即失败 |
| F3-r2 | P2 | **新增** | `[ ! -d $ROOT/web/node_modules ]` 让已有 node_modules 时跳过 npm ci，stale 依赖被打包 |

**为什么你的 R1 验证没发现**：
- 你 R1 验证用 `OUT_DIR=$PWD`（**不**是默认 `$ROOT/dist`）
- 你工作 worktree 有 `web/package-lock.json` 残留（被 `.gitignore` 排除）—— 你的 `npm ci` 在你机器上能跑是因为有这个文件
- 你工作 worktree 有 `web/node_modules` 残留——F3 守护让你跳过 `npm ci`

**这和 D3 R3 panic 是同类问题**：工作机残留状态让实施 agent 误以为修复正确，CI 干净环境立刻暴露。

## 修复要求（按 review 建议，3 finding 联动修）

### F2-r2 + F3-r2 联动修

```sh
# 步骤 1: 从 .gitignore 删除 web/package-lock.json
sed -i '/^web\/package-lock\.json$/d' .gitignore
git add .gitignore

# 步骤 2: 在 web/ 下重新生成 lockfile（基于当前 web/package.json）
cd web && npm install --package-lock-only --no-audit --no-fund
cd ..
git add web/package-lock.json

# 步骤 3: 改 build-release.sh 移除 if [ ! -d ] 守护
# 旧：
#   if [ ! -d "$ROOT/web/node_modules" ]; then
#     (cd "$ROOT/web" && npm ci --no-audit --no-fund)
#   fi
# 新：
#   (cd "$ROOT/web" && npm ci --no-audit --no-fund)  # 永远跑
```

### F1-r2 收窄 guard

把"every child of $ROOT" 改为只拒**真正危险的源子目录**（`$ROOT/web` / `$ROOT/scripts` / `$ROOT/internal` / `$ROOT/cmd` / `$ROOT/docs` / `$ROOT/schemas`），**显式放行 `$ROOT/dist`**：

```sh
# 新 guard（推荐方案 A：exempt $ROOT/dist + 显式拒源子目录）
ROOT_RESOLVED=$(cd "$ROOT" && pwd -P)
OUT_DIR_RESOLVED=$(mkdir -p "$OUT_DIR" && cd "$OUT_DIR" && pwd -P)
if [ "$OUT_DIR_RESOLVED" = "$ROOT_RESOLVED" ]; then
    echo "[build-release] refusing to run: OUT_DIR equals ROOT" >&2
    exit 2
fi
# Default OUT_DIR=$ROOT/dist 是文档化的合法路径
if [ "$OUT_DIR_RESOLVED" = "$ROOT_RESOLVED/dist" ]; then
    : # 允许
else
    # OUT_DIR 是 ROOT 其他子目录时拒绝
    case "$OUT_DIR_RESOLVED" in
        "$ROOT_RESOLVED/web"|"$ROOT_RESOLVED/web"/*|\
        "$ROOT_RESOLVED/scripts"|"$ROOT_RESOLVED/scripts"/*|\
        "$ROOT_RESOLVED/internal"|"$ROOT_RESOLVED/internal"/*|\
        "$ROOT_RESOLVED/cmd"|"$ROOT_RESOLVED/cmd"/*|\
        "$ROOT_RESOLVED/docs"|"$ROOT_RESOLVED/docs"/*|\
        "$ROOT_RESOLVED/schemas"|"$ROOT_RESOLVED/schemas"/*|\
        "$ROOT_RESOLVED/api"|"$ROOT_RESOLVED/api"/*)
            echo "[build-release] refusing to run: OUT_DIR=$OUT_DIR_RESOLVED overlaps with source ROOT=$ROOT_RESOLVED" >&2
            exit 2
            ;;
    esac
fi
```

### 回归测试补充（3 个新测试）

```go
// safety_test.go
func TestBuildReleaseAcceptsDefaultOUTDir(t *testing.T) {
    // OUT_DIR=$ROOT/dist 必须通过 guard
}

func TestBuildReleaseAlwaysRunsNpmCi(t *testing.T) {
    // mock node_modules 存在的情况下，验证 npm ci 被调用
}

// 把现有 TestBuildReleaseLockfileStoryIsConsistent 改成：
//   - 在 git archive 提取的 clean tarball 下重跑
//   - 验证 web/package-lock.json 在 .gitignore 中不存在
//   - 验证 (cd web && npm ci) 能成功
```

## test-first 流程（按 D3 R3 panic 经验，必须做）

### 步骤 1：先写 3 个失败测试 + 验证 FAIL
- `TestBuildReleaseAcceptsDefaultOUTDir`（F1-r2 regression）
- `TestBuildReleaseAlwaysRunsNpmCi`（F3-r2 regression）
- 改造 `TestBuildReleaseLockfileStoryIsConsistent` 用 `git archive` clean tarball（F2-r2 regression）

**关键**：F2-r2 测试必须在 **clean git archive 提取的目录**下跑（用 `exec.Command("git", "archive", ...)` 提取到 t.TempDir()），不然你工作机的 lockfile 残留会掩盖问题。

### 步骤 2：跑测试，确认 FAIL

修复前必须看到 FAIL 输出（"OUT_DIR=$ROOT/dist refused" / "npm ci not called" / "package-lock.json missing"）。

### 步骤 3：按上面"修复要求"改

3 个 finding 联动改：F1-r2 收窄 guard + F2-r2 提交 lockfile + F3-r2 移除守护。

### 步骤 4：跑全量验证

```bash
# 关键：在干净 tarball 下验证（不能用工作机残留的 lockfile）
TMPDIR=$(mktemp -d)
git archive HEAD | tar -x -C "$TMPDIR"
cd "$TMPDIR"
bash scripts/build-release.sh
go test -count=1 -timeout 60s ./internal/buildrelease
go test -count=1 -timeout 60s ./...
python3 scripts/validate_contracts.py
bash scripts/acceptance-local.sh
cd - && rm -rf "$TMPDIR"
```

### 步骤 5：commit + 报告

修复 commit 落地 + 发信号 + 验证输出。**team-lead 会启动 codex review round 3**（针对这个 commit），预期 round 3 0 finding → D5 收口。

## 如果 mailbox 投递有问题

请主动检查 `/Users/xiquandai/Documents/code/local-symphony-d5-release/scripts/build-release.sh:40-41`：
- 如果 guard 仍拒 `$ROOT/dist` → 按本文件修
- 如果 `web/package-lock.json` 仍被 `.gitignore` 排除 → 按本文件修
- 如果 `if [ ! -d $ROOT/web/node_modules ]` 仍存在 → 按本文件修

完整 round 2 报告在 `docs/productization/D5_CODEX_REVIEW_ROUND2.md`。
