# Local Symphony WebUI Workbench Redesign

## 背景

当前 WebUI 已覆盖 issue、run、review、approval、workflow、diagnostics 的核心功能，但信息结构仍偏“页面合集”。Operator 需要在 Overview、Board、Issue detail、Run detail、Review、Diagnostics 之间跳转，才能回答三个高频问题：

- 现在最需要处理哪条 work item？
- 当前 issue 为什么不能继续，下一步能做什么？
- 刚执行的命令产生了哪些 run、event、review 或错误？

本设计采用已确认的 **A. 工作台优先** 方向，将首屏调整为一个 operator workbench。

## 目标

- 首屏直接呈现可行动工作队列，而不是先展示纯指标。
- 将 issue facts、relations、run timeline、review packet 摘要合并到同一上下文。
- 将当前状态下可用动作固定在右侧 action rail，减少在长页面里查找按钮。
- 保留现有 REST/SSE 数据边界，不新增后端依赖。
- 保留现有 Board、Workflow、Diagnostics 能力，但降低核心闭环中的页面切换成本。

## 非目标

- 不引入远程 dashboard、多用户权限、push/PR/merge、workspace 清理等 v1 禁止能力。
- 不重做 OpenAPI shape，也不改动 store/orchestrator 语义。
- 不引入大型 UI 组件库或新运行时依赖。
- 不把 raw prompt、raw Codex log、secret-like 内容暴露到 UI。

## 信息架构

### 顶部状态条

顶部保留项目与服务状态，但从品牌展示转为操作状态条：

- Project ID / repo root 简写。
- Workflow valid/invalid。
- SSE connected/reconnecting。
- Running runs、Human Review、Paused、Failed 的紧凑计数。
- Refresh 作为右侧固定按钮。

该区域的目的不是替代 dashboard 内容，而是让 operator 判断当前服务是否可信、数据是否实时。

### 左侧 Work Queue

左侧从九列横向 Board 改为优先级队列：

- **Needs action**：Human Review、Blocked、failed/completed_without_handoff 关联 issue、dispatch paused。
- **Ready to run**：Ready、Rework 且可 dispatch。
- **Watching**：Working、active run、pending approvals。
- **All issues**：按状态折叠汇总，保留完整 Board 入口。

队列 item 显示 identifier、title、state、priority、关键阻塞信号。点击 item 更新中间上下文，不强制跳整页。

### 中间 Context Panel

中间是当前 issue 的主要阅读区，按 operator 判断顺序组织：

1. Header：identifier、title、state、priority、labels、dispatch paused 状态。
2. Acceptance：description 和 acceptance criteria，默认展开。
3. Relations：blocked by、blocks、duplicate、follow-up，展示可点击 ref。
4. Timeline：最近 issue/run events、latest run、latest review packet。
5. Details：workspace/git、comments、完整 run history 可折叠。

当没有选中 issue 时，中间显示“下一步建议”：创建 issue、打开 Ready 队列、查看 Human Review。

### 右侧 Action Rail

右侧只展示当前上下文可执行动作：

- Ready/Rework：Dispatch、Pause dispatch。
- Dispatch paused：Resume dispatch。
- Human Review：Open review packet、Send to Rework、Mark Done。
- Any issue：Update issue、Add comment、Add/remove blocker、Transition。
- Diagnostics/Workflow：当 workflow invalid、daemon unavailable、failed run 时展示关联入口。

每个动作附近展示最近一次 command result 或错误，避免错误 banner 只出现在页面顶部而脱离动作上下文。

### 二级页面

保留以下页面，但作为专项视图：

- Board View：完整 Kanban 扫描和批量管理。
- Run Detail：深链 run 分析。
- Review Packet：完整 artifact/redaction 查看。
- Workflow：配置和 dry-run 工具。
- Diagnostics：系统诊断和 redacted export。

Workbench 是默认入口，专项页面用于深入分析。

## 组件边界

当前 `web/src/App.tsx` 已经过大。实现时先按信息结构拆分，不改变 API client：

- `Shell.tsx`：topbar、navigation、global banners。
- `WorkbenchPage.tsx`：三栏布局和 selected issue state。
- `WorkQueue.tsx`：队列分组与 item。
- `IssueContext.tsx`：issue facts、relations、timeline、history。
- `ActionRail.tsx`：状态驱动动作入口。
- `CommandFeedback.tsx`：局部命令反馈。

原有 `BoardPage`、`IssueDetailPage`、`RunDetailPage`、`ReviewPacketPage` 可先保留，逐步被 Workbench 复用。

## 数据流

- `useDashboardData` 继续作为数据入口，保留 `loadAll`、polling、SSE refresh。
- Workbench 使用 `issues`、`runs`、`events`、`approvals`、`workflow`、`diagnostics` 计算队列和推荐动作。
- 选中 issue 优先来自 route `#/issue/:ref`；没有 route 时选择第一个 Needs action，否则第一个 Ready to run。
- 右侧 action 使用现有 `runMutation`，成功后触发 `loadAll` 并保持选中 issue。
- 深链不存在或 API 失败时沿用现有 not found / failed states。

## 交互细节

- Create issue 改为按钮打开 inline panel 或 modal，不再固定占据 Board 首屏。
- Queue item 需要支持键盘 focus 和 Enter 打开。
- Action rail 中不可执行动作不展示；原因放在 contextual hint 中，例如 active run、paused、missing review packet。
- Command error 显示在 action rail 顶部，同时保留全局 banner，便于快速定位。
- Timeline 默认只显示最近 8 条，提供 Open run / Open review 深入入口。

## 视觉方向

采用安静、密集、可扫描的本地控制台风格：

- 降低卡片阴影和圆角，避免“卡片套卡片”。
- 使用固定三栏比例：左 280px，中间自适应，右 320px。
- 状态色保持克制：green 表示可继续，amber 表示需要人工判断，red 表示阻断。
- 首屏密度优先于大标题，不做营销式 hero。
- 移动端退化为队列、上下文、动作三段纵向布局。

## 测试计划

- `npx -y pnpm@9 --dir web typecheck`
- `npx -y pnpm@9 --dir web test`
- `npx -y pnpm@9 --dir web build`
- 浏览器 smoke：打开 Workbench，验证 queue、selected issue、action rail 可见。
- 回归 17 项 E2E 覆盖：create/update/comment/blocker/duplicate/pause/resume、dispatch、run detail/events、review artifact、Send to Rework、Mark Done、Approval empty、Workflow actions、Diagnostics export、not found、CSRF error、command error、daemon unavailable。

## 风险与约束

- Workbench 会弱化完整 Kanban 的首屏存在感，因此必须保留 Board View。
- 如果 selected issue 自动选择策略不清晰，用户可能误操作；需要明确 active selection 样式。
- SSE 双帧可能导致冗余 refresh，Workbench 需要避免重复 loading 抖动。
- 不应在本次设计中引入新依赖；现有 React/Vite 足够落地。
