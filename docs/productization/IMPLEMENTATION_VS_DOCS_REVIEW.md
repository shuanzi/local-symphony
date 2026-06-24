# 代码实现 × 文档产物 审查报告

**审查日期**：2026-06-21
**审查范围**：`main` @ `93b7e7b`（合并 D6/PR#25 之后）的全部代码实现 vs `docs/productization/`、`README.md`、`PRD.md`、`TECH_SPEC.md`、`api/openapi.yaml`、`schemas/`、`db/schema/`、`web/src/` 等文档/合同产物
**审查方法**：73 个子 agent 的多 agent 工作流——3 路并行勘察（代码事实 / 文档断言 / 合同事实）→ 8 维度深审（D4/D1/D3/C3/C4/合同漂移/安全边界/文档完整性）→ 逐条对抗式核验（61 raw finding → 60 confirmed / 1 refuted）→ 综合。所有 P 级 finding 均附 file:line 双向证据。
**基线**：`git merge-base --is-ancestor` 校验 + `gh pr view` + grep/Read 直接核对，关键结论由主 agent 独立复验。

---

## 0. 执行摘要（先读这一段）

**一句话结论：实现本体是健康的，但文档已经"跑在代码前面"——最大的风险不是代码有 bug，而是文档自相矛盾，让人无法判断还剩什么没做。**

- ✅ **安全边界扎实**：受保护内容模型（`IsProtectedPath`）、raw prompt/log/secret 拒绝边界、`owner_nonce` 仅指纹暴露、fail-closed 哨兵——这些核心安全面在代码 + OpenAPI + JSON Schema + DB + TS 之间端到端一致，对抗式核验没有发现可泄露路径。
- ✅ **数据层完整**：C3 runtime ownership（nonce/heartbeat/reap）已通过 PR #19 **合并进 main**，5 轮 review 0 data-layer finding；`reviewStructuredProjection`、`CodexAvailability` 三面接线、`review_packets` 合同均已落地。
- ⚠️ **最大风险——D4/R16 文档"假合并"**：`V1_PHASE_D_ACCEPTANCE.md` 断言 D4/R16 已 "merged" / "✅ v1.1 WIP 收口"，但 **PR #27 仍是 OPEN**（`mergeable=CONFLICTING`）、`178f638` **不是 main 的祖先**、main 上 `grep` 不到任何 D4 实现代码。`D6_DOCS_CLOSE_NOTES.md` 和 `README.md` 则正确写"尚未启动/准备中"。三份文档给出三个互相矛盾的状态。
- ⚠️ **剩余任务账目不诚实**：`EXECUTION_PLAN §2.1` 仍写 `- [ ] 阶段 D ... 未开始`（实际 D1/D3/D5/D6 已合并）、§11 建议"下一步优先推进 C3"（C3/PR#19 早已合并）、更新日期 `2026-06-08` 早于所有 D 阶段合并。两份收口文档在 R9/R10/R14/R15/R16 共 5/17 个 R 项上互相矛盾。
- ⚠️ **合同漂移有界但不受守卫**：fallback DB schema（`internal/db/schema.go`）丢掉了 3 个 FK + 4 个 index + 2 个 CHECK；TS 类型缺 `raw_*_exposed` 三字段、20 个 OpenAPI 必填字段标成可选。`validate_contracts.py` **不检查** fallback schema 也不检查 TS 类型，所以这些漂移能静默通过 CI。

---

## 1. 验证方法与可信度

| 项 | 数值 |
|---|---|
| 子 agent 总数 | 73 |
| 勘察阶段事实产出 | 代码 40 / 文档 48 / 合同 30 |
| 深审 finding 原始数 | 61 |
| 对抗核验后 confirmed | 60 |
| refuted | 1（"未跟踪 R5 raw 文件未反映进 close notes"——被核验为非问题） |
| 核验失败（rate limit） | 1（docs-completeness:R-4，未阻塞结论） |
| 主 agent 独立复验的关键结论 | D4 PR 状态、C3 合并状态、R 表矛盾、EXECUTION_PLAN §2.1/§11 陈旧 |

> 对抗式核验原则：每个 finding 由独立 subagent 打开文件亲自核对，"找不到具体证据即默认 refute"。本报告只收录 confirmed 项。

---

## 2. 剩余任务总表（按"当前剩余任务"组织）

> 这是本报告的核心交付。按阶段分组，每项含真实状态 + 证据。**status** 以 main 实际代码为准，不以文档措辞为准。

