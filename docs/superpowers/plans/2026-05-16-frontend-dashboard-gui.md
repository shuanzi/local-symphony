# Local Symphony Frontend GUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将当前 `web/` skeleton 落地为可用的本地 operator dashboard MVP，覆盖 issue、run、review、approval、workflow、diagnostics 的核心浏览与命令操作。

**Architecture:** 前端使用 React + TypeScript，先以 `web/` 独立开发服务器调用 `symphony serve` 的 loopback REST/SSE API；完成后把构建产物接入 Go 服务根路径。前端不直接访问 SQLite、Git、文件系统、Codex 或 Tool Gateway，只通过 `/api/v1/*` 和 SSE 获取状态。

**Tech Stack:** React, TypeScript, Vite, REST fetch client, EventSource, Node test scripts, Go static asset serving.

---

## 范围边界

- 包含：本地 dashboard 页面、API client、SSE timeline、核心操作按钮、错误/空态/加载态、artifact 安全展示、前端动作合同测试。
- 不包含：remote dashboard、多租户 RBAC、desktop shell、自动 push/PR/merge、workspace delete/reset/clean/rebase、raw prompt/raw Codex log 展示。
- 默认 API 来源：同源 `/api/v1`；开发模式可通过 Vite proxy 转发到 `http://127.0.0.1:<port>`。

## 文件规划

- Modify: `web/package.json`，添加 `dev`、`build`、`preview`、`typecheck`、`test` 脚本和 React/Vite 依赖。
- Create: `web/index.html`，前端入口 HTML。
- Create: `web/src/main.tsx`，React bootstrap。
- Replace/Modify: `web/src/App.tsx`，dashboard shell 与页面编排；保留并导出 dashboard action inventory。
- Create: `web/src/api/client.ts`，统一 fetch envelope、错误归一化、CSRF/session 头处理。
- Create: `web/src/api/events.ts`，SSE 连接与 replay 状态。
- Create: `web/src/api/types.ts`，本阶段手写最小 API 类型，字段来自 OpenAPI/JSON schema。
- Create: `web/src/components/*.tsx`，拆分 issue board、run timeline、review panel、approval inbox、diagnostics panel、forms。
- Create: `web/src/styles.css`，紧凑操作台布局和状态样式。
- Modify: `web/action-inventory.json`，保持允许动作与禁止动作同步。
- Modify: `web/scripts/typecheck.mjs`、`web/scripts/test.mjs`，覆盖新 UI 合同。
- Modify: `internal/httpapi/httpapi.go`，根路径优先服务 dashboard 静态资源，保留 API 路由。
- Create: `internal/httpapi/dashboard_assets.go`，嵌入或定位构建产物。
- Modify: `README.md`，更新 dashboard 启动和验证说明。

## 总体 Checklist

- [ ] 前端工程可启动：`cd web && pnpm install && pnpm dev`。
- [ ] Dashboard 能连接本地 `symphony serve` 并展示项目状态。
- [ ] Issue 列表、详情、创建、编辑、评论、状态流转可用。
- [ ] Ready/Rework issue 可 dispatch，dispatch pause/resume 可用。
- [ ] Run timeline 通过 SSE 或事件 replay 展示 normalized events。
- [ ] Review Packet 可展示 redacted artifact 列表，支持 Send to Rework 和 Mark Done。
- [ ] Approval Inbox 展示 `action_summary`、`risk_level`、`policy_match`，五种 decision 均可操作。
- [ ] Workflow validate/reload/show 与 diagnostics/export 可用。
- [ ] UI 不暴露 forbidden actions。
- [ ] `go test ./...`、`bash scripts/validate-contracts.sh`、`cd web && pnpm typecheck && pnpm test && pnpm build` 通过。
- [ ] `symphony serve` 根路径打开 dashboard，而不是占位 HTML。

## Task 1: 前端工程骨架

**Files:**
- Modify: `web/package.json`
- Create: `web/index.html`
- Create: `web/src/main.tsx`
- Modify: `web/src/App.tsx`
- Create: `web/src/styles.css`

