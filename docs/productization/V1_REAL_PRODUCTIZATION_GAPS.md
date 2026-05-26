# v1 真实产品化未完成功能需求

**状态**：当前实现差距梳理  
**更新日期**：2026-05-22  
**适用范围**：Local Symphony 从“本地控制台 MVP + fake runner 闭环”推进到“可真实运行 Codex 的本地产品化版本”  

## 1. 文档定位

本文不是新的 PRD，也不扩大 v1 产品范围。目标产品边界仍以 `PRD.md` 为准；字段、状态机、API、DB、CLI、安全和测试合同仍以 `TECH_SPEC.md`、`api/openapi.yaml`、`schemas/`、`db/schema/`、`docs/testing/` 和 `docs/codex/` 为准。当前实现事实以本文核对的代码、`README.md` 的 current-state 说明和已运行验证结果为准。

本文只做一件事：基于当前代码状态，把尚未达到现有 PRD/TECH 目标的能力整理成后续真实产品化需求。实现时不得借本文绕过现有禁止项。

## 2. 当前真实可用能力

当前实现已经可以作为本地控制台 MVP 使用，默认通过 fake runner 跑通端到端流程：

- 本地项目初始化、状态查看、本地 daemon 启动、dashboard open token。
- 本地 issue 创建、列表、详情、更新、评论、状态流转、blocker、duplicate relation、dispatch pause/resume。
- 手动 dispatch `Ready` / `Rework` issue。
- 每个 issue 创建或复用 git worktree 与 branch。
- fake runner 成功路径：写入 `symphony-output.txt`，通过 Tool Gateway 提交 handoff，生成 review packet，进入 `Human Review`。
- Human Review 后由 operator `mark-done` 或 `send-to-rework`。
- run list/show/events/cancel、启动时 stale active run reconciliation。
- run-scoped Tool Gateway：`issue.get`、`issue.comment`、`issue.block`、`artifact.attach`、`followup.create`、`handoff.submit`。
- REST/SSE API、React/Vite dashboard MVP、workflow validate/reload/render preview、redacted diagnostics export。
- loopback 绑定、browser session、CSRF、CLI bearer、tool token、artifact containment、raw prompt/log refusal 等本地安全基础。

已验证的当前主闭环：

```bash
bash scripts/acceptance-local.sh
python3 scripts/validate_contracts.py
```

## 3. 产品化目标

真实产品化版本的目标是让 Local Symphony 不只演示本地流程，而是能在受控安全边界内真实驱动 Codex 完成本地工程任务：

```text
本地 issue
  → 调度/手动 dispatch
  → workspace 隔离
  → 真实 Codex app-server
  → approval bridge
  → Tool Gateway handoff
  → review packet
  → operator Rework / Done
```

目标必须保持以下 v1 边界：

- 不实现 Linear/GitHub Issues adapter。
- 不实现自动 push、PR、merge、publish。
- 不实现 agent 自动 commit。
- 不实现自动 workspace delete/reset/clean/rebase。
- 不实现自动 retry queue/timer。
- 不实现 dynamic tools/MCP。
- 不实现 remote dashboard、多租户 RBAC、project settings mutation、secret 管理、issue delete 或任意状态修改 API。
- 不通过 API、dashboard 或 diagnostics 暴露 raw prompt、raw Codex log 或 raw secret。

## 4. 产品化需求

### R1. Runner 抽象与真实 Codex 接入

**优先级**：P0  
**现状**：阶段 A 已落地 runner interface，dispatch 默认使用 fake runner，`SYMPHONY_RUNNER_KIND=codex` 时进入 fixture-gated Codex runner；未通过 fixture gate 时仍 fail-closed。  
**目标**：orchestrator 通过 runner interface 调用 fake runner 或 Codex runner，默认测试仍使用 fake runner，真实 Codex 只在 fixture gate 通过时运行。

验收要求：

