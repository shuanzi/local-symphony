# Dashboard 前后端联调报告

## 验证结论

2026-05-17 已完成一轮 production build 下的前后端联调。测试使用 `SYMPHONY_DASHBOARD_DIST=/Users/xiquandai/Documents/code/local-symphony/web/dist` 启动 `symphony serve`，通过真实浏览器覆盖 dashboard 核心路径。

主要证据：

- Summary: `/tmp/local-symphony-e2e/logs/summary.json`
- Progress: `/tmp/local-symphony-e2e/logs/progress.log`
- Screenshots: `/tmp/local-symphony-e2e/artifacts/*.png`
- Serve log: `/tmp/local-symphony-e2e/logs/serve.log`
- Browser console: `/tmp/local-symphony-e2e/logs/browser-console.log`

覆盖结果：17/17 项通过，失败 0。

## 已覆盖功能

- Overview 与 SSE connected 状态。
- Board issue create、update、comment、blocker add/remove、duplicate relation、dispatch pause/resume。
- Dispatch、run detail、normalized events。
- Review artifact 内容读取、Send to Rework、Mark Done。
- Approval Inbox 空态。
- Workflow validate、render preview、reload。
- Diagnostics export。
- issue/run not found 状态。
- CSRF 缺失错误。
- Command error UI 与 daemon unavailable UI。

## 已修复的联调问题

- Dashboard 静态资源默认候选会跳过 `Store.RepoRoot` 下的 `web/dist`，避免被管理仓库接管同源 dashboard；显式 `SYMPHONY_DASHBOARD_DIST` 仍可用于开发和本地验收。
- CSRF token 改为每个 server 实例随机生成，mutation 缺 token 返回 `403 csrf_required`。
- SSE 支持持续连接、`Last-Event-ID`、issue/run 过滤；未知 issue/run stream 返回 404。
- SSE 同时输出命名事件帧和默认 message 帧，兼容 `addEventListener(event_type, ...)` 与 dashboard `onmessage` 刷新。
- issue/run deep link 不再 fallback 到第一条记录；404 与非 404 错误都有终止态。
- Workflow render preview 不暴露 raw prompt，只返回 redacted preview 和 prompt metadata。
- `security.NewToken()` 在 entropy source 失败时 fail-closed。

## 剩余风险

- Approval 目前只覆盖空态，尚未构造 pending approval 并验证五种 decision payload。
- 部分关系、Workflow、Review artifact 操作通过浏览器上下文内 `fetch` 覆盖真实 API 后回到 dashboard 断言，不完全等同于用户逐按钮点击。
- SSE 双帧会让同时监听 `onmessage` 和命名事件的外部客户端重复处理同一业务事件；当前 dashboard 只表现为冗余刷新。
- Unknown issue/run stream 的 404 行为尚未同步到 OpenAPI 响应合同。
- `python3 scripts/validate_contracts.py` 因本机未安装 `jsonschema`，跳过了部分 schema/example 深度校验。
- 当前分支未配置 upstream，无法用 `@{u}` 做 ahead/behind 同步判断。

## 改进建议

- 增加 pending approval fixture 或测试 API，覆盖 approve once、approve for run、approve for session、deny、cancel run。
- 将浏览器 E2E 固化为仓库脚本，例如 `scripts/dashboard-e2e.sh`，并在报告中保留稳定 artifact 输出目录。
- 对 SSE 客户端去重：同一 `seq` 同时通过命名事件与 message 到达时只触发一次刷新。
- 更新 OpenAPI，为 issue/run SSE stream 增加 404 error envelope 描述。
- 在合同校验环境安装 `jsonschema`，让 workflow schema 与 tool example 校验不再跳过。
- 为 dashboard 静态资源来源补充 README 安全说明：默认只使用可信安装路径，开发时必须显式设置 `SYMPHONY_DASHBOARD_DIST`。
