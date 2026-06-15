# D6 / R15 — 文档、合同与验收对齐（阶段收口）

**日期**：2026-06-09
**对应计划**：`docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` §7 阶段 D / D6
**worktree 分支**：`codex/v1-productization-d6-docs`
**基线**：`main` @ `f15ba39`（v1.1 WIP 收口）
**阶段状态**：**v1.1 WIP** —— D1 主体 + R1 修复 + R2 review 跑中（1 P2 forwarding）；D3 / R14 5 轮 codex review 0 finding 收口；D5 / R13 主体 + R1 修复 + R2 修复 commit 落地（R2 review 文档待生成）；D2 / D4 准备中；D6 / R15 文档合同收口（本批）。

## 1. R 项 status note 表

| R | 名称 | 阶段 | 状态 | 实施 commit | 依据 / 备注 |
|---|---|---|---|---|---|
| R1 | Runner 抽象 | A | ✅ A 完成 | `2cc1888` 之前 | fake runner / Codex adapter 同一接口 |
| R2 | Codex fixture gate | A | ✅ A 完成 | D3 链 `90f0ded` 起 | `docs/codex/FIXTURE_POLICY.md` 锁定 |
| R3 | Codex process lifecycle | A | ✅ A 完成 | D3 链 | launch / timeout / redacted stderr |
| R4 | Codex 事件归一化 | A | ✅ A 完成 | D3 链 | normalized timeline |
| R5 | Approval API + writeback | B | ✅ B 完成 | B1 链 | stage-B command/file/network approvals |
| R6 | Missing handoff continuation | A | ✅ A 完成 | A5 链 | one continuation then `missing_handoff` |
| R7 | Hook lifecycle | C1 | ✅ C1 完成 | C1 链 | `symphony hook` ready/run/cleanup/wait/finish/cancel |
| R8 | Scheduler tick + runtime lock | C2+C3 | ✅ C2 完成 / 🟡 C3 v1.1 WIP | `dbf4c6e` … `d8840d2` | C3 数据层 5 轮 review 0 finding；协调层 shutdown-ordering 1 P1 留作 C5 daemon lifecycle |
| R9 | CLI over REST + daemon session | C4 | 🟡 C4 v1.1 WIP | C4 链见 `docs/productization/V1_PHASE_C_ACCEPTANCE.md` §5.1；已知限制见 §5.3 | 12 commits（5 轮 codex + 6 轮 adversarial + 1 docs）；trust 边界专项 4 项见 §3 已知限制 |
| R10 | Review Packet API | D1 | 🟡 D1 进行中 | `2cc1888` + R1 `0aee74c` | 主体 + R1 修复落地；**R2 review 跑中**：1 P2 forwarding，见 §3 已知限制 |
| R11 | Dashboard 产品化补齐 | D2 | ⏳ D2 准备中 | — | 待启动 |
| R12 | 安全策略执行 | B3 | ✅ B3 完成 | B3 链 | secret/best-effort/loopback/CSRF/Tool Gateway scope |
| R13 | Release packaging | D5 | 🟡 D5 进行中 | `573b1f0` + R1 `41dabb6` + R2 `cdf08ed` | 主体 + R1/R2 修复 commit 均落地；R2 review 文档待生成；顺带发现 Windows build issue（pre-existing, `internal/db/schema.go:105/134`），归 D5 / R13 范围 |
| R14 | Codex availability diagnostics | D3 | ✅ D3 完成 | `90f0ded` … `57c46c0` | 5 轮 codex review 0 finding 收口（HEAD `57c46c0`） |
| R15 | 文档合同 | D6 | 🟡 D6 进行中 | 本批 | 本批文档收口 |
| R16 | Rework prompt 上下文 | D4 | ⏳ D4 准备中 | — | 待启动 |
| R17 | DB schema guard | A0 | ✅ A0 完成 | A0 链 | `internal/db.MigrateAppSchema` idempotent |

## 2. 文档变更清单