- 定义并落地 `Runner` / `RunRequest` / `RunResult` 边界，Codex 协议细节保持在 `internal/agent/codex` 内。
- dispatch run 记录的 `runner_kind` 能准确反映 fake 或 codex。
- 默认 CI 和本地 acceptance 不依赖真实 Codex。
- 未通过 fixture gate 时不得启动 `codex app-server`，run 使用 `unsupported_codex_version` 失败并 pause。
- fake runner 保留为确定性验收路径，不成为生产 Codex 逻辑的特殊分支污染。

### R2. Codex fixture gate 与兼容性元数据

**优先级**：P0  
**现状**：阶段 A 已提交 `0.0.0-test` schema/transcript fixture、`compatibility.json` 和版本/handshake 校验；真实 Codex version 仍需新增对应 committed fixture 后才能声明支持。  
**目标**：只对已提交协议 fixture 的 Codex 版本声明支持。

验收要求：

- `internal/agent/codex/testdata/schema/<codex-version>/` 和 `internal/agent/codex/testdata/transcripts/<codex-version>/` 存在并被测试消费。
- prelaunch gate 只通过 `codex --version` 和 committed metadata 判定兼容性。
- generated protocol/schema version 来自 committed metadata 或 static compatibility metadata，不能通过启动真实 app-server 动态发现。
- post-launch handshake 与 metadata 不一致时，run 以 `codex_protocol_error` 失败。
- `SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration` 作为 opt-in 验证路径。

### R3. Codex process、stdio transport 与生命周期管理

**优先级**：P0  
**现状**：阶段 A 已实现 fixture-gated Codex process launcher、handshake、stdio JSONL 边界、timeout 与 process group termination；approval `cancel_run`、daemon shutdown/reconciliation 对真实 Codex process 的完整生产路径仍随阶段 B/C 收敛。  
**目标**：可控启动 `codex app-server`，cwd 固定为 issue workspace，并按 run 生命周期管理进程。

验收要求：

- 启动命令、cwd、环境变量与 `TECH_SPEC.md §10.1` 一致。
- startup timeout、turn timeout、stall timeout 分别映射到 `codex_startup_failed`、`turn_timeout`、`stall_timeout`。
- operator cancel、approval `cancel_run`、reconciliation、daemon shutdown、timeout 都能终止 process group。
- 进程 stderr 只进入 redacted diagnostics，不通过 API/dashboard 暴露 raw log。
- 所有 terminal failure 都走统一 failure path：恢复 source state、设置 dispatch pause、写入 run event 和系统 comment。

### R4. Codex 事件归一化与 timeline

**优先级**：P0  
**现状**：阶段 A 已映射最小 Codex normalized timeline，包括 process、handshake、thread、turn、approval placeholder、tool observation、protocol error、process exited；完整 approval row/writeback 和 dashboard 深度呈现仍在阶段 B/C。  
**目标**：把 Codex protocol 通知转换为 PRD/TECH 定义的 run event，而不是把 raw payload 直接塞进 UI。

验收要求：

- 支持 `agent.process_started`、`agent.handshake_completed`、`agent.thread_started`、`agent.turn_started`、`agent.turn_progress`、`agent.turn_completed`、`agent.turn_failed`、`agent.protocol_error`、`agent.process_exited`。
- 支持 `approval.requested`、`approval.resolved`。
- 支持 tool call 观察事件，但 Tool Gateway 自身仍以 daemon 记录为准。
- `run_events.data_json` 必须 redacted；大 payload 或 raw-ref 只能作为受限 artifact metadata。
- dashboard Run Detail 只消费 normalized timeline。

### R5. Approval bridge 与安全策略生产路径

**优先级**：P0  
**现状**：Approval API、CLI、dashboard 决策入口存在，但当前真实运行路径没有 Codex approval request producer；响应字段和 OpenAPI schema 也未完整满足 TECH 对 `action_summary`、`risk_level`、`policy_match` 的要求。  
**目标**：Codex 请求命令、文件、网络或 protected path 时，adapter 能生成 approval row、等待 operator 决策或 timeout，并把决策写回 Codex。