- [ ] 将 `web/src/App.ts` 重命名或迁移为 `web/src/App.tsx`，保留 `API_PREFIX` 与 `dashboardActions` 导出。
- [ ] 添加 React/Vite 依赖，理由写入 PR：TECH_SPEC 已要求 React/TypeScript dashboard，当前 skeleton 无可运行 GUI。
- [ ] 实现三栏操作台布局：左侧 issue board，中间详情/时间线，右侧 review/approval/diagnostics。
- [ ] 加入全局加载态、空态、daemon unavailable 状态。
- [ ] 本地验证：`cd web && pnpm typecheck && pnpm test`。
- [ ] 提交：`git commit -m "feat: scaffold dashboard app"`。

## Task 2: API Client 与类型边界

**Files:**
- Create: `web/src/api/client.ts`
- Create: `web/src/api/types.ts`
- Modify: `web/src/App.tsx`

- [ ] 定义 `ApiEnvelope<T>`、`ApiErrorEnvelope`、`Issue`、`Run`、`ReviewPacket`、`Approval`、`Diagnostics` 最小类型。
- [ ] 实现 `apiGet`、`apiPost`、`apiPatch`，统一解析 `{data, meta}` envelope。
- [ ] 对 `401`、daemon unavailable、artifact refusal、command error 返回稳定 UI error model。
- [ ] 实现 session bootstrap：调用 `/api/v1/auth/session`；命令请求保留 CSRF 注入点。
- [ ] 添加 API client 单元测试，覆盖成功 envelope、错误 envelope、网络失败。
- [ ] 本地验证：`cd web && pnpm test`。
- [ ] 提交：`git commit -m "feat: add dashboard api client"`。

## Task 3: Issue Board 与命令操作

**Files:**
- Create: `web/src/components/IssueBoard.tsx`
- Create: `web/src/components/IssueDetail.tsx`
- Create: `web/src/components/IssueForm.tsx`
- Modify: `web/action-inventory.json`

- [ ] 拉取 `/api/v1/issues?limit=50&sort=priority` 并按状态分组显示。
- [ ] 支持创建 issue：title、description、acceptance criteria、priority、labels。
- [ ] 支持 issue update、comment、blocker、duplicate 标记与解除。
- [ ] 支持合法状态流转，按钮只展示 OpenAPI 已存在的 command API。
- [ ] 支持 dispatch、dispatch-pause、dispatch-resume；按钮在状态不允许时禁用并解释原因。
- [ ] 测试 forbidden actions 不出现：`git push`、`publish`、`create pr`、`workspace delete`、`secret read`。
- [ ] 本地验证：创建 `LOC-1`，流转到 `Ready`，dispatch 后进入 `Human Review`。
- [ ] 提交：`git commit -m "feat: implement issue dashboard actions"`。

## Task 4: Run Timeline 与 SSE

**Files:**
- Create: `web/src/api/events.ts`
- Create: `web/src/components/RunTimeline.tsx`
- Modify: `web/src/App.tsx`

- [ ] 首屏通过 `/api/v1/events` replay durable normalized events。
- [ ] 在线状态通过 `/api/v1/events/stream` 建立 EventSource。
- [ ] Timeline 显示 `issue.created`、`issue.transitioned`、`run.claimed`、`handoff.submitted`、`review.packet_generated`、`issue.completed`。
- [ ] 断线时显示 reconnecting 状态，并保留已有事件。
- [ ] UI 不展示 raw Codex logs 或 raw prompt。
- [ ] 本地验证：dispatch 一条 issue 后，timeline 出现 run 与 review 事件。
- [ ] 提交：`git commit -m "feat: add dashboard event timeline"`。

## Task 5: Review Packet 与 Artifact 展示

**Files:**
- Create: `web/src/components/ReviewPanel.tsx`
- Create: `web/src/components/ArtifactViewer.tsx`
- Modify: `web/src/api/types.ts`

- [ ] 调用 `/api/v1/reviews/{issue_ref}` 展示 packet 状态、文件列表、redacted 标记。
- [ ] 对 `content_url=null` 或 artifact refusal 显示安全空态，不把不可见内容当作成功验证。
- [ ] 支持查看允许的 artifact 内容：changed files、diffstat、patch、review markdown。
- [ ] 支持 Send to Rework，表单字段为 `reason`，UI 可显示为 feedback。
- [ ] 支持 Mark Done，表单字段为 `reason`，UI 可显示为 comment。
- [ ] Mark Done 前提示 latest review packet 必须为当前 latest completed handoff run。
- [ ] 本地验证：review packet generated 后，Send to Rework 与 Mark Done 均可按状态约束执行。
- [ ] 提交：`git commit -m "feat: implement review packet panel"`。

