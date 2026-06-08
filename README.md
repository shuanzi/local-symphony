# Local Symphony

Local Symphony 是一个基于 OpenAI Symphony 项目思想改造的本地优先（local-first）agent 工程流程控制台。它面向本地 Git 仓库，提供本地 issue 管理、独立 worktree 工作区、agent run 编排、Tool Gateway、handoff、review packet 生成和人工复核闭环。

当前工程是 v1 本地版本，默认使用 fake runner 跑通端到端流程；真实 Codex adapter 目前是 fixture-gated skeleton，未配置受支持 fixture 时会 fail-closed，不会直接启动未知协议版本的 Codex 进程。

## 1. 项目能力概览

v1 已覆盖以下本地闭环：

```text
本地 issue 创建 / 编辑 / 评论 / 状态流转
Ready / Rework issue 手动 dispatch
每个 issue 独立 git worktree + branch
fake runner 默认执行并提交 handoff
run-scoped Tool Gateway
handoff finalizer + review packet 生成
Human Review 后由 operator Mark Done 或 Send to Rework
SQLite local tracker
REST / SSE API
CLI 操作入口
Diagnostics 导出
React/Vite dashboard MVP 与前端动作合同检查
合同校验脚本与本地 acceptance 脚本
```

v1 明确不实现以下能力：

```text
Linear adapter / GitHub Issues adapter
自动 push / 自动 PR / 自动 merge / publish
agent 自动 commit
自动 workspace cleanup / delete / reset / rebase
自动 retry queue / retry timer
remote dashboard / multi-tenant RBAC
dynamic tools / MCP
raw prompt 或 raw Codex log 通过 API/dashboard/diagnostics 直接导出
```

## 2. 工程目录

```text
cmd/symphony/                    # Go CLI / daemon 入口
internal/app/                    # serve 生命周期与 runtime descriptor
internal/cli/                    # CLI 命令解析
internal/store/                  # SQLite local tracker 与状态管理
internal/orchestrator/           # dispatch / run lifecycle / fake runner 编排
internal/workspace/              # git worktree workspace manager
internal/toolgateway/            # Tool Gateway registry 与 run-scoped token 校验
internal/review/                 # review packet 生成
internal/httpapi/                # REST / SSE API
internal/agent/fake/             # 默认 fake runner
internal/agent/codex/            # fixture-gated Codex adapter skeleton
internal/observability/          # diagnostics
api/openapi.yaml                 # REST / SSE API 合同
schemas/                         # JSON Schema 合同
db/schema/                       # SQLite schema 合同
docs/testing/                    # 验收说明与 contract manifest
scripts/                         # 合同校验与本地验收脚本
web/                             # React/Vite dashboard MVP 与动作合同检查
PRD.md                           # 产品需求说明
TECH_SPEC.md                     # 技术设计说明
WORKFLOW.md                      # init 后生成的项目 workflow 配置，若已存在则不会覆盖
```

## 3. 环境要求

建议环境：

```text
Go 1.23+
Git
Python 3.10+，用于运行合同校验脚本
Node.js 18+，用于运行 React/Vite dashboard 检查
支持 CGO 的 C 编译工具链
SQLite3 开发库 / 系统库
```

本项目的 SQLite 封装使用 CGO 调用系统 `sqlite3`。如果构建时报 SQLite 或 CGO 相关错误，请先安装系统依赖：

```bash
# Debian / Ubuntu
sudo apt-get update
sudo apt-get install -y build-essential libsqlite3-dev

# macOS
xcode-select --install
# 如系统找不到 sqlite3，可再安装：
brew install sqlite
```

合同校验依赖 Python dev 包；首次运行前安装：

```bash
python3 -m pip install -r requirements-dev.txt
```

## 4. 构建

在工程根目录执行：

```bash
go build -o ./bin/symphony ./cmd/symphony
./bin/symphony --help
```

也可以直接使用 `go run`：

```bash
go run ./cmd/symphony --help
```

## 5. 初始化本地项目

Local Symphony 可以初始化任意本地 Git 仓库。建议仓库至少已有一次 commit，以便使用 git worktree 创建 issue workspace。