验收要求：

- Codex command/file_change/network approval request 进入 `approval_requests`。
- Approval API 返回 `action_summary`、`risk_level`、`policy_match`、`requested_at`、`timeout_ms`、`expires_at`、`resolved_at`。
- `approve_once`、`approve_for_run`、`approve_for_session`、`deny`、`cancel_run` 均有明确 Codex writeback 语义。
- `deny` 只拒绝当前动作，不直接取消 run 或设置 `operator_cancelled`。
- `cancel_run` 立即取消 run，使用 `operator_cancelled`，并 pause dispatch。
- approval timeout 产生 `approval_timeout`，之后不可再决策。
- command/network/protected-path auto-deny 使用 `command_denied`、`network_denied`、`protected_path_denied`，并走终止 pause 路径。

### R6. Missing handoff continuation

**优先级**：P0  
**现状**：阶段 A 已支持 fake runner 和 fixture-gated Codex runner 的 missing handoff continuation；Codex continuation 在同一 runner process/thread 中发送 handoff-only prompt。真实 Codex version 仍受 fixture gate 限制。  
**目标**：当主 turn 完成但没有 handoff 时，按配置最多发起一次专用 handoff continuation。

验收要求：

- `agent.max_handoff_continuations=1` 时，第一次 missing handoff 不立即终止，而是在同一 Codex session/thread 中发送专用 handoff continuation。
- continuation 仍未提交 handoff 时，run 进入 `completed_without_handoff`，failure code 为 `missing_handoff`，issue 恢复 source state 并 pause。
- `agent.max_handoff_continuations=0` 时，首次 missing handoff 直接进入终止 pause 路径。
- continuation 不能重新发送完整任务 prompt，避免扩大执行范围。

### R7. Hook lifecycle 完整落地

**优先级**：P1  
**现状**：当前只执行 `after_run`，且未完整实现 `after_create`、`before_run`；TECH 中对应 failure code 已存在。  
**目标**：workspace 生命周期 hook 与 run 生命周期一致。

验收要求：

- 新 workspace 创建后运行 `after_create`。
- 每次 run 启动前运行 `before_run`，包括首次 run 和 rework run。
- `after_create_failed`、`before_run_failed` 按 failure matrix 恢复 issue、pause dispatch，并不启动 runner。
- 只要 workspace 已准备，terminal worker outcome 都尝试 `after_run`。
- hook 输出受 `hooks.max_output_bytes` 限制，并进入 redacted event/diagnostics/review packet 摘要。

### R8. Scheduler tick loop 与单 daemon 项目所有权

**优先级**：P1  
**现状**：`orchestrator.Tick()` 存在，但 `serve` 未启动周期 tick；项目 runtime descriptor 存在，但当前主要是写 JSON/DB descriptor，尚未达到 TECH 要求的单 daemon ownership/runtime lock 合同。  
**目标**：daemon 能周期性调度 eligible issue，同时保证每个 project DB 只有一个活跃 owner。

验收要求：

- `symphony serve` 根据 workflow polling interval 启动 tick loop。
- tick 只选择 `Ready` 和 `Rework`，不把 `Working` 当正常候选。
- tick 和手动 dispatch 共享同一 `DispatchIssue` preflight。
- failure 后 pause 的 issue 不会被自动 redispatch。
- 项目 runtime lock 能阻止同一 project 多 daemon 同时写入。
- stale descriptor/lock owner 可安全恢复；runtime descriptor 不包含 secret/token。

### R9. CLI over REST 与 daemon session 对齐

**优先级**：P1  
**现状**：多数 CLI 命令直接打开 SQLite store；TECH 要求普通 operator CLI 使用 `/api/v1`，agent-facing tool CLI 使用 Tool Gateway。  
**目标**：CLI 行为与 API/dashboard 共享同一鉴权、CSRF/CLI bearer、错误映射和 side effect。

验收要求：

