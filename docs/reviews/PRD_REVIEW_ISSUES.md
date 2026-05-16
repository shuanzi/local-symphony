# PRD 审查问题清单

> 状态：已完成首轮合并。问题来源包括功能索引、产品合理性、定义清晰度、PRD-SPEC 双向一致性和端到端场景走查。

## 摘要

| 严重级别 | 数量 | Fixed | Open | Deferred |
| --- | ---: | ---: | ---: | ---: |
| Blocker | 0 | 0 | 0 | 0 |
| Major | 25 | 24 | 0 | 1 |
| Minor | 4 | 3 | 1 | 0 |

## 问题清单

### PRD-REV-001

```text
来源位置: PRD.md §5/§8A/§13; TECH_SPEC.md §3.1/§18/§19/§20
问题类型: 产品合理性 / 当前版本范围
严重级别: Major
当前描述: v1 同时要求 tracker、orchestrator、Codex adapter、dashboard、REST/SSE、CLI、security、contract artifacts、M0-M8 全量验收。
问题说明: 这可能超过“小型 MVP”的最小闭环，但也可能是已确认的 release-hardening 范围。
建议修改: 若 v1 要收敛为 MVP，应拆分 blocking scope 与 hardening scope；若保持当前范围，应在产品决策中确认 v1 是完整 release target，而非 prototype。
是否需要产品决策: 是
影响到的 TECH_SPEC 部分: §3.1, §18, §19, §20
状态: Deferred
```

### PRD-REV-002

```text
来源位置: PRD.md §7.1/§8.4/§12/§13; TECH_SPEC.md §10/§19 M7/§20
问题类型: 定义不清
严重级别: Major
当前描述: 主路径描述启动 Codex app-server，但成功指标和 CI 又强调 fake runner。
问题说明: 未明确真实 Codex adapter 是 v1 release scope、可选能力还是测试外能力。
建议修改: 明确 real Codex adapter 属于 v1 release scope；默认 CI 和主验收可使用 fake runner，真实 Codex 测试 opt-in。
是否需要产品决策: 否，本轮按现有 TECH_SPEC 方向收敛。
影响到的 TECH_SPEC 部分: §10, §18, §20
状态: Fixed
```

### PRD-REV-003

```text
来源位置: PRD.md §4; TECH_SPEC.md §4.4/§13/§15
问题类型: 产品合理性 / 目标用户边界
严重级别: Major
当前描述: PRD 面向“小团队 operator”，但 v1 明确不是 remote dashboard / RBAC。
问题说明: “小团队”容易引出多人登录、共享 DB、操作者身份和审查责任归属。
建议修改: 收窄为单机本地 operator，以及小团队中负责运行本地控制面的 operator。
是否需要产品决策: 否，本轮按 v1 本地单机边界收敛。
影响到的 TECH_SPEC 部分: §4.4, §13, §15
状态: Fixed
```

### PRD-REV-004

```text
来源位置: PRD.md §7.1; TECH_SPEC.md §7.3/§11.2/§16.3
问题类型: 定义不清 / 缺少边界场景
严重级别: Major
当前描述: `symphony init` 只在主路径中出现，缺少文件产物、幂等、非 Git repo、权限和冲突行为。
问题说明: 首次使用不可验收，失败后 operator 不知道下一步。
建议修改: 补充 init preflight、side effects、幂等、失败错误和成功输出。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §7.3, §11.2, §16.3
状态: Fixed
```

### PRD-REV-005

```text
来源位置: PRD.md §8.1/§8.9/§8.10; TECH_SPEC.md §7.8/§12.5/§15.4
问题类型: 定义不清 / 缺少边界场景
严重级别: Major
当前描述: Issue create/update/list/comment/labels 及大量数据分页过滤规则不足。
问题说明: Board、CLI list 和 API 在空数据、大量数据、非法输入时不可验收。
建议修改: 补充字段级规则、label normalization、filter/sort/pagination、空列表语义和错误映射。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §12.5, §15.4
状态: Fixed
```