### 2.1 真正未完成的实现工作

| # | 阶段 | 任务 | 真实状态 | 证据 |
|---|---|---|---|---|
| 1 | **D4 / R16** | Rework prompt 上下文产品化：合并 **OPEN PR #27**（`codex/v1-productization-d4-rework`，17 commits 含 `178f638`）。分支上已实现 `injectReworkContext` / `SafeSummary` DTO / `BuildReworkPrompt` / `rework_snapshots` 表 / 带 protected-path 过滤的 cumulative-diff SHA。**main 上 reason 已持久化（`store.go:2188-2198`）但从不读回 prompt**。 | 🔴 未合并 | `orchestrator.go:156` RenderPrompt 仅带 `{issue, run, workspace}`；`git merge-base --is-ancestor 178f638 main` ⇒ exit 1；`gh pr view 27` ⇒ OPEN / CONFLICTING；main 上 grep `injectReworkContext\|BuildReworkPrompt\|SafeSummary\|rework_snapshots\|LatestReviewReasonForIssue` ⇒ 0 命中 |
| 2 | **D2 / R11** | Dashboard 产品化补齐：Overview / Approval Inbox / Review Packet / Diagnostics 的页面级状态覆盖（loading/empty/auth error/daemon unavailable/artifact refusal/command error）。明确推迟到 v1.1。 | 🔴 未启动 | `V1_PHASE_D_ACCEPTANCE.md:16`（D2 'n/a 不在本阶段范围'）；`EXECUTION_PLAN.md:714-735` |
| 3 | **C3 协调层 P1** | shutdown 顺序 vs 长跑 scheduler tick 的并发接管 race。round-5 P1 标记"不修"，推迟到"C5 daemon lifecycle design problem"——但**计划里根本没有 C5 章节**（§6 阶段 C → §7 阶段 D，无 §C5），推迟目标悬空，无 worktree/PR。 | 🟡 已知限制，悬空 | `EXECUTION_PLAN.md:560-572`（round-5 P1 'shutdown 顺序' 标 不修 C5）、`:574-579`（留作 C5，需新建 worktree + 追加 C5 子章节）；计划无 §C5 heading |
| 4 | **C4 信任边界** | 抽 `internal/trustboundary` 共享包（统一 fail-closed `CheckRepoRoot/CheckProjectID/CheckAPIURL` + `validation_failure_kind` enum），去重 daemonclient/cli 的镜像 guard，解决 4 条 C4 已知限制。作为 C5 信任边界专项收口。**包不存在**。 | 🔴 未启动 | `ls internal/trustboundary` ⇒ No such file；`grep -rn trustboundary --include='*.go'` ⇒ 0；`EXECUTION_PLAN.md:648-661` |
| 5 | **C4 validation-failure vs missing-file 区分** | `logoutRevokeFromFile` 路径**已修**（`cli.go:982-1006` 返 `validationFailed` bool），但 `loadCLISessionToken`（`session.go:37-55`）、`readCLISessionToken`（`cli.go:191-197`）、`ReadSessionFile`（`session.go:68-87`）仍把 `IsNotExist` 与 validation 失败混为一桶。`D6` 误标"(已修)"。 | 🟡 部分修 | `cli.go:982-1006`（已修）vs `cli.go:191-197` / `session.go:37-55` / `session.go:68-87`（未修）；`D6_DOCS_CLOSE_NOTES.md:67`（'(已修)'）vs `EXECUTION_PLAN.md:651` + `PHASE_C_ACCEPTANCE.md:177`（仍 open） |
| 6 | **C1 子项** | "review packet/diagnostics 纳入 hook summary"（`EXECUTION_PLAN:453`）+ "hook summary 纳入 review packet/diagnostics"（`:459`）仍未勾选，但 C1 整体标 `[x]` 已落地。hook summary→review-packet 联动未实现。 | 🔴 未启动 | `EXECUTION_PLAN.md:453,459`（未勾）vs `:39`（C1 `[x]`）；`grep hook_summary\|HookSummary internal/` ⇒ 0；`review.go:153-177` 从 handoff/issue/git 构建 review.json，无 hook summary |
| 7 | **合同守卫缺口** | 扩展 `validate_contracts.py` 覆盖 `internal/db/schema.go` fallback schema + `web/src/types.ts`——当前 fallback（缺 FK/index/CHECK）与 TS 漂移不受守卫，能静默过 CI。 | 🔴 未启动 | `validate_contracts.py` grep `'fallback\|schema.go\|internal/db'` ⇒ 0；grep `'typescript\|types.ts\|web/src'` ⇒ 0；validator 退出 0 'contract validation passed' |
| 8 | **fallback DB schema 平价** | 恢复 `fallbackAppSchema` 的 3 个 `project_id` FK + 4 个 index（local_sessions/open_tokens/runtime_descriptors）；恢复 `fallbackProjectSchema` 的 `review_packets.status` CHECK + generated-implies-NOT-NULL CHECK。（`artifacts.kind` CHECK 缺失是有意的，`schema_test.go:133-134` 已注明。） | 🔴 未启动 | `internal/db/schema.go:16-18,49`（无 FK/CHECK）vs `db/schema/v1_app.sql:41,54,71` + `v1_project.sql:305,322-330`；`MigrateProjectSchema`（`schema.go:206-234`）在无 CHECK 列上 probe 提前返回，不修复 |
| 9 | **TS 类型合同** | 给 `web/src/types.ts` 的 `ReviewPacketArtifact` 补 `raw_prompt_exposed/raw_codex_log_exposed/raw_secret_exposed`；收紧 `ReviewPacketSummary`（当前 20/25 个 OpenAPI 必填字段标成可选）。可选的 v1.1 合同清理项。 | 🟡 部分 | `types.ts:173-180`（缺 3 字段）vs `openapi.yaml:838` + `review_packet.schema.json:11-20`（必填）；`types.ts:191-227`（20 字段可选）vs `openapi.yaml:854-920`（25 必填） |
| 10 | **D1 审查面** | Review packet 的 approvals sidecar 只暴露 `approval_requests` 行元数据（8 字段），**不含** `action_summary/risk_level/policy_match`——这些正是 Approval Inbox 渲染的字段。Review Packet 页面 approvals 行只能显示数量。 | 🟡 投影不完整 | `review.go:654-665`（8 字段 SELECT+投影）vs `App.tsx:1875-1907`（inbox 渲染 action_summary/risk_level/policy_match）；`openapi.yaml:897-899`（approvals 允许更丰富条目但代码不填） |

