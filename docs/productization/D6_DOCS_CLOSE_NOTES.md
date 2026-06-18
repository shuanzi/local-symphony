# D6 / R15 — 文档、合同与验收对齐（阶段收口）

**日期**：2026-06-09
**对应计划**：`docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` §7 阶段 D / D6
**worktree 分支**：`codex/v1-productization-d6-docs`
**基线**：`main` @ `f15ba39`（v1.1 WIP 收口）
**阶段状态**：**v1.1 WIP** —— D1 主体 + R1 修复 + R2 review 已收口（原 1 P2 已修：ReviewPacketArtifact.kind enum 已补全）；D3 / R14 已把 Codex availability preflight 接到 diagnostics/status/state surface（`observability.CodexAvailability(...)` / `Diagnostics` 调 `codex.RunPreflight(...)`）；D5 / R13 已随当前 `main` 合入，当前 checkout 包含 release script 与 `web/package-lock.json`；D2 / D4 准备中；D6 / R15 文档合同收口（本批）。

## 1. R 项 status note 表

| R | 名称 | 阶段 | 状态 | 实施 commit | 依据 / 备注 |
|---|---|---|---|---|---|
| R1 | Runner 抽象 | A | ✅ A 完成 | `2cc1888` 之前 | fake runner / Codex adapter 同一接口 |
| R2 | Codex fixture gate | A | ✅ A 完成 | D3 链（分支 `codex/v1-productization-d3-codex-availability`）起 | `docs/codex/FIXTURE_POLICY.md` 锁定 |
| R3 | Codex process lifecycle | A | ✅ A 完成 | D3 链 | launch / timeout / redacted stderr |
| R4 | Codex 事件归一化 | A | ✅ A 完成 | D3 链 | normalized timeline |
| R5 | Approval API + writeback | B | ✅ B 完成 | B1 链 | stage-B command/file/network approvals |
| R6 | Missing handoff continuation | A | ✅ A 完成 | A5 链 | one continuation then `missing_handoff` |
| R7 | Hook lifecycle | C1 | ✅ C1 完成 | C1 链 | `symphony hook` ready/run/cleanup/wait/finish/cancel |
| R8 | Scheduler tick + runtime lock | C2+C3 | ✅ C2 完成 / 🟡 C3 v1.1 WIP | `dbf4c6e` … `d8840d2` | C3 数据层 5 轮 review 0 finding；协调层 shutdown-ordering 1 P1 留作 C5 daemon lifecycle |
| R9 | CLI over REST + daemon session | C4 | 🟡 C4 v1.1 WIP | C4 链见 `docs/productization/V1_PHASE_C_ACCEPTANCE.md` §5.1；已知限制见 §5.3 | 12 commits（5 轮 codex + 6 轮 adversarial + 1 docs）；trust 边界专项 4 项见 §3 已知限制 |
| R10 | Review Packet API | D1 | 🟡 D1 进行中 | `2cc1888` + R1 `0aee74c` | 主体 + R1 修复落地；R2 review 已收口（原 1 P2 已修，见 §3.1） |
| R11 | Dashboard 产品化补齐 | D2 | ⏳ D2 准备中 | — | 待启动 |
| R12 | 安全策略执行 | B3 | ✅ B3 完成 | B3 链 | secret/best-effort/loopback/CSRF/Tool Gateway scope |
| R13 | Release packaging | D5 | ✅ D5 已合入当前 main | `573b1f0` + R1 `41dabb6` + R2 `cdf08ed` + follow-ups | release script、`web/package-lock.json` 与 release notes 已随当前 main 合入；顺带发现 Windows build issue（pre-existing, `internal/db/schema.go:105/134`），归 D5 / R13 范围 |
| R14 | Codex availability diagnostics | D3 | 🟡 D3 部分完成 / R14 后续补齐 | 分支 `codex/v1-productization-d3-codex-availability`（5 轮 review） | diagnostics/status/state surface 字段已接入：`CodexAvailability(...)` / `Diagnostics` 调 `codex.RunPreflight(...)`，有 supported fixture 时报真实 version/support/available，无 fixture 时 `available=false` + `warning=unsupported_codex_version` |
| R15 | 文档合同 | D6 | 🟡 D6 进行中 | 本批 | 本批文档收口 |
| R16 | Rework prompt 上下文 | D4 | ⏳ D4 准备中 | — | 待启动 |
| R17 | DB schema guard | A0 | ✅ A0 完成 | A0 链 | `internal/db.MigrateAppSchema` idempotent |

## 2. 文档变更清单

| 文件 | 状态 | 范围 |
|---|---|---|
| `README.md` | M | §1 / §14 状态指针；真实 Codex 不运行段对齐 D3 / R14 已接 preflight 的 diagnostics/status/state projection |
| `PRD.md` | M | 头部加 v1 阶段 D 收口状态元数据；§8.4 Codex Runner 段加 D3 / D5 状态注 |
| `TECH_SPEC.md` | M | 头部加 v1 阶段 D 收口状态元数据；§10 Codex adapter 段对齐 D3 / R14 已接 preflight 的 diagnostics/status/state projection；末尾加 D-Phase Close Summary 指针 |
| `docs/codex/ADAPTER_MAPPING.md` | M | 头部加 D3 / R14 5 轮 review 收口一致状态 |
| `docs/codex/FIXTURE_POLICY.md` | M | CI 段补 D3 / R14 preflight 状态；D5 / R13 release packaging 不在本 policy 声明 |
| `docs/testing/ACCEPTANCE.md` | M | A0–A10 acceptance 分类标签化（fake / 安全回归 / real Codex opt-in）；顶部加分类总览表 |
| `docs/productization/V1_PHASE_C_ACCEPTANCE.md` | M | refs/状态校准（D6 follow-up review，commit `13230ec`） |
| `docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` | M | refs/状态校准（D6 follow-up review，commit `13230ec`） |
| `docs/testing/CONTRACT_VALIDATION_MANIFEST.json` | M | 测试合同 manifest 对齐（D6 follow-up review，commit `c2cf88d`） |
| `docs/productization/D6_DOCS_CLOSE_NOTES.md` | A | 本文档（R15 主交付物） |