### PRD-REV-006

```text
来源位置: PRD.md §9; TECH_SPEC.md §8.2/§12.5/§15.4
问题类型: 定义不清 / 缺少边界场景
严重级别: Major
当前描述: 通用 `/transition` 请求体、reason 规则、重复状态流转、issue Cancelled 和 Duplicate 关系语义不完整。
问题说明: issue 级取消和 run 级取消容易混淆；Duplicate relation 的输入和幂等不清。
建议修改: 补充 transition body、reason guard、same-state conflict、Duplicate relation guard 与 issue/run cancel 文案区分。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §8.2, §12.5, §15.4
状态: Fixed
```

### PRD-REV-007

```text
来源位置: PRD.md §8.6/§8.7/§11.4; TECH_SPEC.md §11.4/§11.5/§14
问题类型: 定义不清 / 职责重叠
严重级别: Major
当前描述: `followup.create` 会创建 issue，`handoff.submit.followups` 也叫 followups。
问题说明: 不清楚 handoff followups 是否自动创建 issue，是否与 tool-created follow-up 去重。
建议修改: 明确 `followup.create` 才真实创建 Inbox issue；handoff followups 只是 Review Packet 建议事项。
是否需要产品决策: 否，本轮按最小副作用收敛。
影响到的 TECH_SPEC 部分: §11.5, §14
状态: Fixed
```

### PRD-REV-008

```text
来源位置: PRD.md §8.7; schemas/tools/handoff_submit.input.schema.json
问题类型: 定义不清
严重级别: Major
当前描述: handoff payload 必填字段明确，但空数组、空字符串和 UI 空态缺少产品说明。
问题说明: reviewer 无法判断空 tests/risks/verification 的含义。
建议修改: 明确数组字段必需、可为空，元素规则以 schema 为准；Review Packet UI 必须展示明确空态。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §11.5, §14
状态: Fixed
```

### PRD-REV-009

```text
来源位置: PRD.md §8.9/§13; TECH_SPEC.md §10.6/§10.7/§12.7/§13
问题类型: 缺少边界场景
严重级别: Major
当前描述: Approval decision 枚举清楚，但 timeout/expired 后 run、pause、UI、重复 decide 语义未在 PRD 中定义。
问题说明: pending approval 过期路径不可验收。
建议修改: 增加 `approval_timeout` run failure、pause reason、expired approval 展示和 later decide conflict。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §10.6, §10.7, §12.7
状态: Fixed
```

### PRD-REV-010

```text
来源位置: PRD.md §8.9; TECH_SPEC.md §15.4
问题类型: 缺少边界场景
严重级别: Major
当前描述: Dashboard 页面职责已列出，但 loading、empty、auth/error、artifact refusal、daemon unavailable 状态缺失。
问题说明: 首次使用、SSE 断线、401/403/CSRF、无 review/approval 等情况不可验收。
建议修改: 为所有页面补充共享 UI 状态合同。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §12, §15.4
状态: Fixed
```

### PRD-REV-011

```text
来源位置: PRD.md §8.5/§8.9/§8.10; TECH_SPEC.md §6.5/§6.6/§12.10/§15.4
问题类型: 定义不清
严重级别: Minor
当前描述: Workflow validate/reload/render preview 的 operator 使用路径不足。
问题说明: validate 是否更新 effective config、invalid reload 是否保留 last valid config 等容易混淆。
建议修改: 补充 validate/render-preview/reload 的产品路径和失败行为。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §6.5, §12.10, §15.4
状态: Fixed
```

### PRD-REV-012

```text
来源位置: PRD.md §6; TECH_SPEC.md §4A/§18/§20
问题类型: PRD-SPEC 漂移 / 技术合同缺口
严重级别: Major
当前描述: PRD 把 runtime/build/test matrix 委托给 TECH_SPEC §4A，但 §4A 原本只列平台。
问题说明: release/CI 无法判断平台支持是否通过。
建议修改: 补齐 runtime contract、macOS/Linux build/test matrix、Windows best-effort 非阻断规则。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §4A, §18, §20
状态: Fixed
```