```bash
cd /path/to/your/repo

# 示例：如果是一个全新仓库，先创建初始 commit
git init
git config user.email symphony@example.invalid
git config user.name Symphony
printf 'hello\n' > README.md
git add README.md
git commit -m init

# 使用已构建的 symphony 二进制初始化项目
/path/to/local-symphony/bin/symphony init --issue-prefix LOC
```

初始化后会生成或使用以下本地文件：

```text
.symphony/project.db             # 当前项目的 SQLite tracker DB
.symphony/artifacts/             # review packet、diagnostics 等 artifact
WORKFLOW.md                      # 项目 workflow 配置；不存在时自动生成
~/.symphony/app.db               # 全局 app-level SQLite DB
~/.symphony/workspaces/          # 默认 issue worktree 根目录
~/.symphony/runtime/             # serve 运行期 descriptor
~/.symphony/cli-session.json     # serve 启动后写入的本地 CLI session 信息
```

查看当前项目状态：

```bash
/path/to/local-symphony/bin/symphony status --project .
```

## 6. 启动本地 API / Dashboard GUI

启动本地服务：

```bash
/path/to/local-symphony/bin/symphony serve --project . --no-open
```

默认绑定 loopback 地址 `127.0.0.1`，端口未指定时由系统分配。也可以指定固定端口：

```bash
/path/to/local-symphony/bin/symphony serve --project . --host 127.0.0.1 --port 7331 --no-open
```

检查 API：

```bash
curl http://127.0.0.1:7331/api/v1/health
```

`/api/v1/health` 不需要认证。`/api/v1/state`、`/api/v1/events` 和其他受保护 API 需要通过本地 dashboard open URL 建立 session，或携带 daemon 颁发的认证凭据。

服务运行时，可查看 runtime descriptor：

```bash
/path/to/local-symphony/bin/symphony open --project .
```

当前 `web/` 是 React/Vite dashboard MVP 和动作合同检查。`symphony serve` 的 `/api/v1/*` 与 `/tool/v1/call` 路由优先于 dashboard 静态资源。根路径只会从可信 dashboard dist 位置读取静态资源：优先使用 `SYMPHONY_DASHBOARD_DIST` 指向的构建产物目录；未设置时从 `symphony` 可执行文件所在目录推导 `web/dist`、`../web/dist`、`../share/local-symphony/web/dist` 等安装位置。被管理项目根目录下的 `web/dist` 不会作为默认候选。

## 7. 本地 issue 主流程

以下命令演示 v1 的默认主流程：

```bash
BIN=/path/to/local-symphony/bin/symphony

# 创建本地 issue
$BIN issue create \
  --project . \
  --title "Add greeting" \
  --description "Implement a greeting helper" \
  --acceptance "Greeting helper exists" \
  --acceptance "Acceptance script passes" \
  --priority 3 \
  --label demo

# 将 issue 放入可派发状态
$BIN issue transition LOC-1 Ready --project .

# 手动 dispatch；默认 fake runner 会生成 symphony-output.txt 并提交 handoff
$BIN issue dispatch LOC-1 --project .

# 查看 run、issue 和 review packet
$BIN run list --project .
$BIN review LOC-1 --project .

# 人工复核后标记完成
$BIN review mark-done LOC-1 --reason "Accepted" --project .
```

Rework 流程：

```bash
$BIN review send-to-rework LOC-1 --reason "Need additional verification" --project .
$BIN issue dispatch LOC-1 --project .
```

暂停 / 恢复 dispatch：

```bash
$BIN issue dispatch-pause LOC-1 --reason "Waiting for dependency" --project .
$BIN issue dispatch-resume LOC-1 --reason "Dependency resolved" --project .
```

查看单个 issue：

```bash
$BIN issue show LOC-1 --project .
```

## 8. Workflow 配置

`WORKFLOW.md` 是项目内的 workflow 配置入口。`symphony init` 会在文件不存在时生成默认模板。

常用命令：

```bash
$BIN workflow show --project .
$BIN workflow validate --project .
$BIN workflow reload --project .
```

v1 的关键 workflow 约束包括：

