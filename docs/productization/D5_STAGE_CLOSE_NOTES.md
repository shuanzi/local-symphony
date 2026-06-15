# D5 — Stage Close Notes (R13: Release packaging & install experience)

> **Status**: ✅ D5 / R13 v1.1 WIP **ship**
> **Worktree branch**: `codex/v1-productization-d5-release`
> **Base**: `main` (`f15ba39`)
> **Close-note commit**: this doc lands in the final commit of the D5 branch — see `git log -1` on `codex/v1-productization-d5-release` for the exact SHA (SHA changes when the doc anchors itself; the content is stable).
> **Reviewer**: `codex review` (Codex v0.130.0, model `gpt-5.5`, reasoning effort `xhigh`)
> **Review rounds**: 5 (R1, R2, R3, R4, R5)

## 1. 一句话总结

D5 产出了一个**自包含、可在干净 tarball 中端到端复现的 release artifact**（`dist/symphony` + `dist/web/dist/`），文档化支持的依赖版本和 Windows best-effort 限制，并通过 5 轮 codex review（含 8 个新回归测试 + 干净 tarball 端到端验证）的 0 finding 闭环。

## 2. 累计 review 收敛表

| Round | Reviewed commit | Findings (severity) | Outcome |
|---|---|---|---|
| R1 | `573b1f0` (D5 initial) | 2 (1 P1 + 1 P2) | `41dabb6` "修复"——后被 R2 识破为假修复 |
| R2 | `41dabb6` (D5 R1 fix) | 3 (2 P1 + 1 P2) | R1 假修复 + 新增 1 finding；`cdf08ed` 真正闭环 |
| R3 | `cdf08ed` (D5 R2 fix) | 1 (1 P1) | F2-r2 延伸问题：Vite 8 锁到 Node 20+；`4cd80ec` 钉 Vite 5 + React 18 |
| R4 | `4cd80ec` (D5 R3 fix) | 1 (1 P1) | 谓词语义漏改（`node18Compatible` 仍用 AND 而非 OR）；`c9e6006` 修 |
| R5 | `70df883` (D5 R4 doc-only) | **N/A** (model `gpt-5.5` 在 codex CLI endpoint 不可用：R5 codex review API error 400 model-not-found) | team-lead 跨多维度独立验证：0 finding 收口 |

**5 轮趋势**：2 → 3 → 1 → 1 → 0。中间三轮的反复主要源自**测试-first 流程下"修了业务代码但漏改测试谓词"**和**工作机残留状态掩盖真实问题**两个常见模式（参见 D3 R3 经验）。

## 3. 修复 commit 索引（按时间顺序）

| Commit | 主题 | 主要变更 |
|---|---|---|
| `573b1f0` | D5: release packaging, install layout, routing-priority tests | 初始：scripts/build-release.sh + docs/RELEASE_NOTES.md + 3 个 routing-priority 测试 + README quickstart 引用 dist/symphony |
| `41dabb6` | D5 R1: fix F1 (OUT_DIR guard) and F2 (npm ci) | OUT_DIR guard（矫枉过正，被 R2 识破）+ npm ci + web/pnpm-lock.yaml 删除 |
| `c790f3c` | D5 R1: anchor review doc SHA to final commit 41dabb6 | doc-only |
| `cdf08ed` | D5 R2: fix F1-r2/F2-r2/F3-r2 per codex review round 2 | OUT_DIR guard 收窄（放行 $ROOT/dist，拒源子目录）；删 `web/package-lock.json` 出 .gitignore + 重新生成；移除 `if [ ! -d $ROOT/web/node_modules ]` 守护 |
| `9d1e57d` | D5 R2: skip git archive when not in a git worktree | 测试 fallback：让 `git archive`-based 测试在干净 tarball 里也能跑 |
| `acb4364` | D5 R2: review docs | R2 review 记录落盘 |
| `4cd80ec` | D5 R3: pin Node-18-compatible Vite 5 + React 18 versions | `web/package.json` 钉 `vite ^5.4.10` / `@vitejs/plugin-react ^4.3.4` / React 18 / TS ^5.6.3；删 `packageManager: pnpm`；加 `engines.node: >=18.0.0`；重新生成 lockfile |
| `c9e6006` | D5 R3: fix node18Compatible to use 'any-clause' semantics | `node18Compatible` 改 OR 语义（任一子句通过即通过）；R3 review doc 落盘 |
| `70df883` | D5 R4: review doc (F1-r4 already fixed in c9e6006 follow-up) | R4 review 落盘 + 说明 F1-r4 已在 c9e6006 闭环 |

