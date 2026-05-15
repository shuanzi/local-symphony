# PRD 与 Tech SPEC 双向映射矩阵

> 状态：已完成初始映射。本文档只证明 `PRD.md` 与 `TECH_SPEC.md` 之间存在可追踪合同位置，不代表实现代码或 executable contracts 已全部通过。

## PRD 到 Tech SPEC 映射

| 功能 ID | PRD 功能或场景 | 用户目标 | 触发条件 | 主流程 | 异常或空状态 | 验收标准 | Tech SPEC 对应位置 | 一致性状态 | 问题引用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| F-001 | local-first 控制面与本地 tracker | 无外部 tracker 管理 agent 工程流 | `symphony init/serve` | Go daemon + dashboard + SQLite + orchestrator | 禁止 Linear/remote/RBAC | 本地 tracker 可完整主路径 | §1, §3.1, §3.2, §4.1, §17, §20 | 一致 | PRD-REV-003 |
| F-002 | 项目初始化 | 创建本地项目运行环境 | operator 执行 `symphony init` | 创建项目、默认 workflow、DB | 非 Git repo、权限、冲突、DB version 不支持 | init 可用、DB 创建、workflow 可校验 | §7.1-7.5, §11.2, §19 M0 | 已修复 | PRD-REV-004 |
| F-003 | Issue 创建/编辑/展示 | 建立本地任务 source of truth | 创建 Inbox issue | 写入 issue、sequence、history/event | 非法输入 no mutation | 可创建 LOC-1，字段完整返回 | §7.6, §7.7, §7.8, §12.5, §15.4 | 已修复 | PRD-REV-005, PRD-REV-021 |
| F-004 | Issue 必填字段校验 | 防止不完整任务进入执行 | Inbox -> Ready 或 dispatch | 校验 title/description/AC/priority | 无效返回 `invalid_request` 或 transition 失败 | Ready/dispatch 前字段有效 | §8.2, §8.7, §12.5 | 一致 | - |
| F-005 | labels/comments/blockers/relations | 支持分类、讨论、依赖 | issue 更新、评论、blocker 操作 | 写 labels/comments/relations | active blocker 阻止 dispatch | blocker/duplicate relation 可见且可解除 | §7.6, §7.8, §7.9, §12.5 | 已修复 | PRD-REV-005, PRD-REV-021 |
| F-006 | Issue 状态机 | 明确生命周期 | operator/orchestrator/finalizer 动作 | 状态按 PRD §9 流转 | 非法流转拒绝 | 状态与 side effect 符合 PRD | §8.1, §8.2, §12.12 | 已修复 | PRD-REV-006, PRD-REV-015, PRD-REV-021, PRD-REV-022, PRD-REV-024, PRD-REV-028, PRD-REV-029 |
| F-007 | Terminal issue reopen | 重新整理或重新执行终态任务 | Done/Cancelled/Duplicate reopen | 只能到 Inbox/Ready，保留历史/workspace | 禁止直接到 Working/Human Review/Rework/Blocked | reopen 清 pause，不复用旧 run，Duplicate relation 需通过 remove 入口单独解除，改 canonical 前必须先 remove 旧 relation，remove 后可重新指定 canonical | §7.6, §8.2, §12.5, §18.5 | 已修复 | PRD-REV-021, PRD-REV-028, PRD-REV-029 |
| F-008 | Shared DispatchIssue preflight | 手动和 scheduler 同规则 | scheduler tick 或 manual dispatch | 校验 state、字段、pause、blocker、active run、workflow、concurrency | 失败不创建 run/workspace/process | 失败原因与 ApiErrorCode 对齐 | §8.3, §8.6, §8.7, §12.5 | 一致 | - |
| F-009 | Scheduler tick 与并发 | 可控调度 | daemon tick | reconcile、revalidate、slot、query、claim、launch | workflow invalid 跳过 dispatch | bounded concurrency，按 priority 排序 | §8.6, §8.7, §7.9 | 一致 | - |
| F-010 | 手动 dispatch pause/resume | operator 控制是否继续调度 | CLI/API pause/resume | 写入或清除 dispatch_paused 与 reason | active run、终态、blank reason 拒绝 | 不改变 state，不自动运行 | §12.5, §11.2, §8.14 | 已修复 | PRD-REV-013 |
| F-011 | 自动失败 pause | 失败后避免自动重复运行 | failure/cancel/missing/block/security deny | 恢复 source state，设置 paused reason | canonical reason 不得自由文本 | dashboard/CLI 可诊断 pause 原因 | §8.9-8.13, §8.10, §12.12 | 已修复 | PRD-REV-016 |
| F-012 | Active run reconciliation | 状态变化时取消无效 active run | active run issue 离开 Ready/Working/Rework | cancel run、pause、revoke token、保留 workspace | terminal/non-terminal 使用不同 code | 不把无效 run 推入 Human Review | §8.8, §8.14, §17 | 一致 | - |
| F-013 | Startup stale run / inconsistent Working | daemon 重启后边界可诊断 | daemon startup | 标记 stale run failed；修复或 pause Working issue | 不自动 redispatch | diagnostics 给出 operator remediation | §8.15, §16.3, §18.5 | 一致 | - |
| F-014 | 每 issue 独立 workspace/worktree/branch | 隔离 Codex 执行 | dispatch claim 后 | 创建或复用 worktree，生成 branch/base_sha | path/branch conflict 失败 | 不污染主 repo | §9.1-9.4, §7.6 | 一致 | - |
| F-015 | workspace 保留且不发布 | 避免自动破坏或远程副作用 | 任意 terminal/failure/rework | 不 reset/clean/delete/rebase/push/PR/merge | 禁止暴露相关 API/CLI | Done 不触发发布或清理 | §3.2, §9.4, §12.11, §14.8, §20 | 一致 | - |
| F-016 | WORKFLOW.md 配置合同 | repo-owned workflow | startup/reload/dispatch | 解析 front matter、defaults、strict validation | invalid 阻断 dispatch，保留 last valid | 配置项与 hard constraints 生效 | §6.1-6.5, §12.10, §18.3 | 已修复 | PRD-REV-011 |
| F-017 | Prompt 渲染与默认 prompt | 约束 agent 行为 | dispatch/run build prompt | Runtime Envelope + Tool Manifest + prompt + context + handoff contract | unknown variable/filter 失败；agent 不得自动 commit | 默认 prompt 包含 no-push/no-PR/no-Done/no-automatic-commit/stdin handoff，manual commit 只是外部 operator 例外且不引入 Symphony commit 功能 | §6.6, §6.7, §18.7 | 已修复 | PRD-REV-014 |
| F-018 | Codex fixture gate | 避免未知协议运行 | real Codex run | 先检测版本和 fixture metadata，再启动 | unsupported 预启动失败 | unsupported version -> `unsupported_codex_version` | §10.1-10.2, §19 M7, §20 | 已修复 | PRD-REV-002 |
| F-019 | Codex lifecycle/events/timeouts | 可观察 agent 执行 | Codex run 启动 | handshake、thread、turn、approval bridge、timeout/cancel | protocol/timeout/stall 映射 failure_code | events normalized，失败 pause | §10.3-10.7, §16.1 | 一致 | - |
| F-020 | Fake runner 默认测试 | CI 不依赖真实 Codex | 默认测试 | 使用 fake scenarios 覆盖主路径/失败 | Real Codex 只 opt-in | default CI 不需 Codex | §10.8, §18.1-18.5, §20 | 已修复 | PRD-REV-002, PRD-REV-020 |
| F-021 | Tool Gateway 固定 registry | agent 受控修改系统 | `symphony tool ...` | IPC + token + scope + schema + registry | 禁止 Done/delete/push/PR/secrets | 只允许固定工具 | §11.3-11.5, §13.3 | 一致 | - |
| F-022 | issue.get/comment/block tools | agent 读取/评论/阻塞当前 issue | tool call | current issue only；comment；block 当前 issue | block 不创建 blocker relation | block -> Blocked + cancelled/agent_blocked + pause | §11.5, §8.8, §18.5 | 一致 | - |
| F-023 | artifact.attach tool | agent 提交 artifact | tool call | workspace 相对路径、size、containment | protected/path traversal hard deny | protected path 只 failed tool_call，不直接终止 run | §11.5, §13.6, §13.8, §18.2 | 一致 | - |
| F-024 | followup.create tool | agent 创建 follow-up Inbox issue | tool call | 新 issue Inbox + followup_of relation | 不能设 Ready/Done/blocks/duplicates | follow-up 受当前 run scope 限制，`followup_of` / `followups` 在 Issue API 和 Issue Detail 可见 | §11.5, §7.6, §15.4 | 已修复 | PRD-REV-007, PRD-REV-023 |
| F-025 | handoff.submit two-stage payload | agent 交接结果 | run 完成前 tool call | 校验 payload，记录 handoff | target 只能 Human Review | submit 不直接流转状态 | §11.5, §11.6, §7.6, §8.13 | 已修复 | PRD-REV-008, PRD-REV-017 |
| F-026 | Handoff 幂等与冲突 | 防止重复/篡改交接 | 同一 run 多次 submit | canonical payload hash；首个 wins | 不同 hash -> conflict/exit 7 | conflict 不生成 review、不进 Human Review | §11.6, §18.3, §18.5 | 一致 | - |
| F-027 | Missing handoff continuation | 给 agent 一次专用补交机会 | main turn 无 handoff | 默认一次 continuation；0 则直接终止 | 仍缺失 -> completed_without_handoff + pause | 默认和 0 配置路径均验收 | §6.6, §8.11, §17, §18.5 | 一致 | - |
| F-028 | after_run finally 保证 | terminal outcome 后运行 hook | workspace 已准备且 worker 结束 | 所有 terminal outcome 尝试 after_run | hook 失败只 event/diagnostic，除非导致 packet 失败 | Human Review 前 after_run 已尝试 | §8.12, §9.5, §14.1 | 一致 | - |
| F-029 | Human Review gate/finalizer | 只有可复核交付进入 review | handoff exists + terminal outcome | after_run、review packet、run completed、state Human Review | 更高优先级取消/失败阻断 | packet generated 才能 Human Review | §8.13, §8.14, §14.7 | 已修复 | PRD-REV-016 |
| F-030 | Review packet 文件与 schema | reviewer 获取可复核材料 | finalizer 生成 packet | 写 review.md/json/patch/changed/untracked 等 | critical 缺失不得 generated | 包含 diff、tests、risks、tool/approval/prompt metadata | §14.1-14.6, §7.6, §9.7 | 一致 | - |
| F-031 | Untracked files guarantee | 不漏新文件 | review generation | untracked 写入 changed-files/untracked json，尽量 patch | 大/二进制/策略限制记录 reason | 普通 untracked 文本进 patch | §9.7, §14.6, §18.5 | 一致 | - |
| F-032 | Review API + Artifact API | Dashboard 安全展示 packet | 打开 Review Packet | Review API 返回 summary/artifact ids；Artifact API 取内容 | raw prompt/log/secret content_url=null 或 refusal | 不直接读 filesystem | §12.8, §12.9, §14.4, §15.4 | 一致 | - |
| F-033 | Dashboard 页面 | 本地可视化控制面 | browser 打开 localhost | Overview/Board/Issue/Run/Approval/Review/Workflow/Diagnostics | 不直读 DB/Git/filesystem/Codex | 页面职责与 REST/SSE 对齐，duplicate relation remove 与 shared states 有测试/验收覆盖 | §15.1-15.4, §13.1.1, §18.4, §19 M6 | 已修复 | PRD-REV-003, PRD-REV-010, PRD-REV-021, PRD-REV-025 |
| F-034 | Approval Inbox decision 语义 | operator 审批命令/文件/网络 | approval pending | approve_once/run/session、deny、cancel_run；pending 过期后 `approval_timeout` | 非 pending/expired -> `approval_not_pending` no mutation | deny 不等于 cancel_run；timeout 会失败并 pause dispatch | §10.6, §10.7, §12.7, §13.4, §15.4 | 已修复 | PRD-REV-009 |
| F-035 | REST/SSE API 合同 | CLI/dashboard 统一访问 | API request/SSE connect | envelope、endpoint surface、SSE replay id=seq | 禁止隐藏 excluded APIs | OpenAPI 覆盖且排除 forbidden routes | §12.1-12.12, §18.6-18.7 | 部分待审 | PRD-REV-019 |
| F-036 | Operator CLI | 终端操作系统 | `symphony ...` | issue/run/approval/review/workflow/diagnostics 命令 | exit code 0-9；review path metadata only | help 不暴露 v1 禁止能力 | §11.1-11.2, §18.7 | 已修复 | PRD-REV-004, PRD-REV-005 |
| F-037 | Loopback/session/CSRF/CLI bearer/open token | 本地入口鉴权 | serve/open/API/CLI | cookie+CSRF、bearer、open-token exchange | 未认证仅 bootstrap | token hash-only，runtime descriptor 无 secret | §12.3, §13.1.1, §13.2, §18.2 | 一致 | PRD-REV-003 |
| F-038 | Tool token 生命周期 | 限制 agent 工具权限 | run 创建 tool token | 绑定 project/issue/run/workspace/tools/expiry | terminal/cancel/reconcile/shutdown revoke | 不赋予 REST operator 权限 | §13.3, §11.5 | 一致 | - |
| F-039 | Command policy | 控制 shell command 风险 | Codex command approval | allow/review/deny + protected/network override | deny/terminal denial 映射 failure_code | command_denied 写 approval/pause | §13.4, §10.6, §18.2 | 一致 | - |
| F-040 | Network policy | 默认阻断未知网络 | Codex network request | allowlist allow；default deny auto-deny | default deny 不进 Approval Inbox | review 模式才 pending | §13.5, §18.2 | 一致 | - |
| F-041 | Protected paths | 防止敏感路径暴露 | file read/write 或 artifact.attach | Codex-mediated auto-deny；tool attach hard deny | 两种拒绝语义不同 | protected_path_denied 或 failed tool_call | §13.6, §13.1, §11.5 | 一致 | - |
| F-042 | Redaction/containment/diagnostics export | 安全展示与导出 | API/artifact/diagnostics/review | redacted-only，路径限制到 artifacts/exports | raw prompt/log/secret refusal | export 不支持 raw logs | §13.7-13.8, §12.9-12.10, §16.3 | 一致 | - |
| F-043 | Observability/diagnostics | 失败可诊断 | dashboard/CLI diagnostics | normalized run_events、logs、diagnostics schema | raw Codex logs 不作为 UI timeline | 显示 failure_code、paths、workflow、Codex/Git/DB | §16.1-16.3, §10.5 | 已修复 | PRD-REV-018 |
| F-044 | Send to Rework | 人工要求返工 | issue Human Review | 校验 latest generated packet/no active/reason | blank/non-HR/missing packet/active run 拒绝 | state -> Rework，保留 workspace/branch | §14.9, §12.8, §8.2 | 一致 | - |
| F-045 | Rework 新 packet 与 prompt 上下文 | 返工继续同 workspace | Rework dispatch | 复用 workspace/branch/base_sha；prompt 含 reason + previous summary | summary 仅 safe/redacted metadata | 新 immutable cumulative packet | §14.9, §9.4, §18.5 | 一致 | - |
| F-046 | Mark Done | operator 最终接受 | issue Human Review | 校验 latest generated packet/no active/reason | 非 HR/blank/packet mismatch 拒绝 | state -> Done，completed_at，且不 commit/push/merge/cleanup | §14.8, §12.8, §8.2 | 一致 | - |
| F-047 | 可执行合同交付物 | implementation agent 有合同输入 | 文档包/CI | OpenAPI、SQL、JSON Schema、work orders、testing、codex docs | 冲突需先修合同 | CI 阻断合同不一致 | §2.1, §18.7, §20 | 部分待审 | PRD-REV-019 |
| F-048 | 平台与发布验收 | 明确 v1 支持和完成定义 | release | macOS/Linux 支持；single binary；default tests | Windows best-effort | DoD 全部满足才 v1 done | §4A, §18.1-18.7, §20 | 已修复 | PRD-REV-012, PRD-REV-020 |