```text
tracker.kind = local
git.branch_prefix = symphony
git.auto_push = false
agent.handoff_required = true
agent.handoff_state = Human Review
tools.allow_dynamic_tools = false
tools.allow_mcp = false
security.allow_remote_api = false
```

如果 workflow 校验失败，dispatch 会失败并返回 `workflow_invalid`。

## 9. Tool Gateway

Tool Gateway 面向 agent run 使用。工具调用必须携带 run-scoped token，并且只能在该 run 的 workspace 范围内操作。

当前 registry 支持：

```text
issue.get
issue.comment
issue.block
artifact.attach
followup.create
handoff.submit
```

CLI 形式：

```bash
# 由运行时注入；手工调用时需要自行设置
export SYMPHONY_TOOL_ENDPOINT=http://127.0.0.1:7331/tool/v1/call
export SYMPHONY_TOOL_TOKEN=<run-scoped-token>

symphony tool issue get
symphony tool issue comment --json ./comment.json
symphony tool issue block --json ./block.json
symphony tool artifact attach --json ./artifact.json
symphony tool followup create --json ./followup.json
symphony tool handoff submit --json ./handoff.json
```

示例 handoff 输入可参考：

```text
examples/handoff.json
schemas/tools/handoff_submit.input.schema.json
```

注意：`handoff.submit` 只提交 handoff 数据；issue 进入 `Human Review` 需要 after_run hook 和 review packet finalizer 成功完成。

## 10. Review packet 与 artifact

dispatch 成功后，系统会生成 review packet，并将 issue 转入 `Human Review`。

查看 review packet 元信息：

```bash
$BIN review LOC-1 --project .
$BIN review path LOC-1 --project .
```

review packet 与 artifact 会写入：

```text
.symphony/artifacts/<issue>/<run>/...
```

packet 通常包括：

```text
REVIEW.md
review.json
patch.diff
changed_files.txt
untracked_files.txt
diffstat.txt
```

## 11. Diagnostics

查看诊断信息：

```bash
$BIN diagnostics --project .
```

导出 redacted diagnostics artifact：

```bash
$BIN diagnostics export --project .
```

Diagnostics 用于排查 workflow、SQLite、runtime descriptor、Codex 支持状态、git/worktree、failure summary 和 dispatch pause 状态。

## 12. 测试说明

在工程根目录执行以下检查。

### 12.1 Go 包检查

```bash
go test ./...
```

当前工程没有复杂 Go 单元测试文件，但该命令会编译所有 Go package，并能发现接口、CGO、导入和类型错误。

### 12.2 合同校验

```bash
python3 -m pip install -r requirements-dev.txt
python3 scripts/validate_contracts.py
```

该脚本校验：

```text
OpenAPI route 合同
JSON Schema 语法与关键字段
SQLite schema 可解析性
Tool Gateway registry
examples 与对应 schema
文档中的 v1 禁止能力漂移
CONTRACT_VALIDATION_MANIFEST.json
```

也可以使用 shell wrapper：

```bash
bash scripts/validate-contracts.sh
```

### 12.3 本地端到端验收

```bash
bash scripts/acceptance-local.sh
```

该脚本会在临时目录中创建 Git 仓库，构建 `symphony` 二进制，然后执行：

```text
init → issue create → Ready → dispatch → review packet → mark-done
```

成功时输出：

```text
acceptance-local passed
```

### 12.4 Web dashboard 检查

```bash
cd web
npm run typecheck
npm test
```

当前 web 检查覆盖 TypeScript 类型检查、dashboard action inventory 全量 required/forbidden 映射、关键 API 路径、认证失效清理、事件增量加载、review/workflow 受保护操作和关键本地表单校验的静态漂移检查。

### 12.5 fake runner 失败场景

可用环境变量模拟 runner 行为：

```bash
# 模拟 runner 失败
SYMPHONY_FAKE_RUNNER_OUTCOME=failure $BIN issue dispatch LOC-1 --project .

# 模拟完成但未提交 handoff
SYMPHONY_FAKE_RUNNER_OUTCOME=missing_handoff $BIN issue dispatch LOC-1 --project .

# 模拟 active/running 状态，便于测试 stale run reconciliation
SYMPHONY_FAKE_RUNNER_OUTCOME=hold $BIN issue dispatch LOC-1 --project .

# 自定义失败码
SYMPHONY_FAKE_RUNNER_OUTCOME=failure \
SYMPHONY_FAKE_FAILURE_CODE=codex_protocol_error \
$BIN issue dispatch LOC-1 --project .
```