### 2.2 文档对账（"账目诚实性"问题——这是本次审查发现的最普遍一类）

| # | 问题 | 真实状态 | 证据 |
|---|---|---|---|
| 11 | **D4/R16 三份文档三个矛盾状态** | PR #27 OPEN 未合并。`PHASE_D_ACCEPTANCE` 说"merged"/"✅"（**假**）；`D6` 说"尚未启动"（**对**）；`README` 说"准备中"（**最接近**，但低估了一个跑了 12 轮 review 的开放 PR）。 | `PHASE_D_ACCEPTANCE.md:15/130/143` vs `D6_DOCS_CLOSE_NOTES.md:73-75` vs `README.md:12`；`gh pr view 27` ⇒ OPEN |
| 12 | **R 表 5/17 项矛盾** | `D6_DOCS_CLOSE_NOTES`（R9🟡/R10🟡/R14🟡/R15🟡/R16⏳）vs `PHASE_D_ACCEPTANCE`（同项全 ✅ / "全部 ship"）。两份文档同日（2026-06-09）声称记录同一阶段收口，却对 5 项不一致。`D6` 后续 commit（`c2cf88d`）还把 R14 从 ✅ 降级为 🟡——漂移在 follow-up 里被**强化**而非弥合。 | `D6:21/22/26/27/28` vs `PHASE_D:123/124/128/129/130/133` |
| 13 | **EXECUTION_PLAN §2.1 陈旧** | 仍写 `- [ ] 阶段 D ... 未开始`——实际 D1/D3/D5/D6 已合并（PR #23/#24/#25/#26）。更新日期 `2026-06-08` 早于所有 D 阶段合并。 | `EXECUTION_PLAN.md:40`（未开始）、`:4`（2026-06-08）；`gh pr list` ⇒ #23/#24/#25/#26 MERGED |
| 14 | **EXECUTION_PLAN §11 陈旧** | 建议"下一步优先推进 C3"——C3/PR#19 早已合并。 | `EXECUTION_PLAN.md:961`；`gh pr view 19` ⇒ MERGED 2026-06-08 |
| 15 | **EXECUTION_PLAN C3 分支状态陈旧** | 写"4 个 commit 保留在 worktree（`codex/v1-productization-c3-owner-nonce`，未 push / 未 PR / 未 merge）"——实际已通过 PR #19 合并进 main，且该分支不存在。`PHASE_C_ACCEPTANCE` 自己就标 C3 PR#19 merged——两份文档自相矛盾。 | `EXECUTION_PLAN.md:507`（未合并）vs `PHASE_C_ACCEPTANCE.md:12,21`（merged）；`git merge-base --is-ancestor d8840d2 main` ⇒ 是祖先；`git branch -a \| grep c3-owner` ⇒ 无 |
| 16 | **PHASE_C_ACCEPTANCE §3 heartbeat 描述与代码相反** | 文档写 heartbeat 失锁"仅打印 stderr 并退出 goroutine，不主动终止 daemon"。代码实际是 **fail-closed 主动关闭**（停所有 loop + `srv.Close` + 非零返回），且有回归测试。 | `PHASE_C_ACCEPTANCE.md:69`（陈旧限制措辞）vs `app.go:170-188`（fail-closed）+ `app_test.go:706`（`TestServeExitsAfterHeartbeatOwnershipLost`） |
| 17 | **D6 误标 C4 limitation #2 "(已修)"** | 见 #5——只修了 logout 路径，D6 却标整体"已修"，与 EXECUTION_PLAN/PHASE_C_ACCEPTANCE 冲突。 | `D6_DOCS_CLOSE_NOTES.md:67` vs `EXECUTION_PLAN.md:651` + `PHASE_C_ACCEPTANCE.md:177` |
| 18 | **EXECUTION_PLAN limitation #1 措辞陈旧** | 写"该 fail-open 注释仍是 repo_root 路径的明显反模式"——round-6 已把 fail-open 注释换成 fail-closed 注释，guard 现在返 `ErrUnauthorized`。真正残留问题（无共享包导致模式在两个镜像文件重犯）在同段别处正确捕获，但这一句读起来像当前 bug。同一陈旧措辞在 `PHASE_C_ACCEPTANCE.md:176` 重复。 | `EXECUTION_PLAN.md:650` + `PHASE_C_ACCEPTANCE.md:176` vs `session.go:96-126` + `cli.go:227-253`（fail-closed） |
| 19 | **PHASE_D_ACCEPTANCE §1 标题复制粘贴错误** | `D1 hook lifecycle`（实为 D1/R10 Review Packet API）、`D3 scheduler tick loop`（实为 D3/R14 Codex availability）、`D5 single daemon ownership`（实为 D5/R13 Release packaging）——3/4 粗体标题套用了 Phase-C 模板，正文描述正确，仅标题错。 | `PHASE_D_ACCEPTANCE.md:20-23` |
| 20 | **GAPS 文档"现状"段落陈旧** | `V1_REAL_PRODUCTIZATION_GAPS.md`（2026-05-22）的 R8/R9/R14 现状段说"serve 不启动 tick""CLI 直连 SQLite""diagnostics 硬编码 codex.available=false"——这些现在都实现了。R16 现状段仍准确。GAPS 是计划的"需求来源"但无"以执行计划为准"的免责声明。 | `GAPS.md:164-165,179-180,251,289` vs `EXECUTION_PLAN.md:5` |