- 普通 operator CLI 在 daemon 可用时通过 REST API 执行命令。
- 缺失 daemon/session 时给出明确本地登录或 `symphony serve/open` 指引。
- CLI JSON 输出保持 envelope-unwrapped stable object。
- API `error.code` 与 CLI exit code 一致，特别是 `invalid_request=2`、workflow/prompt 相关错误为 9，其余操作冲突为 7。
- `symphony tool ...` 保持 Tool Gateway token 路径，不获得 operator REST 权限。

### R10. Review Packet API 与 dashboard 可审查性补齐

**优先级**：P1  
**现状**：review packet metadata 和 artifact 内容路径可用，但 dashboard 仍偏 MVP；Review API 对 structured summary、tests、risks、verification、tool calls、approvals 的展示能力不足。  
**目标**：Review Packet 页面成为人工验收的主界面，而不是只看文件元信息。

验收要求：

- Review API 返回足够信息，让 dashboard 展示 summary、acceptance criteria、handoff、changed files、diff、tests、risks、verification、approvals、tool calls、git 和 How to Continue。
- dashboard 通过 Review API 与 Artifact API 获取内容，不直接读 filesystem。
- raw prompt、raw Codex log、raw secret 对应 artifact 的 `content_url=null` 或返回明确 refusal。
- `symphony review path` 只输出 metadata/path diagnostics，不读取或打印 artifact 原文。
- review packet failure 不得进入 `Human Review`。

### R11. Dashboard 产品化补齐

**优先级**：P1  
**现状**：dashboard MVP 已有页面和动作，但与 TECH 中 generated API client、共享状态、approval 展示字段、Codex availability 等目标仍有差距。  
**目标**：dashboard 成为稳定 operator 控制面。

验收要求：

- 所有页面覆盖 loading、empty、auth error、daemon unavailable、artifact refusal、command error。
- Overview 展示 workflow status、running runs、pending approvals、failed runs、Human Review、paused issues、Codex availability、recent events。
- Approval Inbox 使用 API 返回的 `action_summary`、`risk_level`、`policy_match`，不解析 opaque `request_json`。
- Review Packet 页面以 `review.json` structured source 为基础展示内容。
- dashboard action inventory 继续禁止 publish、PR、backup/restore、workspace delete、secret、issue delete、project settings 等未来动作。

### R12. 安全策略与回归套件

**优先级**：P1  
**现状**：已有部分 token/path/redaction 基础，但真实 Codex-mediated command/network/protected-path 路径未完整出现。  
**目标**：安全策略可执行、可验证、可诊断。

验收要求：

- command policy 支持 allow/review/deny 分类，并接入 approval bridge。
- network default deny，不声明 OS-level network isolation。
- protected path read/write 通过 Codex-mediated 路径 auto-deny 或 review；Tool Gateway `artifact.attach` 仍是 daemon hard-deny 且不直接终止 run。
- redaction golden fixtures 覆盖 prompt、Codex log、secret、diagnostics。
- 默认安全回归不依赖真实 Codex、外部网络或 `SYMPHONY_TEST_CODEX=1`。
- opt-in real Codex tests 只用于真实 adapter 兼容性验证。

### R13. Release packaging 与安装体验

**优先级**：P2  
**现状**：可本地 build；dashboard dist 查找已实现，但单一 release artifact、资产嵌入/安装布局、平台矩阵和 known limitations 还未完全产品化。  
**目标**：用户可以安装一个明确版本的本地产品，而不是从源码拼装运行。

验收要求：

- release 构建产物明确，例如 `dist/symphony`。
- dashboard 静态资产随安装布局可被 daemon 安全发现；如选择嵌入，也必须保持 API/Tool Gateway 路由优先。
- release notes 声明 Go、Node、SQLite、Git、Codex 支持版本。
- macOS arm64/x64、Linux x64 为 release blocking 平台；Windows 如提供必须标注 best-effort 限制。
- quickstart、README、CLI help 与实际命令保持一致。