### PRD-REV-013

```text
来源位置: PRD.md §6/§10; TECH_SPEC.md §7.6/§12.5/§15.4
问题类型: 产品合理性 / 隐藏范围
严重级别: Major
当前描述: v1 没有 Archive 状态或入口，但 dispatch control guard 提到 archived issue。
问题说明: 这暗示隐藏 archive 能力。
建议修改: 明确 archive/delete lifecycle 是 v1 非目标；`archived_at` 为 future-reserved，不参与 v1 guard。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §7.6, §12.5, §15.4
状态: Fixed
```

### PRD-REV-014

```text
来源位置: PRD.md §6/§8.5; TECH_SPEC.md §6.2/§6.7/§18.7
问题类型: 定义不清 / 用户可见行为边界
严重级别: Major
当前描述: PRD 禁止自动 commit，但默认 prompt 允许 operator 外部明确要求时 commit。
问题说明: 不清楚 Symphony 是否提供 commit 自动化，以及 commit 后 review packet 如何计算。
建议修改: 明确 v1 不提供 commit API/CLI/dashboard，不自动创建/修改/push commit；若 workspace 中存在 commit，review packet 仍按当前 tree 对 base_sha 生成。
是否需要产品决策: 否，本轮保留现有 manual prompt 例外但关闭 Symphony 自动化边界。
影响到的 TECH_SPEC 部分: §6.2, §6.7, §9.7, §18.7
状态: Fixed
```

### PRD-REV-015

```text
来源位置: PRD.md §9; TECH_SPEC.md §8.2
问题类型: PRD-SPEC 漂移
严重级别: Major
当前描述: PRD 显式列出 `Working -> Ready/Rework` 失败回源路径；TECH_SPEC §8.2 allowed transitions 总表原本缺这条边。
问题说明: 实现或测试若只看 §8.2 会漏掉失败恢复状态。
建议修改: 在 TECH_SPEC §8.2 增加 `Working -> Ready/Rework` 行并引用 §8.10/§8.11/§8.13/§8.15。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §8.2
状态: Fixed
```

### PRD-REV-016

```text
来源位置: PRD.md §10; TECH_SPEC.md §8.13
问题类型: PRD-SPEC 漂移 / 错误处理
严重级别: Major
当前描述: PRD 要求 review packet failure 自动 pause 写 reason/paused_at；TECH_SPEC §8.13 原本只写 dispatch_paused 和 failure_code。
问题说明: finalizer failure path 字段不完整。
建议修改: 在 TECH_SPEC §8.13 显式写 `dispatch_pause_reason=review_packet_failed` 和 `dispatch_paused_at=now`。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §8.13
状态: Fixed
```

### PRD-REV-017

```text
来源位置: PRD.md §8.7; TECH_SPEC.md §7.7/§11.5
问题类型: PRD-SPEC 漂移 / 用户可见副作用
严重级别: Major
当前描述: PRD 说 `handoff.submit` 只记录 handoff 数据；TECH_SPEC transaction 原本包含 insert issue comment。
问题说明: issue comment 是用户可见状态，且与 `issue.comment` tool 重叠。
建议修改: 明确 `handoff.submit` 不自动写 comment；需要普通评论时 agent 必须显式调用 `issue.comment`。
是否需要产品决策: 否，本轮按最小副作用收敛。
影响到的 TECH_SPEC 部分: §7.7, §11.5
状态: Fixed
```

### PRD-REV-018

```text
来源位置: PRD.md §6/§8.10; TECH_SPEC.md §7.3/§16.3
问题类型: 缺少边界场景
严重级别: Minor
当前描述: v1 不做 migration/rollback，但 unsupported DB version 的 operator 恢复路径缺少产品文案。
问题说明: 技术拒绝明确，用户下一步不明确。
建议修改: 增加只读失败、检测版本、期望版本、DB 路径和人工恢复指引。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §7.3, §16.3
状态: Fixed
```

