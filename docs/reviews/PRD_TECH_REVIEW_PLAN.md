# PRD 与 Tech SPEC 审查计划

## 目标

系统审查 `PRD.md` 的产品合理性、功能定义清晰度，以及它与 `TECH_SPEC.md` 的一致性。审查必须产出可定位、可修复、可复查的问题清单；可直接修复项完成后进行复审，需要产品决策或下一轮合同审查的事项保持显式 Deferred/Open。

## 输入

- `PRD.md`
- `TECH_SPEC.md`

## 输出

- `docs/reviews/PRD_TECH_REVIEW_MATRIX.md`：PRD 功能与 Tech SPEC 的双向映射矩阵。
- `docs/reviews/PRD_REVIEW_ISSUES.md`：结构化问题清单。
- 必要时更新 `PRD.md`。
- 必要时更新 `TECH_SPEC.md`。

## 执行 Checklist

- [x] 0. 冻结输入并确认分支与 `origin` 同步。
- [x] 1. 从 `PRD.md` 抽取功能索引，不在此阶段评价内容。
- [x] 2. 建立 PRD 功能到 Tech SPEC 的映射矩阵。
- [x] 3. 审查 PRD 功能合理性。
- [x] 4. 审查 PRD 功能定义是否清晰、可实现、可验收。
- [x] 5. 审查 PRD 与 Tech SPEC 的双向一致性。
- [x] 6. 按端到端用户场景走查边界路径。
- [x] 7. 合并、去重并分级所有问题。
- [x] 8. 修复首轮已确认且无需产品决策的问题，优先处理 Blocker 和可直接闭环的 Major。
- [x] 9. 复审已修复项，确认未引入新的 PRD-SPEC 漂移，并记录 Deferred/Open 剩余项。

## 复审迭代记录

- [x] 第一轮复审：发现 duplicate relation 移除、same-state Duplicate、follow-up 可见性、transition reason、Dashboard shared states 和审查文档状态问题。
- [x] 第一轮修复：补齐 PRD/TECH 主合同和 review docs 追踪。
- [x] 第二轮复审：将剩余 Major 收敛到 duplicate relation 在 Dashboard/DTO 不可发现，以及 `PRD-REV-019` 状态误标。
- [x] 第二轮修复：补齐 duplicate_of/duplicates 产品字段、NormalizedIssue、Issue Detail remove action、验收覆盖和矩阵追踪；`PRD-REV-019` 保持 Open。
- [x] 第三轮复审：发现 duplicate relation 单值 DTO 与多 active canonical 的潜在冲突。
- [x] 第三轮修复：明确同一 source issue 最多一个 active duplicate canonical，改 canonical 前必须先 remove 旧 relation，并补充验收与矩阵追踪。
- [x] 第四轮复审：发现 remove 不改 state 导致“remove 后指定新 canonical”的 same-state Duplicate 路径不可执行。
- [x] 第四轮修复：补充 no active duplicate relation 时的 same-state Duplicate 建 relation 例外、存储唯一性约束和正向验收。
- [x] 第五轮聚焦复审：确认本轮无新增 Major，剩余项仅为明确 Deferred/Open；同时修正 same-state Duplicate 省略 `duplicate_of` 的 Minor 歧义和 `PRD-REV-029` 矩阵追踪。

## 剩余决策与下一轮范围

- `PRD-REV-001` 仍为 Major + Deferred，需要产品 owner 决策确认 v1 是完整 release target 还是需要拆分 MVP/hardening scope；本轮不声明该业务决策已闭环。
- `PRD-REV-019` 仍为 Open，OpenAPI、SQL schema、JSON Schema、docs/testing、docs/codex、docs/agent_work_orders 等 executable contracts 需要下一轮由 implementation owner 逐项审查。
- 本轮复审新增的问题以 `PRD_REVIEW_ISSUES.md` 的 Open/In Progress 状态为准；只有能从当前文件验证的事项才可标为 Fixed。

## 分工方式

根代理负责：

- 创建和维护本计划。
- 协调子代理并合并结论。
- 做最终编辑、去重、分级和复审。

子代理分工：

- `explorer`：抽取功能索引，建立 PRD 到 Tech SPEC 的初始映射。
- `explorer`：审查产品合理性，只读分析。
- `explorer`：审查功能定义清晰度，只读分析。
- `explorer`：审查 PRD 与 Tech SPEC 双向一致性，只读分析。
- `explorer`：按端到端场景走查边界路径，只读分析。
- `reviewer`：在修复后进行只读复审。

## 审查矩阵字段