---

## 3. 漂移 / 不一致 Finding 详表（按严重度排序）

### 🔴 Important（影响判断或合同一致性，应优先处理）

| ID | 维度 | 标题 | 类型 | 证据 |
|---|---|---|---|---|
| F1 | D4 | D4/R16 **未在 main 实现**——prompt 不带 review reason / safe summary / cumulative diff | remaining-task | `orchestrator.go:156,218-232,296-298`；`store.go:2084-2211`（持久化 reason 不读回）；main grep 0 命中 |
| F2 | D4 | `PHASE_D_ACCEPTANCE` 声称 D4 已 merged/shipped，与 D6/README/实际 main 矛盾 | doc-ahead | `PHASE_D_ACCEPTANCE.md:15,130,143` vs `D6:73-75` / `README:12`；PR #27 OPEN |
| F3 | D4 | main 上 rework reason 持久化但从不读回 re-dispatch prompt | remaining-task | `store.go:2188-2198`（持久化）vs `orchestrator.go:156`（不读回）；`LatestReviewReasonForIssue` main 上 0 命中 |
| F4 | C3 | `PHASE_C_ACCEPTANCE` 称 heartbeat 失锁只打印 stderr 不终止——代码实际 fail-closed 主动关闭 | doc-stale | `PHASE_C_ACCEPTANCE.md:69` vs `app.go:170-188` + `app_test.go:706` |
| F5 | C4 | `D6` 误标 C4 limitation #2 "(已修)"——只修 logout 路径，其余 3 处仍 collapse | doc-ahead | `D6:67` vs `cli.go:191-197` / `session.go:37-55,68-87`；`EXECUTION_PLAN:651` + `PHASE_C_ACCEPTANCE:177` 仍 open |
| F6 | 合同漂移 | `fallbackAppSchema` 丢 3 个 FK + 4 个 index | drift | `schema.go:16-18`（无 FK）vs `v1_app.sql:41,54,71` |
| F7 | 合同漂移 | `fallbackProjectSchema` 丢 `review_packets.status` CHECK + generated-implies-NOT-NULL CHECK；`MigrateProjectSchema` 在无 CHECK 列上 probe 提前返回不修复 | drift | `schema.go:49,206-234` vs `v1_project.sql:305,322-330` |
| F8 | 合同漂移 | TS `ReviewPacketArtifact` 缺 3 个 `raw_*_exposed`（OpenAPI + schema 必填，服务器逐条发出） | drift | `types.ts:173-180` vs `openapi.yaml:838` + `review_packet.schema.json:11-20`；`httpapi.go:871-873` |
| F9 | 合同漂移 | `validate_contracts.py` 不检查 fallback schema 也不检查 TS——"无第三套事实来源"原则只在 openapi/JSON-schema/SQL 之间执行 | remaining-task | `validate_contracts.py` grep 0 命中；validator 退出 0 |
| F10 | 安全边界 | 任务描述里"hashGitBlob 非零退出当 absent"是错的——实际函数是 `gitShowHeadPath`，非零退出 ⇒ `unknown=true` ⇒ **fail-closed**（拒绝匹配的 untracked 文件）。代码是安全的，陈旧的是任务描述。 | doc-stale | `review.go:322-342,308-320,408-412`；grep `hashGitBlob` ⇒ 0 |
| F11 | 安全边界 | "filler copy" 残留风险与 `cumulative_diff_sha` 保护**只在未合并的 D4 分支上**，main 上 0 命中。D6 说 not-started 而 PHASE_D 说 merged——又是 D4 矛盾。 | doc-stale | main grep `filler copy\|cumulative_diff` ⇒ 0；`D6:73-75` vs `PHASE_D:15,130` |
| F12 | 文档完整性 | `EXECUTION_PLAN §2.1` 说"阶段 D 未开始"，实际 D1/D3/D5/D6 已合并 | doc-stale | `EXECUTION_PLAN.md:40,4,961` |
| F13 | 文档完整性 | D4/R16 三份文档三个矛盾状态 | inconsistency | `PHASE_D:15/130/143` vs `D6:73-75` vs `README:12`；PR #27 OPEN |
| F14 | 文档完整性 | R 表 5/17 项矛盾（D6 vs PHASE_D） | inconsistency | `D6:21/22/26/27/28` vs `PHASE_D:123/124/128/129/130` |
| F15 | 文档完整性 | C3 协调层 P1 推迟到 C5 但计划无 §C5 章节 | remaining-task | `EXECUTION_PLAN.md:560-579`；计划无 §C5 heading |
| F16 | D1 | Review packet approvals sidecar 只暴露 8 字段行元数据，不含 inbox 用的 action_summary/risk_level/policy_match | inconsistency | `review.go:654-665` vs `App.tsx:1875-1907` |

