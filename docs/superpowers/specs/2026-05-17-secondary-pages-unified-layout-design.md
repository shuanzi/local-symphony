# Secondary Pages Unified Layout Design

## 背景

Workbench 已成为默认入口，但 Board、Issue Detail、Run Detail、Approval、Review、Workflow、Diagnostics 仍保留旧的页面合集观感。Operator 在这些专项页里需要快速判断页面目的、当前状态和可执行动作。

## 目标

- 为所有二级页面提供一致的页面头：标题、说明、关键状态和主操作。
- 将详情页改成主信息区加右侧辅助区的布局，减少长页面滚动成本。
- 保留现有 API、路由、命令和安全边界，不新增依赖。
- 移动端保持单列，不产生页面级横向滚动。

## 方案

采用 **统一专项页面结构**：

- `PageHeader`：用于 Board、Issue、Run、Approval、Review、Workflow、Diagnostics 的页首说明和主操作。
- `PageSplit`：用于 Issue、Run、Review、Workflow、Diagnostics 等需要主内容和辅助信息的页面。
- Board 保留完整 Kanban，但增加状态摘要和创建 issue 折叠入口。
- Approval 聚焦 pending 优先，resolved 作为次级列表。
- Review 将 packet summary、artifact boundary、review action 形成清晰的工作流顺序。
- Workflow 将状态、动作、配置分区，强调 dry-run 与 reload 的边界。
- Diagnostics 将 export 与 warning 摘要前置，细节保持网格。

## 非目标

- 不拆分 `App.tsx` 到多个文件。
- 不改变 REST/SSE 数据流。
- 不新增远程、PR、push、secret、workspace delete 等禁止能力。
- 不显示 raw Codex log、raw prompt 或 secret-like 内容。

## 验证

- `npx -y pnpm@9 --dir web typecheck`
- `npx -y pnpm@9 --dir web test`
- `npx -y pnpm@9 --dir web build`
- 浏览器走查 Workbench、Board、Issue Detail、Run Detail、Review、Approval、Workflow、Diagnostics。