### R14. Codex availability 与 diagnostics 产品化

**优先级**：P1  
**现状**：diagnostics 当前固定展示 `codex.available=false`，没有接入 `codex --version`、fixture support、adapter preflight 或最近 Codex failure summary。  
**目标**：operator 能在 dashboard、CLI status 和 diagnostics 中判断“为什么真实 Codex 当前不能运行”。

验收要求：

- diagnostics 展示 installed Codex version、fixture support 状态、selected protocol/schema metadata、最后一次 preflight 结果。
- 无 fixture 时明确显示 `unsupported_codex_version` 风险，而不是只显示 unknown。
- dashboard Overview 显示 Codex availability，并链接到 Diagnostics。
- `symphony status` 展示 Codex availability、running runs、pending approvals、Human Review、paused issues 和 recent failures。
- diagnostics export 保持 redacted-only，不导出 raw Codex log、raw prompt 或 raw secret。

### R15. 文档、合同与验收对齐

**优先级**：P0  
**现状**：PRD/TECH 描述的是目标 v1 与技术合同；README 更接近当前实现；部分 acceptance 文档同时覆盖当前 fake path 和目标 contract。二者之间需要持续显式标注实现差距，避免误导用户。  
**目标**：所有文档都能清楚区分“当前可用”“目标合同”“禁止能力”和“已知限制”。

验收要求：

- README 的能力概览保留当前实现状态，不声称真实 Codex 已可用。
- PRD/TECH 如继续作为目标合同，应在 release notes 或产品化文档中标注当前实现达成度。
- `docs/codex/*` 与真实 adapter 代码、fixtures、测试保持同步。
- `docs/testing/ACCEPTANCE.md` 区分 fake acceptance、security regression、real Codex opt-in acceptance。
- 每个需求完成时同步更新 OpenAPI/schema/CLI help/dashboard action inventory/测试 manifest，避免第三套事实来源。

当前文档关系必须显式保持：

| 文档/来源 | 在产品化推进中的角色 | 注意事项 |
|---|---|---|
| `README.md` | 当前用户可运行能力和 known limitations | 不应声称真实 Codex 已默认可用。 |
| `PRD.md` | 目标产品边界和禁止能力 | 不等于当前实现完成度。 |
| `TECH_SPEC.md` | 目标技术合同和验收细节 | 实现差距应落到本文或后续 work order。 |
| `docs/testing/ACCEPTANCE.md` | acceptance 场景集合 | 需要区分 fake acceptance、security regression、real Codex opt-in。 |
| 当前代码 | 当前实现事实 | 本文需求必须能追溯到具体未完成能力。 |

### R16. Rework prompt 上下文产品化

**优先级**：P1  
**现状**：Rework issue 可以再次 dispatch 并复用 workspace/branch；但当前 prompt rendering 只传入 issue、run、workspace 的基础上下文，没有把 latest review reason 和 previous review packet summary 注入下一轮 agent prompt。  
**目标**：返工 run 的输入必须明确包含上一轮人工复核结论和安全摘要，帮助 Codex 针对 review feedback 修正，而不是重新猜测任务意图。

验收要求：

- 从 `Human Review` 执行 `send-to-rework` 后，下一次从 `Rework` dispatch 的 run prompt 包含 latest review reason。
- prompt 包含 previous review packet summary，最小字段为 `packet_no`、`run_id`、`handoff.summary`、`changed_files`、`tests`、`risks`、`verification`、`followups`。
- previous review packet summary 只能使用 redacted/safe metadata，不能把 raw prompt、raw Codex log、raw secret 或不允许的 artifact 内容注入 prompt。
- redacted prompt snapshot 能记录本轮 Rework prompt 的 metadata/hash，便于 review packet 和 diagnostics 追踪。
- Rework run 继续复用同一 workspace、branch、base_sha，并保留 cumulative diff 语义。

### R17. DB schema version guard 与只读恢复指引