### 🟡 Minor（措辞/可观测性/合同清晰度，不阻塞）

| ID | 维度 | 标题 | 类型 | 证据 |
|---|---|---|---|---|
| F17 | D3 | D6 标 R14 🟡 部分完成 vs PHASE_D 标 ✅ shipped——代码支持"已 ship"读法 | inconsistency | `D6:26` vs `PHASE_D:128`；`operator.go:243`/`httpapi.go:250`/`diagnostics.go:50` 三面已接线 |
| F18 | D3 | `warning` 字段对任何 `!Available` 都发 `unsupported_codex_version`，不只是缺 fixture；failure_reason 19 种但 warning 不区分 | drift | `codex_availability.go:78-80,84` vs `preflight.go:26-46` |
| F19 | D3 | `AppState.codex` 是 `additionalProperties:true` 开放对象，未 `$ref` `DiagnosticsCodex`——合同层不强制形状匹配 | remaining-task | `openapi.yaml:472-474` vs `:1135-1146` |
| F20 | D3 | `DiagnosticsCodex.warning` 是自由 nullable string 非 enum（failure_reason 是 enum） | remaining-task | `openapi.yaml:1146` + `diagnostics.schema.json:326` |
| F21 | C3 | EXECUTION_PLAN 称 C3 在未 push/未合并分支——实际已合并进 main（PR#19） | doc-stale | `EXECUTION_PLAN.md:507` vs `PHASE_C_ACCEPTANCE.md:12,21`；`git merge-base d8840d2 main` ⇒ 祖先 |
| F22 | C3 | shutdown-ordering P1 是唯一未修协调层问题，数据层完整一致 | remaining-task | `app.go:215-226,316-371`；`store.go:54,2677,2732,2782` |
| F23 | C3 | legacy `CreateRuntimeDescriptor`（auto-nonce）入口仍暴露，与显式 nonce 变体并存（已诚实披露） | remaining-task | `store.go:2656-2662` vs `:2677`；`app.go:374`；`PHASE_C_ACCEPTANCE.md:66` |
| F24 | C4 | EXECUTION_PLAN limitation #1 "fail-open 注释仍是反模式" 措辞陈旧——已是 fail-closed | doc-stale | `EXECUTION_PLAN.md:650` + `PHASE_C_ACCEPTANCE.md:176` vs `session.go:96-126` |
| F25 | 合同漂移 | TS `ReviewPacketSummary` 20/25 OpenAPI 必填字段标可选 + 多了未声明的 `files?`（服务器确实发 `files` 作为 `artifacts` 别名，是 OpenAPI 漏文档） | drift | `types.ts:191-227,223` vs `openapi.yaml:854-920`；`httpapi.go:778-779` |
| F26 | 合同漂移 | TS 无 `AppState` interface、无顶层 `codex` 字段——dashboard 只经 `data.diagnostics.codex` 取；整个 `/state` 响应在 TS 层无类型 | drift | `types.ts`（无 AppState）vs `openapi.yaml:472`；`App.tsx:752` |
| F27 | 合同漂移 | `ReviewPacketArtifact.kind`（24 值）vs 通用 `Artifact.kind`/`artifacts` CHECK（18 值）——6 个合成投影 kind 不入库。有意且单向测试覆盖，但合同未文档化、validate_contracts 不守卫关系 | drift | `openapi.yaml:841` vs `:960`；`v1_project.sql:266`；`schema.go:214`；`httpapi_test.go:3632-3641`（超集测试） |
| F28 | D1 | R10/D1: D6 标 🟡 进行中 vs PHASE_D 标 ✅ shipped——代码支持 shipped | doc-stale | `D6:22` vs `PHASE_D:14,20,124`；`httpapi.go:770-845` 已落地 |
| F29 | D1 | PHASE_D §1 'D1 hook lifecycle' 标题复制粘贴错误（应为 Review Packet API），3/4 粗体标题套 Phase-C 模板 | doc-stale | `PHASE_D_ACCEPTANCE.md:20-23` |
| F30 | D1 | TS `ReviewPacketArtifact` 缺 `raw_*_exposed`（同 F8，标为 v1.1 收口项） | remaining-task | `types.ts:173-180` vs `openapi.yaml:836-838`；`PHASE_D_ACCEPTANCE.md:20` |
| F31 | D1 | TS `ReviewPacketSummary` 全标可选（同 F25） | remaining-task | `types.ts:191-227` vs `openapi.yaml:854-920` |
| F32 | C1/D1 | C1 子项"hook summary 纳入 review packet/diagnostics"未实现，但 C1 标 done | remaining-task | `EXECUTION_PLAN.md:453,459` vs `:39`；grep `hook_summary` ⇒ 0 |
| F33 | 安全边界 | Codex stderr 只字节数计数、从不 redact-and-store（discard-by-byte-count）；文档反复叫"stderr redaction"。fail-closed by omission，但若未来开始抓 stderr 文本无 redaction 闸 | drift | `codex.go:244-257,1660-1668` vs `EXECUTION_PLAN.md:182,198,956` |
| F34 | 安全边界 | review.md markdown body 可能携带 protected 内容——handoff.Summary/Tests/Risks/Verification 原样渲染，无 `IsProtectedPath` 扫描。PHASE_B 已声明"非 DLP 引擎"但未把 handoff 文本面列为残留限制 | remaining-task | `review.go:155-167,564-599`；`toolgateway.go:384-396`（仅校验形状） |