### PRD-REV-019

```text
来源位置: PRD.md §8A/§13; TECH_SPEC.md §2.1/§18.7/§20
问题类型: 剩余覆盖风险
严重级别: Minor
当前描述: 本轮主审查对象是 PRD 与 TECH_SPEC；OpenAPI、SQL schema、JSON Schema、docs/testing、docs/codex、docs/agent_work_orders 只作为引用，没有逐项审查。
问题说明: 即使 PRD 与 TECH_SPEC 对齐，第三层 executable contracts 仍可能漂移。
建议修改: 下一轮按同一矩阵方法审查 executable contracts 与 TECH_SPEC/PRD 的一致性。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §2.1, §18.7, §20
状态: Open
```

### PRD-REV-020

```text
来源位置: TECH_SPEC.md §18.4/§18.5/§19
问题类型: 缺少验收覆盖
严重级别: Major
当前描述: 本轮新增 init failure、issue list/filter/pagination、handoff 不写 implicit comment、approval timeout 等行为后，测试/验收清单原本没有同步覆盖。
问题说明: 正文合同已修复，但如果测试策略不更新，后续实现可能再次漂移。
建议修改: 在 integration tests、fake-agent E2E 和 M0/M1/M4/M6 acceptance 中补充对应测试项。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §18.4, §18.5, §19
状态: Fixed
```

### PRD-REV-021

```text
来源位置: PRD.md §9; TECH_SPEC.md §8.2/§12.5/§15.4
问题类型: 定义不清 / 缺少用户可见入口
严重级别: Major
当前描述: Duplicate relation 可以把 issue 标记为 Duplicate，但缺少解除 Duplicate relation 或恢复误标的入口。
问题说明: operator 误标后没有明确的产品路径恢复关系与状态，Dashboard/CLI/API 的行为不可验收。
建议修改: 明确 Duplicate relation 的解除入口、允许状态、审计事件、Dashboard/CLI 可见动作和失败语义。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §8.2, §12.5, §15.4
状态: Fixed
```

### PRD-REV-022

```text
来源位置: PRD.md §9; TECH_SPEC.md §8.2/§12.5
问题类型: 定义不清 / 幂等冲突
严重级别: Major
当前描述: Duplicate relation 的重复提交、目标变化和 same-state transition 之间的幂等/冲突规则仍不够明确。
问题说明: 实现可能把同一 Duplicate 目标视为成功、冲突或重复事件；不同 Duplicate 目标的错误码和 mutation 边界也不清晰。
建议修改: 补充 Duplicate 幂等规则、不同 target 的 conflict 语义、事件写入规则和 no mutation 保证。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §8.2, §12.5
状态: Fixed
```

### PRD-REV-023

```text
来源位置: PRD.md §8.6/§8.7; TECH_SPEC.md §7.6/§11.5/§15.4
问题类型: PRD-SPEC 漂移 / 用户可见性
严重级别: Major
当前描述: `followup.create` 创建 followup relation，但关系在 issue detail、Board 或 CLI/API 展示中的可见性不足。
问题说明: followup issue 被创建后，operator 可能无法从父 issue 或子 issue 追踪上下文，审查和后续调度不可诊断。
建议修改: 明确 followup relation 的双向展示、API 字段、CLI 输出和 Dashboard issue detail 可见位置。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §7.6, §11.5, §15.4
状态: Fixed
```

### PRD-REV-024

```text
来源位置: PRD.md §9/§10; TECH_SPEC.md §8.2/§12.5
问题类型: 定义不清 / 副作用缺口
严重级别: Major
当前描述: transition reason 的校验规则已有补充，但不同 reason 对 comment、history、pause、timestamps 等副作用的要求仍不完整。
问题说明: 同一状态流转可能在 API、CLI、Dashboard 中写出不同审计结果，operator 无法复查为什么发生 transition。
建议修改: 为 transition reason 补齐副作用矩阵，包括必填/可选 reason、history event、comment 写入、pause 字段和 no mutation 场景。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §8.2, §12.5
状态: Fixed
```