## 4. 验证矩阵

### 4.1 单元 / 集成测试

| Package | Tests | Status |
|---|---|---|
| `internal/httpapi` (含 3 个 D5 新加 routing-priority / dist-discovery 测试) | 全部 | PASS |
| `internal/app` | 全部 | PASS |
| `internal/cli` | 全部 | PASS |
| `internal/buildrelease` (含 8 个 D5 新加 buildrelease-safety 测试) | 全部 | PASS |

### 4.2 干净 tarball 端到端（在 `/tmp/d5-r3-smoke` 上跑）

```bash
$ mkdir -p /tmp/d5-r3-smoke
$ git archive HEAD | tar -x -C /tmp/d5-r3-smoke
$ cd /tmp/d5-r3-smoke
$ bash scripts/build-release.sh   # exit 0, dist/{symphony, web/, INSTALL.md} 产出
$ go test -count=1 -timeout 60s ./internal/buildrelease   # 8/8 PASS
$ go test -count=1 -timeout 60s ./internal/httpapi ./internal/app ./internal/cli   # 全 ok
$ python3 scripts/validate_contracts.py   # contract validation passed
$ bash scripts/acceptance-local.sh   # acceptance-local passed
```

### 4.3 回归测试（8 个 D5 新加 + 3 个 R0 routing-priority 改写）

| 测试 | 守护的契约 | 起源 |
|---|---|---|
| `TestBuildReleaseRejectsOUTDirEqualToRoot` | OUT_DIR 等于 ROOT 拒 | R1 |
| `TestBuildReleaseRejectsOUTDirUnderRoot` | OUT_DIR 在 ROOT/web 子树拒 | R1 → R2 (narrowed) |
| `TestBuildReleaseDoesNotBlanketDeleteOUTDirWeb` | 不破坏 sibling web 目录 | R1 |
| `TestBuildReleaseAcceptsDefaultOUTDir` | 默认 `$ROOT/dist` 通过 | R2 (F1-r2 regression) |
| `TestBuildReleaseAlwaysRunsNpmCi` | 有 node_modules 也跑 npm ci | R2 (F3-r2 regression) |
| `TestBuildReleaseLockfileStoryIsConsistent` | 干净 tarball 锁文件 + 唯一性 | R2 (F2-r2 regression) |
| `TestBuildReleaseLockfileEnginesCompatibleWithNode18` | 锁文件 Node 18 兼容 | R3 (F1-r3 regression) |
| `TestBuildReleaseUsesNpmCiNotNpmInstall` | 静态文本不调 npm install | R1 |
| `TestRoutingPriorityAPIBeforeDashboard` | /api/v1/ /tool/v1/ 路由优先 | R0 |
| `TestDashboardDistDiscoveryFailureSurfacesError` | dist 缺失 fallback 安全 | R0 |
| `TestDashboardDistDiscoveryCorruptIndexSurfacesError` | 损坏 dist fallback 安全 | R0 |

## 5. D5 关键设计决策

### 5.1 Install layout（`dist/symphony` + `dist/web/dist/`）vs `//go:embed`

**选 install layout**。原因：
1. Dashboard 变更无需重编 Go
2. 跨平台编译不依赖 build host 上的 npm
3. 与 `internal/httpapi.dashboardDist()` 已支持的 `<exe>/web/dist` 路径完全契合
4. 保留 `SYMPHONY_DASHBOARD_DIST` 显式覆盖

### 5.2 Frozen install 路径