### ⚪ Info（已确认健康，列此以示核查覆盖）

| ID | 维度 | 标题 | 证据 |
|---|---|---|---|
| I1 | D4 | D4/R16 **在未合并分支上完整实现**（a-e 全部） | `git show origin/codex/v1-productization-d4-rework:...rework_prompt.go / safe_summary.go / orchestrator.go` |
| I2 | D1 | `reviewStructuredProjection` 已 ship 全部 D1 字段 + 三 `raw_*_exposed` 默认 false | `httpapi.go:770-845`；`review.go:153-177` |
| I3 | D1 | artifact refusal case（raw kind → `content_url=null`）已实现并测试 | `httpapi.go:752-760,862-875`；`httpapi_test.go:3383-3461` |
| I4 | D1 | `ReviewPacketArtifact.kind` enum（24 值）openapi/schema/code 一致 | `review_packet.schema.json:24-49` = `openapi.yaml:841` |
| I5 | D1 | dashboard ReviewPacketPage 消费结构化 API，不读文件系统 | `App.tsx:1943,1963,2037-2061` |
| I6 | D3 | `CodexAvailability` 三面（state/status/diagnostics）已接线，无 stub/TODO | `httpapi.go:250`；`operator.go:243`；`diagnostics.go:50` |
| I7 | C3 | `owner_nonce` 仅指纹暴露端到端一致（DB 存全量，diagnostics/openapi/schema/投影/validator 仅 fingerprint） | `v1_app.sql:60-72`；`schema.go:18`；`openapi.yaml:1123-1134`；`diagnostics.schema.json:243-294`；`store.go:2936-2954` |
| I8 | C4 | `internal/trustboundary` 缺席——已诚实标为 C5 提议工作，未夸大 | `ls internal/trustboundary` ⇒ 无；`EXECUTION_PLAN.md:655-661` |
| I9 | C4 | 镜像 `checkSessionRepoRoot`/`checkCLISessionRepoRoot` 逐字重复——已诚实标为 limitation #3 | `session.go:107-145` vs `cli.go:234-265` |
| I10 | C4 | `validation_failure_kind` 字段缺席——已诚实标为提议 | grep 0 命中；`EXECUTION_PLAN.md:660` |
| I11 | 合同 | `DiagnosticsRuntimeDescriptor` openapi/schema/TS/投影一致（无 owner_nonce，仅 fingerprint） | `openapi.yaml:1123-1134` = `diagnostics.schema.json:243-294` = `store.go:2935-2955` |
| I12 | 合同 | Approval DTO 四源（openapi/schema/TS/store struct）14 字段完全一致 | `openapi.yaml:804-822`；`approval_request.schema.json:7-22`；`types.ts:141-156`；`store.go:112-127` |
| I13 | 合同 | `ApiErrorCode` openapi vs schema 枚举集合相同（仅排序不同，daemon_already_running 位置不同）——集合语义等价 | `openapi.yaml:427` vs `failure_codes.schema.json:93`；validate_contracts 按集合比较通过 |
| I14 | 合同 | `review_packet.schema.json` 的 `failure_code` 内联 enum 是 `failure_codes.schema.json` 的副本——但 `validate_contracts.py:782-794` **已交叉校验** | `review_packet.schema.json:432-460`；`validate_contracts.py:782-794` |
| I15 | 安全 | 受保护内容模型在 orchestrator+review 间实现一致 | `security.go:468-484`；`review.go:534-543,210`；`toolgateway.go:124,173`；`codex.go:474` |
| I16 | 安全 | redaction golden fixture 已接 `validate_contracts`，但只校验 fixture 形状不调 Go 引擎；运行时覆盖在 `orchestrator_test.go:843-865` | `validate_contracts.py:516-571`；`orchestrator_test.go:843-865` |
| I17 | 安全 | 无 raw prompt/log/secret 泄露路径（API/dashboard/artifact 路由） | `httpapi.go:752-760,931-934,798-800,983-992,214-216`；`review.go:118-139,170-172` |
| I18 | 安全 | `artifact.attach` protected-path hard-deny 已测试：失败 tool call，不终止 run、不建 approval | `toolgateway.go:124-127`；`toolgateway_test.go:156-189` |
| I19 | 文档 | C5 trust-boundary 包缺席——已诚实标为 v1.1 WIP 未来工作 | `ls internal/trustboundary` ⇒ 无；`EXECUTION_PLAN.md:648-661` |