**优先级**：P0  
**现状**：阶段 A 已在 app/project DB open/init 路径执行 `schema_meta.schema_version` guard，并在 diagnostics 暴露 schema version 状态；更完整的 dashboard 恢复体验仍可在后续产品化阶段补齐。  
**目标**：当 app DB 或 project DB schema version 不被当前 binary 支持时，所有入口 fail-closed，且不自动 migration、rollback、backup 或 restore。

验收要求：

- app DB 和 project DB 都必须有 `schema_meta` 表，且 `schema_version` 恰好为当前支持版本。
- DB 缺失时可以初始化 v1；`schema_meta` 缺失、`schema_version` 缺失、版本大于/小于当前支持版本、不可解析时返回 `unsupported_db_version`。
- unsupported DB version 失败必须 read-only，不写入任何 schema、runtime、issue、run、event、diagnostics artifact 或 session side effect；这不禁止 CLI、dashboard 或 diagnostics endpoint 返回只读错误响应。
- CLI、dashboard 和 diagnostics 错误信息包含 detected version、expected version、DB path 和 operator 恢复指引：使用兼容 binary、使用 operator 自行维护的备份进行人工恢复，或初始化新的 project DB。
- v1 不提供自动 migration、rollback、backup 或 restore 命令，也不在错误路径里暗示这些能力已存在。

## 5. 推荐实施顺序

### 阶段 A：真实 Codex 最小可运行闭环

覆盖 R17，以及 R1、R2、R3、R4、R6 的最小子集。阶段 A 的验收记录见 `docs/productization/V1_PHASE_A_ACCEPTANCE.md`。

目标：先建立 DB schema guard 的 fail-closed 基础；兼容 fixture 存在时，真实 Codex 可以完成一个无需 approval 的 issue，并通过 handoff 进入 Human Review。

验收命令建议：

```bash
go test ./internal/agent/codex
go test ./internal/db ./internal/store ./internal/orchestrator ./internal/toolgateway ./internal/review
python3 scripts/validate_contracts.py
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
```

### 阶段 B：approval 与安全策略闭环

覆盖 R5、R12。

目标：真实 Codex command/file/network/protected-path 请求能进入 Approval Inbox，并正确处理 approve、deny、cancel、timeout、auto-deny。

验收命令建议：

```bash
go test ./internal/security ./internal/httpapi ./internal/store ./internal/agent/codex
python3 scripts/validate_contracts.py
```

### 阶段 C：daemon 产品行为补齐

覆盖 R7、R8、R9。

目标：hook lifecycle、scheduler tick、single daemon ownership、CLI over REST 达到 TECH 合同。

验收命令建议：

```bash
go test ./internal/app ./internal/orchestrator ./internal/cli ./internal/httpapi ./internal/db ./internal/store
bash scripts/acceptance-local.sh
python3 scripts/validate_contracts.py
```

### 阶段 D：operator 体验和发布

覆盖 R10、R11、R13、R14、R15、R16。

目标：dashboard、review、Rework prompt、diagnostics、release packaging 和文档对齐达到真实用户可用状态。

验收命令建议：

```bash
go test ./...
cd web && npm run typecheck && npm test
python3 scripts/validate_contracts.py
bash scripts/acceptance-local.sh
```

## 6. 不纳入本文需求的事项

以下事项即使对“产品化”有吸引力，也仍不属于当前 v1 真实产品化推进范围：

- Linear/GitHub Issues adapter。
- 自动 push、PR、merge、publish。
- agent 自动 commit。
- 自动 retry queue/timer。
- workspace cleanup/delete/reset/rebase。
- dynamic tools/MCP。
- remote dashboard、多租户 RBAC、审计后台、admin/project settings。
- secret 读取或 secret mutation API。
- raw prompt/raw Codex log/raw secret export。
- 完整 CI/CD 或 Git provider automation。

这些能力如未来要做，应先修改 PRD/TECH 并新增独立产品方案，不能夹带进上述需求实现。