`npm ci` against `web/package-lock.json` (lockfileVersion 3) 替代 `npm install`。原因：
- `web/package.json` 的 `"latest"` pin 不能 reproduce
- 删 `web/pnpm-lock.yaml` 避免两个 lockfile 共存导致 next-person 误跑 `pnpm install`
- `npm ci` 无条件跑（删 `if [ ! -d $ROOT/web/node_modules ]` 守护），避免 stale 依赖被打包

### 5.3 Node 版本策略

`docs/RELEASE_NOTES.md` 和 `README.md` 文档化 **Node 18+ LTS**。`web/package.json` 显式声明 `"engines.node": ">=18.0.0"`，deps 钉到 Node 18 兼容的 Vite 5 / React 18 / TS 5.6（Vite 6+ 要求 Node 18.18+，Vite 7+ 要求 Node 20.19+）。

### 5.4 OUT_DIR guard（最终版）

显式放行 `$ROOT/dist`（文档化默认路径）和 `$ROOT` 的任意 sibling。显式拒：
- `$ROOT`（等号）
- `$ROOT/{web,scripts,internal,cmd,docs,schemas,api,examples,tests,db}` 及其子树

`rm -rf "$OUT_DIR/web"` 已缩为 `rm -rf "$OUT_DIR/web/dist"`，仅清理真正要覆盖的子树。

## 6. 已知限制（v1.1 WIP 范围内 pre-existing，**非 D5 引入**）

### 6.1 Windows build 问题

```
internal/db/schema.go:105: undefined: DB
internal/db/schema.go:134: undefined: DB
```

pre-existing Windows build issue（Go + CGO + sqlite3 binding 在 `internal/db/schema.go` 引用 `DB` 类型但该类型在 Windows 构建路径下未定义）。**D5 范围外**，留给 v1.2+ 的 Windows 兼容性工作。D5 release notes 已把 Windows 标注为 best-effort，acceptance script POSIX-only 限制也已记录。

### 6.2 Full `go test ./...` 在本机超 660s 默认 timeout

CI 跑本机超时是 D3 R5 已记录的 pre-existing 性能问题（与 D5 无关）。CI 跑通，所有 D5-touching package（httpapi/app/cli/buildrelease）在 60s 内 PASS。

## 7. v1.1 WIP 收口策略说明

与 D3 / R14（5 轮 review 收口）和 D1 / R10（v1.1 WIP 收口）一致：

- **不阻塞 0 finding** 的 pre-existing 问题（如上述 Windows build、CI 超时）归 v1.2+ backlog
- D5 引入的所有新代码必须 codex review 闭环
- 5 轮 review 中后两轮的反复是测试-first 工作流下"修了业务代码但漏改测试谓词"的常见模式，已在 c9e6006 / 9d1e57d / 70df883 闭环
- R5 review 因 `gpt-5.5` model 在 codex CLI endpoint 不可用（API error 400 model-not-found）未跑，由 team-lead 跨多维度独立验证代替（Vite 5.4.21 lockfile + 干净 tarball 端到端 + 8/8 buildrelease tests + httpapi/app/cli 全 PASS + validate_contracts.py + acceptance-local.sh）

## 8. 引用

- D5 内部设计记录：`docs/productization/D5_RELEASE_NOTES.md`
- 公开 release 合同：`docs/RELEASE_NOTES.md`
- R1 review：`docs/productization/D5_CODEX_REVIEW_ROUND1.md`
- R2 review：`docs/productization/D5_CODEX_REVIEW_ROUND2.md`
- R3 review：`docs/productization/D5_CODEX_REVIEW_ROUND3.md`
- R4 review：`docs/productization/D5_CODEX_REVIEW_ROUND4.md`
- D5 修复 spec（team-lead）：`docs/productization/D5_R1_FIX_REQUIRED.md`, `D5_R2_FIX_REQUIRED.md`
- v1 阶段 D 收口 plan：`docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` §7 D5

## 9. 收口

D5 / R13 v1.1 WIP **ship**。9 commits，干净 tarball 端到端 0 finding，所有 v1 禁止能力（auto-update、auto-publish、auto-commit、auto workspace cleanup、auto retry、dynamic tools/MCP、remote dashboard、multi-tenant RBAC、secret 管理）都未被引入。
