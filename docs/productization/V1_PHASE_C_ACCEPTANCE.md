# 阶段 C 验收记录

**状态**：阶段 C v1.1 WIP 收口（**C1+C2 完成 + C3 v1.1 WIP + C4 v1.1 WIP**），等待 PR 合并
**更新日期**：2026-06-08
**覆盖阶段**：C（Daemon 产品行为补齐）

## 阶段 C 收口 PR

| 子包 | PR | 分支 | 状态 |
|---|---|---|---|
| C3 owner nonce/heartbeat v1.1 WIP | **[#19](https://github.com/shuanzi/local-symphony/pull/19)** | `pr/c3-owner-nonce-v1-1-wip` | open, waiting review |
| C4 CLI over REST v1.1 WIP | **[#20](https://github.com/shuanzi/local-symphony/pull/20)** | `pr/c4-cli-rest-v1-1-wip` | open, waiting review |

两个 PR **不可相互依赖**——C3 与 C4 改动无交叉文件冲突。

## 整体进度

阶段 C 下设 4 个工作包：

- [x] **C1 hook lifecycle**：hook adapter 接 lifecycle event，codex_review 已合并。
- [x] **C2 scheduler tick loop**：scheduler tick + 串行 in-flight dispatch，codex_review 已合并。
- [~] **C3 single daemon ownership / runtime lock**：app DB `runtime_descriptors` owner guard 已实现并合并；**v1.1 WIP**：owner nonce/heartbeat 数据层完整 + 5 轮 codex review 充分；协调层 1 P1 留作 C5 daemon lifecycle design problem。**PR #19 open**。
- [~] **C4 CLI over REST 与 daemon session 对齐**：12 commits（含 5 轮 codex review + 6 轮 adversarial review + 1 docs）实施完整；**v1.1 WIP**：trust 边界专项（fail-open 模式反复在 `repo_root` guard 路径重犯 + validation failure vs missing file 区分不足）作为 v1.1 收口后续。**PR #20 open**。

## C4 v1.1 WIP

C4 是阶段 C 的最后一个工作包。CLI over REST 与 daemon session 对齐已通过 9 轮 codex review + 6 轮 adversarial review 收口，C4 内部不再开 round 7，进入 v1.1 WIP 路线。

### C4 commit 链（12 commits，HEAD `b9af4a6`）

| Commit | 摘要 |
|---|---|
| `83e5e44` | C4: CLI over REST + daemon session alignment |
| `7cb9291` | C4 codex review: auth bypass fix + array decode + workflow POST + URL query + login UX |
| `095b946` | C4 codex review 2: login probe + mutating retry + legacy logout |
| `88a66c3` | C4 codex review 3: err wrap + login exit 7 + acceptance poll + fallback schema |
| `97c2b0a` | C4 codex review 4: mark workflow validate as non-mutating |
| `5a7f5bd` | C4 adversarial: project_id guard + ErrSessionMissing wrap + logout revoke |
| `f358715` | C4 adversarial round 2: loopback guard + degraded logout + revoke envelope |
| `7f93187` | C4 adversarial round 3: logoutRevokeFromFile via Discover + project_id guard |
| `eec5c28` | C4 adversarial round 4: session repo_root guard + legacy logout scoping |
| `7fa5a08` | C4 adversarial round 5: openDescriptor trust chain + logout per-source + repo_root propagation |
| `b9af4a6` | C4 adversarial round 6: fail-closed repo_root + sticky project-scoped logout |
| `b9af4a6+` (本批) | docs: mark C4 as v1.1 WIP, list 9 commits + finding table + trust-boundary known limit |

### 累计 finding 表（0 critical / 4 important / 18 minor）

| 轮次 | 级别 | finding | 状态 |
|---|---|---|---|
| codex review 1 | important | auth bypass | fixed (`7cb9291`) |
| codex review 1 | minor ×4 | array decode / workflow GET→POST / URL query / login UX | fixed |
| codex review 2 | minor ×3 | login probe / mutating retry / legacy logout | fixed (`095b946`) |
| codex review 3 | minor ×4 | err wrap / login exit 7 / acceptance poll / fallback schema | fixed (`88a66c3`) |
| codex review 4 | minor | workflow validate 未标 non-mutating | fixed (`97c2b0a`) |
| adversarial round 1 | important ×2 | project_id guard / logout revoke | fixed (`5a7f5bd`) |
| adversarial round 2 | minor ×3 | loopback guard / degraded logout / revoke envelope | fixed (`f358715`) |
| adversarial round 3 | important | logoutRevokeFromFile 不走 Discover | fixed (`7f93187`) |
| adversarial round 3 | minor | project_id guard 在 logout 路径漏写 | fixed |
| adversarial round 4 | important | session repo_root guard 漏 fail-open | fixed (`eec5c28`) |
| adversarial round 4 | minor | legacy logout 不按 project_id scoping | fixed |
| adversarial round 5 | important ×2 | openDescriptor 信任链 / per-source degrade | fixed (`7fa5a08`) |
| adversarial round 5 | minor | `loginResolveProject` 不传播 repo_root | fixed |
| adversarial round 6 | important ×2 | EvalSymlinks fail-open / logout 删 unvalidated | fixed (`b9af4a6`) |

### 已知限制（指向 C5 trust 边界专项 / v1.1 收口）

1. **fail-open 模式反复在 `repo_root` guard 路径重犯**：round 4 在 `internal/daemonclient/session.go` 修过一次（注释甚至写明"can't normalise; don't block on a host-side issue"），round 6 又在同文件 + 镜像 `internal/cli/cli.go` 的 `checkCLISessionRepoRoot` 修第二次。fail-open 注释仍是 repo_root 路径的明显反模式。
2. **validation failure vs missing file 区分不足**：`logoutRevokeFromFile` round 5 把所有非 `IsNotExist` 错误归为同一 `usable=false` 桶，round 6 才发现这导致 "validation 失败时 project-scoped 文件被错误删除"。`loadCLISessionToken` / `readCLISessionToken` / `openDescriptor` 同样存在一刀切。
3. **镜像检查未去重**：`daemonclient` 和 `cli` 两个包各自有 `checkSessionRepoRoot` / `checkCLISessionRepoRoot` + `normaliseRepoRootForCompare`，修改时必须同时改两边（已 round 6 验证）。
4. **project_id 不匹配的可观测性**：`ReadSessionFile` 在 project_id 不匹配时只返 `ErrUnauthorized`，operator 看到的是"session not valid for this project"——但不能区分 copied-DB / stale session / foreign-bearer-attempt。

### C5 trust 边界专项（v1.1 收口工作）

- 抽 `internal/trustboundary` 共享包：`CheckRepoRoot(persisted, caller) error`、`CheckProjectID(persisted, caller) error`、`CheckAPIURL(persisted, expected) error`，统一 fail-closed。
- 重构 `internal/daemonclient/session.go` 和 `internal/cli/cli.go` 的镜像检查为单一调用。
- 改 `ReadSessionFile` / `loadCLISessionToken` / `logoutRevokeFromFile` 的错误类型为 `{usable, validationFailed, degraded, err}` 四元组。
- 在 `cli` 错误 envelope 加 `validation_failure_kind` 字段，让 operator 区分 copied-DB / stale session / foreign-bearer-attempt。
- 端到端覆盖：copied-DB + 原始 checkout 删除、copied-DB + 原始 checkout 移动、API URL 错指 + 探测成功、API URL 非 loopback、API URL 指向 wrong project daemon。

### 当前 C4 已 ship 能力（v1 范围内已可用）

- `symphony status` / `issue *` / `run *` / `approval *` / `review *` / `workflow *` / `diagnostics *` 等 operator 命令在 daemon 可用时走 REST；不可用时降级 local store。
- CLI bearer session 经 loopback + project_id + repo_root 三重 fail-closed 校验。
- `symphony login --logout` 走 Discover /health project_id 守卫，per-source degraded 状态 sticky，project-scoped validation 失败时文件保留。
- `symphony open` 通过 Discover 信任链避免误送 bearer 到 foreign daemon。
- exit code 严格按 `invalid_request=2` / `workflow_or_prompt=9` / `其余操作冲突=7` / `daemon_unavailable=7`。

## 阶段 C 收口门禁

```bash
go test ./internal/app ./internal/orchestrator ./internal/cli ./internal/httpapi ./internal/db ./internal/store ./internal/workspace
bash scripts/acceptance-local.sh
python3 scripts/validate_contracts.py
```

C4 round 6 (`b9af4a6`) 验证：
- `go test -count=1 -timeout 120s ./internal/cli ./internal/daemonclient ./internal/httpapi ./internal/security` 全部通过
- `python3 scripts/validate_contracts.py` 通过
- `bash scripts/acceptance-local.sh` 通过