## 3. 已知限制

### 3.1 D1 / R10 — R2 review 已收口（原 1 P2 已修）

原 P2 #1（`ReviewPacketArtifact.kind` enum 缺 `prompt_snapshot / codex_log / agent_file / diagnostic`）已在当前 tree 修复：`schemas/review_packet.schema.json` 已定义 `$defs.ReviewPacketArtifact` 与顶层 `artifacts` property；`api/openapi.yaml` 的 `ReviewPacketArtifact.kind` enum（line 841）与 `ReviewPacketSummary.artifacts`（line 915-917）均已含全部 kind 值（含 `secret_artifact`/`secrets`）。本节不再向 D1 owner forwarding。

### 3.2 D5 / R13 — 已随当前 main 合入

D6 原始基线未声明 `scripts/build-release.sh` / `npm ci` 行为；合入当前 `main` 后，tree 已包含 `scripts/build-release.sh`、`web/package-lock.json`、`docs/RELEASE_NOTES.md` 与 D5 review 记录。本文对 D5 的描述以当前合并后的 main 状态为准。

D5 顺带发现 Windows build issue（pre-existing, `internal/db/schema.go:105/134`）—— 归 D5 / R13 范围，不在 D6 改动面。

### 3.3 C3 协调层 shutdown-ordering P1（v1.1 WIP）

C3 数据层（schema / nonce / heartbeat / diagnostics / migrated 兼容）经 5 轮 review 0 finding；协调层 1 P1 留作 C5 daemon lifecycle design problem。在长跑 scheduler tick 与新 owner 并发接管之间，daemon 关闭序列的顺序仍可在小窗口内产生并发接管。详见 `V1_PHASE_C_ACCEPTANCE.md` §4.2。

### 3.4 C4 trust 边界专项 4 项（v1.1 WIP）

C4 12 commits 收口后留作 v1.1 收口的 4 项已知限制：

1. **fail-open 模式反复在 `repo_root` guard 路径重犯**：round 4 修过一次，round 6 又在同文件 + 镜像 `internal/cli/cli.go` 的 `checkCLISessionRepoRoot` 修第二次。fail-open 注释仍是 repo_root 路径的明显反模式。
2. **validation failure vs missing file 区分不足（已修）**：`logoutRevokeFromFile` 现已返回独立的 `validationFailed` bool（`internal/cli/cli.go:982-1006`）；validation 失败时 `usable=true, validationFailed=true`，调用方保留 project-scoped 文件，不再错误删除。
3. **镜像检查未去重**：`daemonclient` 和 `cli` 两个包各自有 `checkSessionRepoRoot` / `checkCLISessionRepoRoot` + `normaliseRepoRootForCompare`，修改时必须同时改两边。
4. **project_id 不匹配的可观测性**：`ReadSessionFile` 在 project_id 不匹配时只返 `ErrUnauthorized`，operator 看到的是"session not valid for this project"——但不能区分 copied-DB / stale session / foreign-bearer-attempt。

详见 `V1_PHASE_C_ACCEPTANCE.md` §5.3。C5 抽 `internal/trustboundary` 共享包统一 fail-closed 收口。

### 3.5 D4 / R16 实施中

Rework prompt 上下文产品化（D4 / R16）尚未启动，阶段 D 内最后一项产品化 R 项。

## 4. 提交列表

| Commit | 范围 |
|---|---|
| `docs: D6 phase-close notes (R15) + README/PRD/TECH/ACCEPTANCE/codex docs alignment` | 本批 D6 文档收口（worktree `codex/v1-productization-d6-docs`） |

## 5. 验证

```bash
cd /Users/xiquandai/Documents/code/local-symphony-d6-docs
python3 scripts/validate_contracts.py
```

必须仍通过（文档改动不能引入合同漂移）。D6 范围**不跑**：`go test ./...`、`npm test`、`acceptance-local.sh`、`SYMPHONY_TEST_CODEX=1`。

## 6. 范围合规

v1 禁项清单（D6 范围内未引入）：

```text
❌ auto push / PR / merge / publish
❌ auto commit
❌ auto workspace cleanup / reset / rebase
❌ auto retry queue / timer
❌ dynamic tools / MCP
❌ remote dashboard / multi-tenant RBAC
❌ secret 管理
❌ raw prompt / raw Codex log / raw secret 暴露
```

D6 以文档同步为主，附带少量 contract 对齐（`api/openapi.yaml` 保留并填充 `AppState.codex` 字段以匹配 `CodexAvailability(...)` 输出）和测试合同 manifest 对齐；未触碰 Go 实现代码或 dashboard 前端逻辑。