| 文件 | 状态 | 范围 |
|---|---|---|
| `README.md` | M | §1 / §14 状态指针；真实 Codex 不运行段补 D3 / R14 |
| `PRD.md` | M | 头部加 v1 阶段 D 收口状态元数据；§8.4 Codex Runner 段加 D3 / D5 状态注 |
| `TECH_SPEC.md` | M | 头部加 v1 阶段 D 收口状态元数据；§10 Codex adapter 段补 D3 已 ship；末尾加 D-Phase Close Summary 指针 |
| `docs/codex/ADAPTER_MAPPING.md` | M | 头部加 D3 / R14 5 轮 review 收口一致状态 |
| `docs/codex/FIXTURE_POLICY.md` | M | CI 段补 D3 / R14 preflight 状态；D5 / R13 release packaging 不在本 policy 声明 |
| `docs/testing/ACCEPTANCE.md` | M | A0–A10 acceptance 分类标签化（fake / 安全回归 / real Codex opt-in）；顶部加分类总览表 |
| `docs/productization/D6_DOCS_CLOSE_NOTES.md` | A | 本文档（R15 主交付物） |

## 3. 已知限制

### 3.1 D1 / R10 — R2 review 跑中（1 P2）

D1 R1 修复 commit `0aee74c` 修完 R1 的 3 个 finding 后，当前 D6 tree 保留 1 个仍需 D1 owner 处理的 P2 finding。已验证 `schemas/review_packet.schema.json` 顶层 `required` 不包含 `artifacts`，因此不再把 top-level `artifacts` 作为 blocker 转交。

- **P2 #1**：`ReviewPacketArtifact.kind` enum 不完整（`schemas/review_packet.schema.json` 与 `api/openapi.yaml`），缺少 `prompt_snapshot / codex_log / secret_artifact / secrets / agent_file / diagnostic`。

**forwarding to D1 实施 agent**：

1. P2 #1 修法：用 union of two enums，至少补 `prompt_snapshot / codex_log / secret_artifact / secrets / agent_file / diagnostic`。

D6 范围内**不修**，仅记录。

### 3.2 D5 / R13 — R2 review 文档待生成

D5 / R13 release packaging 是跨 PR 跟踪项；当前 D6 tree 包含 `web/pnpm-lock.yaml` 与 `packageManager: pnpm@9.0.0`，但不包含 `package-lock.json` 或 release script，因此本文不声明 `scripts/build-release.sh` / `npm ci` 行为。`docs/productization/D5_CODEX_REVIEW_ROUND2.md` 文档仍待 D5 实施 agent 生成。

D5 顺带发现 Windows build issue（pre-existing, `internal/db/schema.go:105/134`）—— 归 D5 / R13 范围，不在 D6 改动面。

### 3.3 C3 协调层 shutdown-ordering P1（v1.1 WIP）

C3 数据层（schema / nonce / heartbeat / diagnostics / migrated 兼容）经 5 轮 review 0 finding；协调层 1 P1 留作 C5 daemon lifecycle design problem。在长跑 scheduler tick 与新 owner 并发接管之间，daemon 关闭序列的顺序仍可在小窗口内产生并发接管。详见 `V1_PHASE_C_ACCEPTANCE.md` §4.2。

### 3.4 C4 trust 边界专项 4 项（v1.1 WIP）

C4 12 commits 收口后留作 v1.1 收口的 4 项已知限制：

1. **fail-open 模式反复在 `repo_root` guard 路径重犯**：round 4 修过一次，round 6 又在同文件 + 镜像 `internal/cli/cli.go` 的 `checkCLISessionRepoRoot` 修第二次。fail-open 注释仍是 repo_root 路径的明显反模式。
2. **validation failure vs missing file 区分不足**：`logoutRevokeFromFile` 把所有非 `IsNotExist` 错误归为同一 `usable=false` 桶，导致 "validation 失败时 project-scoped 文件被错误删除"。
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

D6 仅做文档同步，未触碰代码、schema、test、dashboard。