失败后 issue 会回到 dispatch 前来源状态，并设置 `dispatch_paused=true`。恢复 dispatch 需要 operator 显式执行 `issue dispatch-resume`。

## 13. REST / SSE API 摘要

主要 API 路径：

```text
GET  /api/v1/health
GET  /api/v1/state
GET  /api/v1/events
GET  /api/v1/events/stream
GET  /api/v1/issues
POST /api/v1/issues
GET  /api/v1/issues/{issue_ref}
GET  /api/v1/issues/{issue_ref}/events/stream
POST /api/v1/issues/{issue_ref}/transition
POST /api/v1/issues/{issue_ref}/dispatch
POST /api/v1/issues/{issue_ref}/dispatch-pause
POST /api/v1/issues/{issue_ref}/dispatch-resume
GET  /api/v1/runs
GET  /api/v1/runs/{run_id}
GET  /api/v1/runs/{run_id}/events
GET  /api/v1/reviews/{issue_ref}
POST /api/v1/reviews/{issue_ref}/send-to-rework
POST /api/v1/reviews/{issue_ref}/mark-done
GET  /api/v1/workflow
POST /api/v1/workflow/validate
POST /api/v1/workflow/reload
GET  /api/v1/diagnostics
POST /api/v1/diagnostics/export
POST /tool/v1/call
```

完整 API 合同以 `api/openapi.yaml` 为准。

## 14. 常见问题

### `project is not initialized; run symphony init`

当前目录或 `--project` 指向的目录没有 `.symphony/project.db`。先执行：

```bash
$BIN init --issue-prefix LOC --project .
```

### `workflow_invalid`

`WORKFLOW.md` 不满足 v1 合同。执行：

```bash
$BIN workflow validate --project .
```

根据返回的 `errors` 修正配置后再 dispatch。

### `reason is required`

以下命令需要显式说明原因：

```text
review mark-done
review send-to-rework
issue dispatch-pause
issue dispatch-resume
```

### SQLite / CGO 构建失败

确认已安装 C 编译器和 SQLite3 开发库，并确保没有关闭 CGO：

```bash
CGO_ENABLED=1 go build -o ./bin/symphony ./cmd/symphony
```

### `serve` 不能绑定远程地址

v1 安全基线只允许 loopback API。`--host` 应使用：

```text
127.0.0.1
localhost
```

### 真实 Codex 不运行

这是 v1 的预期行为。当前真实 Codex adapter 是 fixture-gated skeleton；默认测试与验收使用 fake runner。

## 15. 清理本地数据

清理当前项目数据前请确认不再需要历史 issue、run、review packet 和 artifact。

```bash
# 当前项目内数据
rm -rf .symphony
rm -f WORKFLOW.md

# 全局 runtime/session/workspace 数据，按需清理
rm -rf ~/.symphony/runtime
rm -f ~/.symphony/cli-session.json
rm -rf ~/.symphony/workspaces/<project_id>

# 谨慎：会删除所有 Local Symphony 全局项目索引与 session 数据
rm -f ~/.symphony/app.db
```

## 16. 文档与合同关系

```text
PRD.md                    # 产品目标、范围、用户流程和验收口径
TECH_SPEC.md              # 技术架构、状态机、模块职责、安全、测试和发布合同
api/openapi.yaml          # REST / SSE API 可执行合同
db/schema/*.sql           # SQLite schema 合同
schemas/*.schema.json     # DTO / event / diagnostics / review / workflow / tool schema
docs/testing/*.md         # 验收与 failure code 说明
docs/agent_work_orders/   # M0-M8 implementation work orders
```

当 README 与合同文件冲突时，以 `PRD.md`、`TECH_SPEC.md`、`api/openapi.yaml`、`db/schema/*.sql` 和 `schemas/*.schema.json` 为准。