---

## 4. 被驳回的 Finding（1 项，供透明）

| 标题 | 驳回理由 |
|---|---|
| 未跟踪的 `D4_CODEX_REVIEW_R5_RAW.txt` / `D6_CODEX_REVIEW_R5_RAW.txt` 指示 R5 review 工作未反映进 close notes | 核验认为这些 raw 文件是 review 过程的中间产物，不构成"未反映的剩余任务"，属观察性误报。 |

---

## 5. 建议处理优先级

按"先消除判断歧义、再补实现、再收口合同"的顺序：

**P0 — 消除文档矛盾，让账目诚实（纯文档，无代码风险，应最先做）**
1. 统一 D4/R16 状态：`PHASE_D_ACCEPTANCE.md:15/130/143` 把"merged/✅"改成"PR #27 OPEN / 未合并"，与 `D6`/`README` 对齐。（对应 F2/F11/F13）
2. 统一 R 表：D6 vs PHASE_D 的 R9/R10/R14/R15/R16 对齐到一致状态（建议以 main 实际代码为准：R10/R14 ✅ shipped，R16 ⏳ unmerged-PR）。（F14/F17/F28）
3. 修 `EXECUTION_PLAN §2.1` 阶段 D 复选框 + 更新日期 + §11 "下一步推进 C3" 陈旧建议 + C3 分支状态描述（`507`）。（F12/F14/F15/F21）
4. 修 `PHASE_C_ACCEPTANCE.md:69` heartbeat 描述为 fail-closed；修 `:176`/`EXECUTION_PLAN:650` fail-open 陈旧措辞。（F4/F18/F24）
5. 修 `PHASE_D_ACCEPTANCE §1` 复制粘贴标题。（F29）
6. 统一 C4 limitation #2 状态描述（logout 已修 + 3 处未修），去掉 D6 的整体"(已修)"。（F5）