### PRD-REV-025

```text
来源位置: TECH_SPEC.md §15.4/§18.4/§18.5/§20
问题类型: 缺少验收覆盖
严重级别: Major
当前描述: Dashboard shared states 已补充 loading/empty/auth/error/refusal/daemon unavailable 等产品状态，但对应测试覆盖不足。
问题说明: UI 状态合同如果没有集成或 E2E 验收，后续实现容易只覆盖主路径页面。
建议修改: 在 Dashboard/API 集成测试和 fake-agent E2E 中补充 Dashboard shared states 的验收项。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §15.4, §18.4, §18.5, §20
状态: Fixed
```

### PRD-REV-026

```text
来源位置: docs/reviews/PRD_TECH_REVIEW_PLAN.md
问题类型: 复审产物闭环 / 状态声明过满
严重级别: Major
当前描述: 计划 checklist 曾把“修复已确认问题”和“复审完成”全部勾选，但 `PRD-REV-001` 仍为 Major + Deferred 且需要产品决策。
问题说明: 审查产物容易被误读为所有 Major 均已业务决策闭环，实际仍存在 Deferred/Open 项。
建议修改: 调整计划文档，区分首轮审查与可直接修复项完成、Deferred/Open 剩余项、下一轮产品决策范围。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: 无
状态: Fixed
```

### PRD-REV-027

```text
来源位置: docs/reviews/PRD_TECH_REVIEW_MATRIX.md
问题类型: 可追踪性缺口
严重级别: Minor
当前描述: 矩阵漏挂 `PRD-REV-003` 到目标用户/权限/本地单机相关映射行，且漏挂 `PRD-REV-020` 到测试/验收相关映射行。
问题说明: Fixed issue 虽然在问题清单中存在，但无法从相关矩阵位置追踪回审查发现。
建议修改: 更新矩阵引用，保证 Fixed issue 至少在相关 PRD->Tech 和 Tech->PRD 映射位置可追踪。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: 无
状态: Fixed
```

### PRD-REV-028

```text
来源位置: PRD.md §9; TECH_SPEC.md §7.6/§8.2/§12.5/§18.5
问题类型: 定义不清 / 单值 DTO 约束缺口
严重级别: Major
当前描述: `duplicate_of` 是单一 active canonical relation，但 reopen 保留 duplicate relation 后，再次转 Duplicate 到另一个 canonical 的行为未定义。
问题说明: 实现可能为同一 source issue 产生多个 active duplicate canonical relation，导致 `NormalizedIssue.duplicate_of`、Dashboard remove action 和 API 幂等语义不确定。
建议修改: 明确同一 source issue 最多一个 active duplicate canonical；若要改变 canonical，必须先通过 duplicate relation remove 入口失效旧 relation，再重新 transition。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §7.6, §8.2, §12.5, §18.5
状态: Fixed
```

### PRD-REV-029

```text
来源位置: PRD.md §9; TECH_SPEC.md §7.6/§12.5/§18.5
问题类型: 定义不清 / 恢复路径不可执行
严重级别: Major
当前描述: 已要求更换 duplicate canonical 前先 remove 旧 relation，但 remove 不改变 issue.state；remove 后再转 Duplicate 到新 canonical 会被 same-state transition 默认冲突规则挡住。
问题说明: “先 remove 旧 relation 再设置新 canonical”的正确路径在合同上不可执行，导致误标恢复仍可能卡住。
建议修改: 明确 issue 已是 Duplicate 且当前无 active duplicate relation 时，带合法 `duplicate_of` 的 same-state Duplicate transition 可创建新 relation；同时补充正向验收。
是否需要产品决策: 否
影响到的 TECH_SPEC 部分: §7.6, §12.5, §18.5
状态: Fixed
```