## Task 6: Approval Inbox

**Files:**
- Create: `web/src/components/ApprovalInbox.tsx`
- Modify: `web/src/api/types.ts`
- Modify: `web/action-inventory.json`

- [ ] 调用 `/api/v1/approvals` 展示 pending approvals。
- [ ] 展示 `action_summary`、`risk_level`、`policy_match`，不解析 opaque request JSON 作为稳定显示文本。
- [ ] 实现五个明确动作：`approve_once`、`approve_for_run`、`approve_for_session`、`deny`、`cancel_run`。
- [ ] 将 `deny` 与 `cancel_run` 做视觉区分。
- [ ] 空 inbox 显示明确空态。
- [ ] 本地验证：无 pending approvals 时为空态；构造测试数据时五个按钮命中正确 request payload。
- [ ] 提交：`git commit -m "feat: add approval inbox"`。

## Task 7: Workflow 与 Diagnostics

**Files:**
- Create: `web/src/components/WorkflowPanel.tsx`
- Create: `web/src/components/DiagnosticsPanel.tsx`
- Modify: `web/src/api/types.ts`

- [ ] Workflow panel 支持 show、validate、reload、render preview。
- [ ] Validate/reload 显示 `validation.valid`、errors、warnings 和 side effects。
- [ ] Diagnostics panel 展示 redacted diagnostics 摘要。
- [ ] 支持 diagnostics export，成功后展示 artifact id/path。
- [ ] 不展示 raw prompt、raw secrets、raw Codex logs。
- [ ] 本地验证：`workflow validate` 与 `diagnostics export` 均返回成功 envelope。
- [ ] 提交：`git commit -m "feat: add workflow and diagnostics panels"`。

## Task 8: 静态资源接入 `symphony serve`

**Files:**
- Modify: `internal/httpapi/httpapi.go`
- Create: `internal/httpapi/dashboard_assets.go`
- Modify: `web/package.json`
- Modify: `README.md`

- [ ] `pnpm build` 输出 `web/dist`。
- [ ] Go 服务根路径返回 dashboard `index.html`，`/assets/*` 返回构建资源。
- [ ] `/api/v1/*`、`/tool/v1/call`、artifact 路由优先级不变。
- [ ] 如果 dashboard assets 不存在，根路径返回清晰错误页，说明需要运行 `cd web && pnpm build`。
- [ ] 更新 README：开发模式、生产构建、浏览器访问地址、验证命令。
- [ ] 本地验证：`go build -o ./bin/symphony ./cmd/symphony && ./bin/symphony serve --project <tmp> --host 127.0.0.1 --port 7331 --no-open` 后根路径显示 dashboard。
- [ ] 提交：`git commit -m "feat: serve dashboard assets"`。

## Task 9: 验收与回归保护

**Files:**
- Modify: `web/scripts/typecheck.mjs`
- Modify: `web/scripts/test.mjs`
- Modify: `scripts/acceptance-local.sh`
- Modify: `docs/testing/ACCEPTANCE.md`

- [ ] 扩展 web tests：必需动作存在、禁止动作不存在、关键组件导出存在。
- [ ] 增加 dashboard smoke test：启动服务后检查根路径包含 dashboard root 节点。
- [ ] 保持 acceptance 流程不依赖真实 Codex adapter。
- [ ] 运行 `go test ./...`。
- [ ] 运行 `bash scripts/validate-contracts.sh`。
- [ ] 运行 `bash scripts/acceptance-local.sh`。
- [ ] 运行 `cd web && pnpm typecheck && pnpm test && pnpm build`。
- [ ] 记录剩余风险：真实浏览器自动化、视觉回归、真实 Codex adapter opt-in 测试。
- [ ] 提交：`git commit -m "test: cover dashboard gui contracts"`。

## 完成定义

- [ ] `symphony serve` 打开的根页面是可操作 dashboard。
- [ ] Operator 可以从 GUI 完成 `init` 后的核心闭环：create issue → Ready → dispatch → review packet → mark done。
- [ ] GUI 可以展示 run timeline、review artifacts、workflow validation、diagnostics。
- [ ] GUI 不能触发 v1 禁止能力。
- [ ] README 明确说明当前 GUI 能力和已知限制。
- [ ] 所有目标验证命令通过，失败时文档记录失败命令、错误摘要和剩余风险。