| 字段 | 说明 |
| --- | --- |
| 功能 ID | 稳定编号，例如 `F-001` |
| PRD 功能/场景 | 来自 `PRD.md` 的功能或用户场景 |
| 用户目标 | 用户为什么需要该功能 |
| 触发条件 | 什么情况下进入该功能 |
| 主流程 | 成功路径 |
| 异常/空状态 | 失败、空数据、加载中、权限不足、重复操作等 |
| 验收标准 | 可验证的完成标准 |
| Tech SPEC 对应位置 | `TECH_SPEC.md` 中对应章节、模块、接口或数据结构 |
| 一致性状态 | 一致 / 缺失 / 冲突 / 不明确 |
| 问题引用 | 关联 `PRD_REVIEW_ISSUES.md` 中的问题 ID |

## 问题记录格式

每个问题必须使用以下结构：

```text
ID:
来源位置:
问题类型: 产品合理性 / 定义不清 / PRD-SPEC 漂移 / 缺少边界场景
严重级别: Blocker / Major / Minor
当前描述:
问题说明:
建议修改:
是否需要产品决策:
影响到的 TECH_SPEC 部分:
```

## 严重级别

- `Blocker`：导致功能无法实现、验收无法判断，或 PRD 与 Tech SPEC 存在明确冲突。
- `Major`：导致实现歧义、边界行为不一致，或后续返工概率高。
- `Minor`：表达不清、命名不统一、补充说明类问题。

## 审查步骤

### 0. 冻结输入

- 运行 `git fetch origin`。
- 确认当前分支与 `origin` 同步。
- 记录当前 `PRD.md` 与 `TECH_SPEC.md` 为审查基线。

### 1. 功能索引

- 只从 `PRD.md` 抽取功能、用户场景、约束、验收标准。
- 为每个功能分配稳定 ID。
- 不在本阶段提出修改建议，避免混淆索引和审查。

### 2. 产品合理性审查

逐项检查：

- 用户问题是否明确。
- 是否属于当前版本范围。
- 是否存在重复、冲突或职责重叠。
- 是否存在过度设计。
- 是否缺少 non-goals。
- 是否缺少功能依赖关系说明。

### 3. 功能清晰度审查

逐项检查：

- 谁使用该功能。
- 在什么条件下使用。
- 用户能做什么、不能做什么。
- 正常路径、失败路径、取消路径、重试路径。
- 空数据、加载中、权限不足、重复操作的行为。
- 验收标准是否可测试。
- 是否存在未落地的模糊词，例如“支持”“尽量”“智能”“优化”“适当”“自动”“可配置”“用户友好”“高性能”。

### 4. PRD 与 Tech SPEC 一致性审查

执行双向映射：

- PRD 到 Tech SPEC：每项 PRD 功能是否有对应模块、数据结构、接口、状态定义、错误处理、权限或持久化策略。
- Tech SPEC 到 PRD：每项产品可见技术能力是否有对应 PRD 定义。

重点检查：

- 名词不一致。
- 状态机不一致。
- 权限模型不一致。
- 同步/异步约定不一致。
- 数据字段含义不一致。
- 错误处理不一致。
- 用户可见行为不一致。

### 5. 端到端场景走查

按用户路径检查：

- 首次使用。
- 核心成功路径。
- 用户取消。
- 操作失败。
- 重试。
- 空数据。
- 大量数据。
- 权限不足。
- 输入非法。
- 重复提交。
- 状态恢复。
- 版本升级或数据迁移，如适用。

### 6. 合并与修复

- 合并重复问题。
- 按 Blocker、Major、Minor 排序。
- 先修 PRD 定义问题，再修 PRD 与 Tech SPEC 漂移。
- 如果需要产品决策，明确记录，不擅自扩展范围。
- 修复后更新矩阵和问题清单状态。

### 7. 复审

复审必须确认：

- 没有未解决的 Blocker。
- Major 已修复，或有明确决策；若标为 Deferred，必须记录待产品决策和后续 owner，不计入本轮业务决策闭环。
- PRD 每个功能都有可验证验收标准。
- PRD 与 Tech SPEC 可以双向映射。
- 修复没有引入新的命名、状态、接口或边界行为漂移。

## 完成标准

- `PRD_TECH_REVIEW_MATRIX.md` 已覆盖所有 PRD 功能和 Tech SPEC 产品可见能力。
- `PRD_REVIEW_ISSUES.md` 中所有问题都有状态。
- `PRD.md` 与 `TECH_SPEC.md` 的修改范围最小且可追踪。
- 首轮复审结果已记录，Deferred/Open 项有明确后续范围，且最终 `git diff --check` 无空白错误。