## Tech SPEC 到 PRD 反向映射

| Tech SPEC 章节 | 技术能力或合同 | 是否产品可见 | PRD 对应位置 | 一致性状态 | 问题引用 |
| --- | --- | --- | --- | --- | --- |
| §1-3 | 技术摘要、规范性约定、实现边界 | 是 | PRD §1, §3, §5, §6 | 一致 | - |
| §4-4A | 架构、进程模型、runtime descriptor、平台范围 | 是 | PRD §1, §6, §8.10, §8.11 | 已修复 | PRD-REV-003, PRD-REV-012 |
| §5 | 推荐仓库结构 | 否，开发组织合同 | PRD §8A | 一致 | - |
| §6 | WORKFLOW.md、配置、prompt 渲染 | 是 | PRD §8.5, §13 | 已修复 | PRD-REV-011, PRD-REV-014 |
| §7 | DB、事务、NormalizedIssue、eligibility | 是 | PRD §8.1, §9, §10 | 已修复 | PRD-REV-005, PRD-REV-017, PRD-REV-021, PRD-REV-023, PRD-REV-028, PRD-REV-029 |
| §8 | Issue 状态机和 run lifecycle | 是 | PRD §9, §10, §11 | 已修复 | PRD-REV-006, PRD-REV-015, PRD-REV-016, PRD-REV-021, PRD-REV-022, PRD-REV-024, PRD-REV-028 |
| §9 | Workspace / Git / diff | 是 | PRD §8.3, §8.8, §11 | 一致 | - |
| §10 | Codex adapter / fake runner / approval bridge | 是 | PRD §8.4, §8.9, §13 | 已修复 | PRD-REV-002, PRD-REV-009 |
| §11 | Tool Gateway 与 CLI | 是 | PRD §8.6, §8.7, §8.10 | 已修复 | PRD-REV-004, PRD-REV-007, PRD-REV-008, PRD-REV-021, PRD-REV-023 |
| §12 | REST/SSE API | 是 | PRD §8.10, §9, §10, §11 | 已修复 | PRD-REV-005, PRD-REV-006, PRD-REV-009, PRD-REV-021, PRD-REV-022, PRD-REV-024, PRD-REV-028, PRD-REV-029 |
| §13 | Security model | 是 | PRD §8.11, §13 | 一致 | PRD-REV-003 |
| §14 | Review packet and rework | 是 | PRD §8.8, §11 | 一致 | - |
| §15 | Dashboard requirements | 是 | PRD §8.9 | 已修复 | PRD-REV-003, PRD-REV-010, PRD-REV-021, PRD-REV-023, PRD-REV-025 |
| §16 | Observability and diagnostics | 是 | PRD §7.4, §8.11, §12 | 已修复 | PRD-REV-018 |
| §17 | Upstream-vs-Local resolution | 是 | PRD §3, §6 | 一致 | - |
| §18-20 | 测试、实施阶段、Definition of Done | 是，发布/验收可见 | PRD §8A, §12, §13 | 部分待审 | PRD-REV-001, PRD-REV-019, PRD-REV-020, PRD-REV-025, PRD-REV-028, PRD-REV-029 |

## 状态说明

- `一致`：PRD 与 Tech SPEC 的产品事实和技术合同匹配。
- `已修复`：本轮审查发现缺口，已在 `PRD.md` 或 `TECH_SPEC.md` 中补齐。
- `部分待审`：PRD/Tech SPEC 可映射，但 executable contracts 尚未在本轮逐项验证。
- `缺失`：一侧定义了能力，另一侧没有可追踪定义。
- `冲突`：两侧对同一行为、状态、字段或边界的约定不一致。
- `不明确`：可找到关联，但缺少足够细节，无法作为实现或验收依据。