**P1 — 推进未完成实现**
7. **决策 D4/R16 PR #27**：合并（先解 CONFLICTING）或明确归档。这是阶段 D 唯一的实质实现缺口。（F1/F3）
8. 启动 D2/R11 dashboard 产品化（或明确推迟决策）。
9. 决定 C5：要么新建 §C5 章节并开 worktree 收口 C3 shutdown P1 + C4 trustboundary 包，要么显式接受为 v1.1 长期 WIP 并在计划里落地章节。（F15/任务#3/#4）

**P2 — 收口合同守卫与漂移**
10. 扩展 `validate_contracts.py` 覆盖 fallback schema + TS 类型，让 F6/F7/F8/F25/F26 不再静默过 CI。（F9）
11. 修 fallback schema 平价（FK/index/CHECK）或显式文档化为何允许弱化。（F6/F7）
12. 补 TS `raw_*_exposed` + 收紧 `ReviewPacketSummary` 必填字段。（F8/F25/F30/F31）
13. （可选）补 review packet approvals sidecar 的 action_summary/risk_level/policy_match。（F16）
14. （可选）补 GAPS 文档"以执行计划为准"免责声明。（F20）

---

## 6. 附：核验命令清单（供你独立复核）

```bash
# D4/R16 未合并
gh pr view 27 --json state,mergeable,mergedAt        # OPEN / CONFLICTING / null
git merge-base --is-ancestor 178f638 main && echo merged || echo unmerged  # unmerged
grep -rIl --include='*.go' -E 'injectReworkContext|BuildReworkPrompt|SafeSummary|rework_snapshots' internal/  # 0 命中

# C3 已合并
gh pr view 19 --json state,mergedAt                  # MERGED 2026-06-08
git merge-base --is-ancestor d8840d2 main && echo merged

# R 表矛盾
grep -nE '^\| R(9|10|14|15|16) ' docs/productization/D6_DOCS_CLOSE_NOTES.md
grep -nE '^\| R(9|10|14|15|16) ' docs/productization/V1_PHASE_D_ACCEPTANCE.md

# EXECUTION_PLAN 陈旧
sed -n '4p;40p;507p;961p' docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md

# fallback schema 漂移
grep -nE 'FOREIGN KEY|CHECK' internal/db/schema.go   # 远少于 v1_app.sql
python3 scripts/validate_contracts.py                 # 当前退出 0（不守卫 fallback/TS）

# trustboundary 缺席
ls internal/trustboundary                             # No such file
```

---

**审查结论**：代码实现健康，安全边界与数据层扎实；首要问题不在代码而在**文档账目自相矛盾**（尤其 D4/R16 "假合并" 与 R 表 5 项矛盾），其次是 D4/R16 一个开放 PR 待决策、D2 待启动、C5 悬空。建议先做 P0 纯文档对账（零代码风险、立即恢复判断力），再决策 D4 PR，最后补合同守卫。
