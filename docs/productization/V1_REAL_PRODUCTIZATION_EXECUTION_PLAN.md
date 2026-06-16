# v1 真实产品化执行计划

**状态**：执行中，阶段 A、阶段 B 已完成；阶段 C 数据层完整、codex 5 轮 review 收敛 8/9 finding，C3 v1.1 WIP 状态（协调层 1 P1 未修，留作 C5 daemon lifecycle design problem）；C4 CLI over REST 与 daemon session 对齐已完成 9 轮 codex review + 6 轮 adversarial review，**进入 v1.1 WIP 路线**（trust 边界专项抽包收口）
**更新日期**：2026-06-08
**需求来源**：`docs/productization/V1_REAL_PRODUCTIZATION_GAPS.md`
**目标**：把 R1-R17 的产品化缺口拆成可落地、可审查、可验收的实施批次，推进 Local Symphony 从本地 fake runner MVP 进入真实 Codex 可运行的本地产品化版本。

## 1. 执行原则

本计划不扩展 v1 范围，只把既有 PRD、TECH、OpenAPI、schema、DB、CLI、安全和测试合同转成实施顺序。任何实现不得借本计划引入以下能力：Linear/GitHub Issues adapter、自动 push/PR/merge/publish、agent 自动 commit、自动 workspace cleanup/reset/rebase、自动 retry queue/timer、dynamic tools/MCP、remote dashboard、多租户 RBAC、secret 管理、raw prompt/raw Codex log/raw secret export。

实施时遵守以下原则：

- **先 fail-closed，再跑真实 Codex**：DB schema guard、fixture gate、安全边界和错误映射必须先稳定。
- **默认验收不依赖真实 Codex**：fake runner、fixture 和 unit/integration tests 保持可确定运行；真实 Codex 测试只走 opt-in。
- **Codex 协议隔离在 adapter 内**：orchestrator、dashboard、API 只消费 normalized events、runner result、review packet 和 approval model。
- **每个阶段都同步合同**：OpenAPI、JSON Schema、DB schema、CLI help、dashboard action inventory、测试 manifest 和文档不能产生第三套事实来源。
- **所有安全相关失败走统一终止路径**：恢复 source issue state、pause dispatch、写 run event、写系统 comment、保留 redacted diagnostics。
- **每个工作包先补测试，再实现**：至少包含一个失败测试或合同校验更新，再做代码改动。

## 2. 里程碑总览

| 阶段 | 目标 | 覆盖需求 | 完成信号 |
|---|---|---|---|
| A. 真实 Codex 最小闭环 | 有 committed fixture 时，真实 Codex 能完成一个无需 approval 的 issue 并进入 Human Review | R17、R1、R2、R3、R4、R6、R15 子集 | fake acceptance 不回退；Codex opt-in integration 可跑通最小 handoff |
| B. Approval 与安全策略闭环 | Codex command/file/network/protected-path 请求进入 operator approval，并正确处理 deny/cancel/timeout | R5、R12、R15 子集 | approval API/dashboard/CLI 与 Codex writeback 语义一致 |
| C. Daemon 产品行为补齐 | daemon 可周期调度、单 owner 写 project DB，CLI 与 daemon session 对齐 | R7、R8、R9、R15 子集 | serve tick、runtime lock、CLI over REST 形成稳定产品行为 |
| D. Operator 体验与发布 | dashboard、review、diagnostics、Rework prompt、release packaging 可面向真实用户 | R10、R11、R13、R14、R16、R15 | operator 可从 dashboard 判断、审查、返工、诊断并安装运行 |

R15 是横切要求，不作为单独最后阶段处理。每完成一个需求，都必须同步相关合同、文档和验收清单。

### 2.1 当前进度 checklist

- [x] 阶段 A / 真实 Codex 最小闭环：A0-A5 已完成，合并记录为 PR #11；验收记录见 `docs/productization/V1_PHASE_A_ACCEPTANCE.md`。
- [x] B1 / Approval API contract 补齐：已完成并合并 PR #12，Approval DTO、OpenAPI、JSON Schema、DB/fallback schema、store/httpapi、dashboard 类型与测试已对齐。
- [x] B2 / Codex approval producer 与 writeback：已完成；Codex command/file_change/network approval request 会写入 `approval_requests`，operator 决策可写回 Codex，并覆盖 deny、cancel_run 与 timeout 语义。
- [x] B3 / 安全策略执行与回归套件：默认 command/network/protected-path policy evaluator 已接入 Codex approval bridge，auto-deny 写入 `approval_requests(auto_denied)` 并返回 canonical failure code；redaction golden fixture 已补齐并纳入 contract validation，覆盖 prompt、Codex log、secret、diagnostics。
- [x] 阶段 B 收口：已完成；验收记录见 `docs/productization/V1_PHASE_B_ACCEPTANCE.md`。
- [x] 阶段 C / Daemon 产品行为补齐：进行中；C1 hook lifecycle、C2 scheduler tick loop、C3 single daemon ownership/runtime lock（**v1.1 WIP**——数据层完整、5 轮 codex 滚动 review 收敛 8/9 finding；协调层 1 P1 未修，留作 C5 daemon lifecycle design problem）已落地。C4 CLI over REST 与 daemon session 对齐已完成 9 轮 codex review + 6 轮 adversarial review，**v1.1 WIP**：trust 边界专项（fail-open 模式反复在 repo_root guard 路径重犯 + validation failure vs missing file 区分不足）作为 v1.1 收口工作（PR #22）。验收记录见 `docs/productization/V1_PHASE_C_ACCEPTANCE.md`。
- [ ] 阶段 D / Operator 体验与发布：未开始。

## 3. 全局 Definition of Done

每个工作包完成前必须满足：

- 需求验收项逐条有代码、测试或文档证据。
- 新增/变更 API 字段同步 `api/openapi.yaml`、`schemas/`、前端类型或测试 fixture。
- DB 变更同步 `db/schema/`、fallback schema、store tests、contract validation。
- CLI 行为变更同步 help、exit code、JSON 输出测试。
- Dashboard 行为变更同步 action inventory，禁止项仍不可见不可触发。
- 安全相关路径包含 redaction、path containment、token scope 或 fail-closed 测试。
- 文档更新说明当前可用能力、目标合同和 known limitations。
- 至少运行本工作包的窄范围测试；阶段收口时运行阶段门禁命令。

## 4. 阶段 A：真实 Codex 最小可运行闭环

阶段目标：先建立不可绕过的 DB 和 Codex compatibility gate，再引入 runner abstraction、Codex process lifecycle、normalized timeline 和 missing handoff continuation。阶段结束时，fake runner 仍是默认验收路径；真实 Codex 只在 fixture gate 通过时可 opt-in 运行一个无需 approval 的任务。

**进度**：已完成并合并 PR #11。阶段 A 已覆盖 A0-A5 的最小闭环要求；真实 Codex 仍受 committed fixture gate 限制，完整 approval row/writeback 属于阶段 B。

### A0. DB schema version guard

覆盖 R17。

目标：

- app DB 和 project DB 都要求 `schema_meta.schema_version` 恰好等于当前支持版本。
- 缺失 DB 可以初始化 v1；既有 DB 缺少 meta、版本不匹配或不可解析时返回 `unsupported_db_version`。
- unsupported 路径必须 read-only，并给 operator 兼容 binary、人工恢复或初始化新 DB 的指引。
- diagnostics 从真实 `schema_meta` 读取 version/status。

主要改动面：

- `internal/db`
- `internal/store`
- `internal/observability`
- `internal/cli`
- `internal/httpapi`

验收：

- existing project/app DB unsupported 时，`Open`、`InitProject`、CLI diagnostics 均 fail-closed。
- unsupported 失败不创建 schema、runtime descriptor、issue、run、event、diagnostics artifact 或 session side effect。
- OpenAPI/diagnostics schema 不出现未声明 status。

建议验证：

```bash
go test ./internal/db ./internal/store ./internal/observability ./internal/cli
python3 scripts/validate_contracts.py
```

### A1. Codex fixture gate 与兼容性元数据

覆盖 R2。

目标：

- 建立 committed Codex fixtures 和 static compatibility metadata。
- adapter 只通过 `codex --version` 与 committed metadata 判断支持范围。
- 未通过 gate 时不启动 `codex app-server`，run 失败为 `unsupported_codex_version`。
- post-launch handshake 与 metadata 不一致时失败为 `codex_protocol_error`。

主要改动面：

- `internal/agent/codex`
- `docs/codex/FIXTURE_POLICY.md`
- `docs/codex/ADAPTER_MAPPING.md`
- `docs/testing/CONTRACT_VALIDATION_MANIFEST.json`

任务拆分：

1. 定义 compatibility metadata 格式：Codex version、protocol version、schema version、supported notification/request method 列表。
2. 增加 fixture 目录：`testdata/schema/<codex-version>/` 与 `testdata/transcripts/<codex-version>/`。
3. 实现 `codex --version` parser 与 fixture lookup。
4. 实现 prelaunch gate 单元测试：supported、missing fixture、malformed version、version drift。
5. 实现 handshake metadata 校验测试：metadata mismatch 映射 `codex_protocol_error`。

验收：

- 无 committed fixture 时真实 Codex 路径 fail-closed。
- generated protocol/schema metadata 不依赖启动真实 app-server 动态发现。
- opt-in integration 使用环境变量显式开启。

建议验证：

```bash
go test ./internal/agent/codex
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
python3 scripts/validate_contracts.py
```

### A2. Runner abstraction 与 orchestrator 接入

覆盖 R1。

目标：

- 定义 `Runner`、`RunRequest`、`RunResult` 边界。
- orchestrator dispatch 可选择 fake 或 codex runner。
- `runner_kind` 持久化准确反映 fake/codex。
- fake runner 保持确定性测试路径，不污染真实 Codex adapter 逻辑。

主要改动面：

- `internal/orchestrator`
- `internal/agent/fake`
- `internal/agent/codex`
- `internal/store`
- `internal/core`

任务拆分：

1. 抽出 runner interface，输入只包含 issue、run、workspace、workflow snapshot、tool endpoint/token 和 timeout policy。
2. fake runner 实现 interface，保持现有 acceptance 行为。
3. codex runner 先实现 fail-closed stub，接入 A1 fixture gate。
4. orchestrator 用 workflow/runtime 配置选择 runner。
5. 增加 runner_kind 持久化和 run detail 测试。

验收：

- 默认测试与 acceptance 仍走 fake runner。
- Codex unsupported 时 run 进入统一 failure path，dispatch pause。
- fake/codex 不共享协议解析分支。

建议验证：

```bash
go test ./internal/orchestrator ./internal/agent/fake ./internal/agent/codex ./internal/store
bash scripts/acceptance-local.sh
```

### A3. Codex process、stdio transport 与生命周期管理

覆盖 R3。

目标：

- 可控启动 `codex app-server`，cwd 固定为 issue workspace。
- 管理 process group、stdio transport、startup/turn/stall timeout。
- cancel、approval cancel_run、reconciliation、daemon shutdown、timeout 均能终止进程。
- stderr 只进入 redacted diagnostics，不通过 API/dashboard 暴露 raw log。

主要改动面：

- `internal/agent/codex`
- `internal/orchestrator`
- `internal/app`
- `internal/observability`
- `internal/store`

任务拆分：

1. 实现 process launcher：命令、cwd、env 与 `TECH_SPEC.md §10.1` 对齐。
2. 实现 stdio JSON-RPC 或协议 transport 的 reader/writer 边界。
3. 实现 startup timeout、turn timeout、stall timeout，并映射 failure code。
4. 实现 process group terminate/kill escalation。
5. 将 stderr redaction summary 写入 diagnostics source，不暴露 raw log。
6. 增加 cancellation 与 reconciliation 测试。

验收：

- startup failure -> `codex_startup_failed`。
- turn timeout -> `turn_timeout`。
- stall timeout -> `stall_timeout`。
- 所有 terminal failure 都恢复 source state、pause dispatch、写 event/comment。

建议验证：

```bash
go test ./internal/agent/codex ./internal/orchestrator ./internal/app ./internal/observability
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
```

### A4. Codex 事件归一化与 run timeline

覆盖 R4。

目标：

- Codex protocol notification 转换为 normalized run event。
- UI/API 不消费 raw Codex payload。
- 大 payload 或 raw-ref 只作为受限 artifact metadata。

主要改动面：

- `internal/agent/codex`
- `internal/orchestrator`
- `internal/store`
- `internal/httpapi`
- `web/src`
- `schemas/run_event.schema.json`

任务拆分：

1. 建立 Codex notification -> normalized event 映射表。
2. 支持 agent process/handshake/thread/turn/protocol/process exited events。
3. 支持 approval requested/resolved event 占位，后续 B 阶段接 writeback。
4. 支持 tool call observation event，但 Tool Gateway record 仍由 daemon 记录。
5. 更新 run detail API 和 dashboard timeline consumption。
6. 增加 redaction 与 large payload tests。

验收：

- `run_events.data_json` 全部 redacted。
- dashboard Run Detail 只展示 normalized timeline。
- raw payload 不进入 API/dashboard。

建议验证：

```bash
go test ./internal/agent/codex ./internal/orchestrator ./internal/store ./internal/httpapi
python3 scripts/validate_contracts.py
```

### A5. Missing handoff continuation

覆盖 R6。

目标：

- 主 turn 完成但没有 handoff 时，在配置允许下发起一次同 session/thread 的 handoff continuation。
- continuation 仍未 handoff 时走 `missing_handoff` failure path。
- continuation prompt 只请求 handoff，不重新发送完整任务 prompt。

主要改动面：

- `internal/orchestrator`
- `internal/agent/codex`
- `internal/config`
- `internal/store`
- `internal/toolgateway`

任务拆分：

1. 明确 `agent.max_handoff_continuations` config parse 与默认值。
2. runner result 区分 completed-with-handoff、completed-without-handoff、continuation-requested。
3. Codex runner 支持同 session/thread continuation request。
4. fake runner fixture 保持 missing handoff 可测。
5. 增加 `0` 和 `1` 两种配置下的 orchestrator tests。

验收：

- `max_handoff_continuations=1` 首次 missing handoff 不立即终止。
- `max_handoff_continuations=0` 首次 missing handoff 直接 failure pause。
- continuation 不扩大执行范围。

建议验证：

```bash
go test ./internal/orchestrator ./internal/agent/codex ./internal/config ./internal/toolgateway
bash scripts/acceptance-local.sh
```

阶段 A 收口门禁：

```bash
go test ./internal/agent/codex
go test ./internal/db ./internal/store ./internal/orchestrator ./internal/toolgateway ./internal/review ./internal/observability
python3 scripts/validate_contracts.py
bash scripts/acceptance-local.sh
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
```

## 5. 阶段 B：Approval 与安全策略闭环

阶段目标：真实 Codex 触发 command、file、network、protected path 请求时，Local Symphony 能生成 approval row，等待 operator 决策或 timeout，并把决策写回 Codex。安全策略必须可执行、可验证、可诊断。

### B1. Approval API contract 补齐

覆盖 R5 的 API/model 部分。

**进度**：已完成并合并 PR #12。当前 API、OpenAPI、`schemas/approval_request.schema.json`、store/httpapi projection、dashboard 类型和 Approval Inbox 已统一使用结构化 Approval DTO；`request_json` 和 `decision_json` 保持内部字段，不在 API/dashboard 暴露。

目标：

- Approval API 返回 `action_summary`、`risk_level`、`policy_match`、`requested_at`、`timeout_ms`、`expires_at`、`resolved_at`。
- OpenAPI、schema、store model、dashboard 类型一致。

主要改动面：

- `internal/store`
- `internal/httpapi`
- `api/openapi.yaml`
- `schemas/`
- `web/src`

任务拆分：

1. [x] 扩展 approval response DTO，不暴露 opaque raw request。
2. [x] DB 现有 `request_json` 保持内部记录，API projection 输出结构化字段。
3. [x] 更新 OpenAPI 和 dashboard client/type tests。
4. [x] 增加 pending/resolved/timeout approval API tests。
5. [x] 增加 Approval response JSON Schema 与 OpenAPI/DB/fallback schema 漂移门禁。

验收：

- [x] Dashboard Approval Inbox 不解析 opaque `request_json`。
- [x] API 字段与 TECH 对齐。
- [x] decision 成功后 `decision_json` 内部落库，但 API/OpenAPI/dashboard 不暴露。
- [x] `approval_requests.kind/status/timeout_ms` 约束在主 DB schema、fallback schema 和 contract validation 中一致。

### B2. Codex approval producer 与 writeback

**进度**：已完成。Codex adapter 在 handshake 后接收 `command`、`file_change`、`network` approval request，写入结构化 `approval_requests`，等待 operator 决策或 timeout，并通过 stdin 向 Codex 写回 `approval_decision`。`deny` 只写回拒绝并继续读取 Codex；`cancel_run` 走 `operator_cancelled` 终止路径；approval timeout 标记 row 为 `timeout` 并返回 `approval_timeout`。

覆盖 R5 的 Codex bridge 部分。

目标：

- Codex command/file_change/network approval request 进入 `approval_requests`。
- 决策能写回 Codex：`approve_once`、`approve_for_run`、`approve_for_session`、`deny`、`cancel_run`。

主要改动面：

- `internal/agent/codex`
- `internal/orchestrator`
- `internal/store`
- `internal/httpapi`
- `internal/cli`

任务拆分：

1. [x] Codex adapter 将 approval request normalizes 成 store row。
2. [x] orchestrator 等待 approval resolution 或 timeout。
3. [x] 实现 deny 只拒绝当前动作，不取消 run。
4. [x] 实现 cancel_run 立即终止 run，失败码 `operator_cancelled`，并 pause dispatch。
5. [x] 实现 approval timeout 后不可再决策。
6. [x] 增加 Codex fixture 测试覆盖 writeback。

验收：

- [x] approval timeout -> `approval_timeout`。
- [x] deny 不直接设置 `operator_cancelled`。
- [x] cancel_run 使用 `operator_cancelled`，走终止 pause 路径。

### B3. 安全策略执行与回归套件

覆盖 R12。

**进度**：已完成。policy execution slice、Tool Gateway protected artifact hard-deny、redaction golden fixture 与安全回归均已验证；redaction fixture 位于 `docs/testing/redaction-golden/redaction-golden.json`，并由 `scripts/validate_contracts.py` 校验 manifest metadata 与 fixture case content。

目标：

- command policy 支持 allow/review/deny。
- network 默认 deny，不声明 OS-level isolation。
- protected path read/write 通过 Codex-mediated 路径 auto-deny 或 review。
- redaction golden fixtures 覆盖 prompt、Codex log、secret、diagnostics。

主要改动面：

- `internal/security`
- `internal/agent/codex`
- `internal/toolgateway`
- `internal/observability`
- `tests/`
- `docs/testing`

任务拆分：

1. 建立 command/network/protected-path policy evaluator。
2. 接入 approval bridge：review 进入 approval，deny 映射 failure code。
3. Tool Gateway `artifact.attach` 保持 daemon hard-deny，且不直接终止 run。
4. 增加 redaction golden fixture。
5. 增加不依赖真实 Codex 的安全回归。

验收：

- command denied -> `command_denied`。
- network denied -> `network_denied`。
- protected path denied -> `protected_path_denied`。
- 默认安全测试不需要外部网络或真实 Codex。

阶段 B 收口门禁：

```bash
go test ./internal/security ./internal/httpapi ./internal/store ./internal/agent/codex ./internal/orchestrator ./internal/toolgateway
python3 scripts/validate_contracts.py
bash scripts/acceptance-local.sh
```

## 6. 阶段 C：Daemon 产品行为补齐

阶段目标：让 daemon 成为真实产品控制面，而不是仅靠手动命令直连 SQLite。补齐 hook lifecycle、scheduler tick、single daemon ownership 和 operator CLI over REST。

### C1. Hook lifecycle 完整落地

覆盖 R7。

**进度**：已完成。`after_create` 仅在新 workspace 创建后运行，`before_run` 在 runner/token 创建前运行；两者失败分别映射 `after_create_failed` / `before_run_failed`，通过现有 `Store.FailRun` 终止 run、恢复 issue source state 并 pause dispatch。hook 输出沿用 bounded/redacted 事件路径。

目标：

- 新 workspace 创建后运行 `after_create`。
- 每次 run 启动前运行 `before_run`。
- terminal worker outcome 尝试 `after_run`。
- hook failure 使用对应 failure code，恢复 issue 并 pause dispatch。

主要改动面：

- `internal/workspace`
- `internal/orchestrator`
- `internal/config`
- `internal/store`
- `internal/review`

任务拆分：

1. [x] 建立 hook executor 统一输出截断和 redaction。
2. [x] workspace 创建成功后运行 after_create。
3. [x] runner 启动前运行 before_run。
4. [x] terminal outcome 后运行 after_run。
5. [ ] review packet 和 diagnostics 纳入 hook summary。

验收：

- [x] `after_create_failed`、`before_run_failed` 不启动 runner。
- [x] hook 输出受 `hooks.max_output_bytes` 限制。
- [ ] hook summary 纳入 review packet / diagnostics。

### C2. Scheduler tick loop 与 dispatch preflight 统一

覆盖 R8 的 scheduler 部分。

**进度**：已完成。`symphony serve` 会按 workflow `polling.interval_ms` 启动 scheduler tick loop；tick 候选状态来自 effective `tracker.dispatch_candidate_states`，默认仍为 `Ready` / `Rework`；scheduler 和手动 dispatch 共用同一 `ClaimRun` preflight 路径。tick error 不会终止 daemon，会记录到 stderr 后等待下一次 tick；取消 serve 会停止后续 tick，已启动的 tick drain 后再关闭 store。

目标：

- [x] `symphony serve` 根据 workflow polling interval 启动 tick loop。
- [x] tick 只选择 `Ready` 和 `Rework`。
- [x] tick 和手动 dispatch 共享同一 preflight。
- [x] failure 后 pause 的 issue 不会自动 redispatch。

主要改动面：

- `internal/app`
- `internal/orchestrator`
- `internal/store`
- `internal/config`

任务拆分：

1. [x] 在 serve runtime 启动 tick loop，支持 graceful shutdown。
2. [x] 统一 manual dispatch 和 tick dispatch 的 eligibility/preflight。
3. [x] 增加 polling interval config tests。
4. [x] 增加 paused issue 不 redispatch 的 regression tests。

验收：

- [x] `Working` 不作为正常 tick 候选。
- [x] stale active run reconciliation 后仍遵守 dispatch pause。

已知边界：

- tick 内部 dispatch 仍同步执行；如果 in-flight tick 永久卡住，store close 会延后到 tick drain 后。完整 daemon owner、runtime lock 和 shutdown cancellation 属于 C3/Codex lifecycle 后续收口。

### C3. 单 daemon 项目所有权与 runtime lock

覆盖 R8 的 ownership 部分。

**进度**：v1.1 WIP（数据层完整、review 充分；协调层留作后续 C5/设计层问题）。

- **数据层（schema / nonce / heartbeat / diagnostics / migrated 兼容）已 5 轮 codex 滚动 review 充分收敛**，无新 finding。
- **协调层（scheduler nonce gate / shutdown ordering / long-running tick race）暴露 1 P1 未修**，属于 open design problem。
- 5 轮累计 **4P1 + 5P2 = 9 finding**；前 4 轮的 8 个已修，最后 1 个 P1 留作 v1.1 WIP 已知限制。
- 已知限制：**shutdown ordering** — 在长跑 scheduler tick 与新 owner 并发接管之间，daemon 关闭序列的顺序仍可在小窗口内产生 1 个 P1（round-5 review 暴露，未修）。该问题属于 C5 daemon lifecycle design problem 的子集，**不强制在 C3 收口内解决**。
- 4 个 commit 保留在 worktree（`codex/v1-productization-c3-owner-nonce` 分支，未 push / 未 PR / 未 merge）：
  - `dbf4c6e` C3 主体（owner nonce / heartbeat / diagnostics / 收口测试）
  - `ee4f8a5` round-2：PID 存活判断先于 TTL
  - `e136dfc` round-3：scheduler dispatch nonce gate + migrated row reap + `daemon_already_running` schema
  - `d8840d2` round-4：live migrated owner 保留 + 测试用 context-cancel 替代 SIGINT

数据层已实现：
- app DB `runtime_descriptors` owner guard：`serve` 在任何 project DB mutation 前获取 runtime owner
- active owner conflict 不写 CLI session、不执行 stale-run reconciliation
- shutdown 只释放当前 daemon nonce owner
- dead PID / PID 复用 / heartbeat 停滞 / 既有 v1 app DB 升级场景可恢复
- diagnostics 只展示 8 字符 owner_nonce fingerprint + heartbeat_at / heartbeat_ttl_ms / acquired_at，不暴露 owner_nonce 明文
- heartbeat ticker 与 reap ticker 在 SIGINT / ShutdownContext 时优雅停止
- migrated row + 旧 PID 仍活时**不 reap**（保护 live legacy daemon）

目标：

- 每个 project DB 同时只有一个活跃 daemon owner。
- stale descriptor/lock owner 可安全恢复（包括 PID 复用）。
- runtime descriptor 不包含 secret/token；owner_nonce 仅 8 字符 fingerprint 暴露到 diagnostics。
- 既有 v1 app DB 升级到 owner nonce/heartbeat schema 通过 idempotent migration 完成。

主要改动面：

- `internal/app`
- `internal/store`
- `internal/db`
- `internal/db/schema.go`（含 `MigrateAppSchema`）
- `internal/httpapi`
- `internal/observability`
- `db/schema/v1_app.sql`
- `schemas/diagnostics.schema.json`
- `api/openapi.yaml`
- `scripts/validate_contracts.py`
- `docs/testing/CONTRACT_VALIDATION_MANIFEST.json`

任务拆分（C3 收口追加 work package）：

1. **WP-1**：`runtime_descriptors` 新增 `owner_nonce` / `heartbeat_at` / `heartbeat_ttl_ms` / `acquired_at` 列；`runtime_owner_events` 表记录 reap 事件；idempotent `MigrateAppSchema` 升级既有 v1 app DB。
2. **WP-2**：`app/serve` 启动时 `crypto/rand` 生成 32+ 字节 nonce；周期 ticker 刷新 `heartbeat_at`；conflict 返回 `core.ErrDaemonAlreadyRunning`；heartbeat goroutine 优雅停止。
3. **WP-3**：基于 (heartbeat_at + heartbeat_ttl_ms) 的 `reapStaleRuntimeDescriptor`；`serve` 启动前先 reap，且周期 reap（60s）；PID 复用不阻断 reap。
4. **WP-4**：`RuntimeDescriptorSnapshot` 投影不包含 `owner_nonce` 明文，仅 fingerprint；OpenAPI/JSON Schema `DiagnosticsRuntimeDescriptor` 同步。
5. **WP-5**：`validate_contracts.py` 与 `CONTRACT_VALIDATION_MANIFEST.json` 同步描述 runtime ownership。
6. **WP-6**：PID 复用集成测试、文档收口、阶段 C 验收记录。

验收：

- 同 project 第二个 daemon 在前一个 owner 的 heartbeat 仍有效时返回 `daemon_already_running`，不写 CLI session，不执行 stale-run reconciliation。
- 既有 v1 app DB 在 Open 时自动升级到 v1+（含 owner_nonce/heartbeat/reap events），无需外部 migrate 命令。
- crash / heartbeat 停滞 / PID 复用场景可被新 daemon 接管，并写入 `runtime_owner_events` reap 事件。
- `diagnostics` JSON / OpenAPI / JSON Schema 不暴露 `owner_nonce` 明文；operator 只能看到 8 字符 fingerprint + heartbeat 时间戳。
- `python3 scripts/validate_contracts.py` 通过；`go test ./internal/db ./internal/store ./internal/app ./internal/observability ./internal/httpapi` 全绿。

#### C3 v1.1 WIP — 5 轮 codex 滚动 review 累计 finding 表

| Round | Commit | Severity | Finding | 状态 |
|---|---|---|---|---|
| 1 | `dbf4c6e` | P1 | heartbeat 失锁时 Serve 仍继续调度 | 已修（round-1） |
| 1 | `dbf4c6e` | P2 | diagnostics 默认 projection 缺新字段 | 已修（round-1） |
| 2 | `ee4f8a5` | P2 | crashed daemon 的 fresh heartbeat 阻止新 daemon 接管 | 已修（round-2） |
| 3 | `e136dfc` | P1 | scheduler tick 独立于 heartbeat 检测 ownership 失锁 | 已修（round-3） |
| 3 | `e136dfc` | P2 | migrated row 空 nonce 永久占锁 | 已修（round-3） |
| 3 | `e136dfc` | P2 | `daemon_already_running` 未列入 schema enum | 已修（round-3） |
| 4 | `d8840d2` | P1 | live migrated PID 在 takeover 时被误 reap | 已修（round-4） |
| 4 | `d8840d2` | P2 | 测试用 `os.FindProcess+SIGINT` 可能误杀 `go test` 进程 | 已修（round-4） |
| 5 | （无 commit）| P1 | **shutdown 顺序与 long-running tick 的并发接管** | **不修（C5 design problem）** |

**已知限制（v1.1 WIP）**：

- **协调层 P1 未修**：在长跑 scheduler tick 与新 owner 并发接管之间，daemon 关闭序列的顺序仍可在小窗口内产生并发接管。该问题属于 C5 daemon lifecycle design problem 的子集，**不强制在 C3 收口内解决**。
- 替代方案：在 C5 阶段引入统一的"drain-then-exit"生命周期（先停 dispatcher、停 scheduler、停 heartbeat、停 reap、停 HTTP server，最后 close store）；此改造需协调多 goroutine 的 cancel propagation，超出 C3 收口范围。
- 数据层在 v1.1 WIP 状态下是稳定的：所有数据层相关测试 + contract validation + diagnostics schema 在 5 轮 review 中无新 finding。
- 阶段 D 之前如需补 C5 的 shutdown lifecycle，需新建 worktree 分支（例如 `codex/v1-productization-c5-daemon-lifecycle`），并在主计划中追加 C5 子章节。

### C4. CLI over REST 与 daemon session 对齐

覆盖 R9。

目标：

- 普通 operator CLI 在 daemon 可用时通过 REST API 执行。
- 缺失 daemon/session 时提示 `symphony serve/open` 或本地登录指引。
- `symphony tool ...` 保持 Tool Gateway token 路径。

主要改动面：

- `internal/cli`
- `internal/httpapi`
- `internal/security`
- `internal/app`
- `README.md`

任务拆分：

1. 建立 CLI daemon discovery/session client。
2. 将 status、issue、run、approval、workflow、diagnostics 等 operator 命令切到 REST。
3. 保持 JSON 输出 envelope-unwrapped stable object。
4. 对齐 API error code 与 CLI exit code。
5. 保持 tool CLI 不获得 operator REST 权限。

验收：

- `invalid_request=2`，workflow/prompt 相关错误为 `9`，其余操作冲突为 `7`。
- daemon unavailable 错误可操作。

#### C4 v1.1 WIP：trust 边界专项收口

C4 已完成 9 轮 codex review 和 6 轮 adversarial review（详见 C4 PR [#22](https://github.com/shuanzi/local-symphony/pull/22) 与 `V1_PHASE_C_ACCEPTANCE.md` §5.2），覆盖 CLI/daemon session 对齐、auth bypass 修复、loopback 守卫、project_id 守卫、repo_root 守卫、degraded logout 状态追踪、openDescriptor 信任链、logoutRevokeFromFile Discover 路由、login UX、validate workflow 标注、fail-closed repo_root 校验、sticky project-scoped logout 等。C4 收口进入 v1.1 WIP 路线，不再开 round 7。

**累计 finding 表**（0 critical / 4 important / 18 minor）：

| 轮次 | 级别 | finding | 状态 |
|---|---|---|---|
| codex review 1 | important | auth bypass (CLI bearer 不经 project_id / loopback 校验直接送 daemon) | fixed |
| codex review 1 | minor | `decodeArray` 不区分 env 优先级 + 修 | fixed |
| codex review 1 | minor | workflow validate 走 GET 应改 POST | fixed |
| codex review 1 | minor | URL query 拼接不规范 | fixed |
| codex review 1 | minor | `symphony login` 缺友好提示 | fixed |
| codex review 2 | minor | login probe 缺失（不发 `/auth/session` 探测） | fixed |
| codex review 2 | minor | mutating retry 链路不显式 | fixed |
| codex review 2 | minor | legacy logout 不识别无 project 文件 | fixed |
| codex review 3 | minor | err wrap 路径不一致 | fixed |
| codex review 3 | minor | `symphony login` 失败未统一退出 7 | fixed |
| codex review 3 | minor | acceptance 缺 poll step | fixed |
| codex review 3 | minor | fallback schema 与原 schema 不一致 | fixed |
| codex review 4 | minor | `workflow validate` 未标 non-mutating | fixed |
| adversarial round 1 | important | project_id 不匹配 → ErrSessionMissing wrap | fixed |
| adversarial round 1 | important | logout 缺 server-side revoke 路径 | fixed |
| adversarial round 2 | minor | loopback host guard 不严格 | fixed |
| adversarial round 2 | minor | degraded logout 状态不外露 | fixed |
| adversarial round 2 | minor | revoke envelope shape 不稳定 | fixed |
| adversarial round 3 | important | `logoutRevokeFromFile` 不走 Discover | fixed |
| adversarial round 3 | minor | project_id guard 在 logout 路径漏写 | fixed |
| adversarial round 4 | important | session repo_root guard 漏 fail-open | fixed |
| adversarial round 4 | minor | legacy logout 不按 project_id scoping | fixed |
| adversarial round 5 | important | openDescriptor 信任链未走 Discover | fixed |
| adversarial round 5 | important | logoutRevoke 不追踪 per-source degrade | fixed |
| adversarial round 5 | minor | `loginResolveProject` 不传播 repo_root | fixed |
| adversarial round 6 | important | EvalSymlinks 失败 → fail-open（错接外国 bearer） | fixed |
| adversarial round 6 | important | logout 删 unvalidated project-scoped 文件 | fixed |

**已知限制（指向 C5 trust 边界专项 / v1.1 收口）**：

1. **fail-open 模式反复在 `repo_root` guard 路径重犯**：round 4 在 `internal/daemonclient/session.go` 修过一次（注释甚至写明"can't normalise; don't block on a host-side issue"），round 6 又在同文件 + 镜像 `internal/cli/cli.go` 的 `checkCLISessionRepoRoot` 修第二次。该 fail-open 注释仍是 repo_root 路径的明显反模式；建议抽出 `internal/trustboundary` 共享包，统一管理 trust checks 的 fail-closed / fail-open 语义，禁止在每个调用点重写一遍 guard。
2. **validation failure vs missing file 区分不足**：`logoutRevokeFromFile` round 5 把所有非 `IsNotExist` 错误归为同一 `usable=false` 桶，round 6 才发现这导致 "validation 失败时 project-scoped 文件被错误删除"。`loadCLISessionToken` / `readCLISessionToken` / `openDescriptor` 同样存在 `IsNotExist` 一刀切、validation 失败与 missing 不区分的问题；trustboundary 包应统一返 `{usable, validationFailed, degraded}` 三元组。
3. **镜像检查未去重**：`daemonclient` 和 `cli` 两个包各自有 `checkSessionRepoRoot` / `checkCLISessionRepoRoot` + `normaliseRepoRootForCompare`，修改时必须同时改两边（已 round 6 验证）。trustboundary 包应作为唯一 source of truth。
4. **project_id 不匹配的可观测性**：`ReadSessionFile` 在 project_id 不匹配时只返 `ErrUnauthorized`，operator 看到的是"session not valid for this project"——但不能区分 copied-DB、stale session、foreign-bearer-attempt。建议在 trustboundary 加结构化 details（如 `{expected: "prj_abc", got: "prj_xyz", reason: "project_id_mismatch"}`）。

**C5 trust 边界专项（v1.1 收口工作）**：

- 抽 `internal/trustboundary` 共享包：`CheckRepoRoot(persisted, caller) error`、`CheckProjectID(persisted, caller) error`、`CheckAPIURL(persisted, expected) error`，统一 fail-closed。
- 重构 `internal/daemonclient/session.go` 和 `internal/cli/cli.go` 的镜像检查为单一调用。
- 改 `ReadSessionFile` / `loadCLISessionToken` / `logoutRevokeFromFile` 的错误类型为 `{usable bool, validationFailed bool, degraded bool, err error}` 四元组。
- 在 `cli` 错误 envelope 加 `validation_failure_kind` 字段（"project_id_mismatch" / "repo_root_mismatch" / "repo_root_unresolvable" / "api_url_loopback_violation"），让 operator 区分 copied-DB / stale session / foreign-bearer-attempt。
- 端到端覆盖：copied-DB + 原始 checkout 删除、copied-DB + 原始 checkout 移动、API URL 错指 + 探测成功、API URL 非 loopback、API URL 指向 wrong project daemon。

**当前 C4 已 ship 能力**（v1 范围内已可用）：

- `symphony status` / `issue *` / `run *` / `approval *` / `review *` / `workflow *` / `diagnostics *` 等 operator 命令在 daemon 可用时走 REST；不可用时降级 local store。
- CLI bearer session 经 loopback + project_id + repo_root 三重 fail-closed 校验。
- `symphony login --logout` 走 Discover /health project_id 守卫，per-source degraded 状态 sticky，project-scoped validation 失败时文件保留。
- `symphony open` 通过 Discover 信任链避免误送 bearer 到 foreign daemon。
- exit code 严格按 `invalid_request=2` / `workflow_or_prompt=9` / `其余操作冲突=7` / `daemon_unavailable=7`。

阶段 C 收口门禁：

```bash
go test ./internal/app ./internal/orchestrator ./internal/cli ./internal/httpapi ./internal/db ./internal/store ./internal/workspace
bash scripts/acceptance-local.sh
python3 scripts/validate_contracts.py
```

## 7. 阶段 D：Operator 体验与发布

阶段目标：把真实运行路径包装成 operator 可以稳定使用、审查、诊断、返工和安装的本地产品。

### D1. Review Packet API 与 dashboard 可审查性

覆盖 R10。

目标：

- Review API 返回 summary、acceptance criteria、handoff、changed files、diff、tests、risks、verification、approvals、tool calls、git 和 How to Continue。
- dashboard 只通过 Review API 与 Artifact API 获取内容。
- raw prompt/raw Codex log/raw secret artifact 拒绝内容读取。

主要改动面：

- `internal/review`
- `internal/httpapi`
- `internal/store`
- `web/src`
- `schemas/review_packet.schema.json`

任务拆分：

1. 扩展 review packet structured projection。
2. Artifact API 增加 refusal case 测试。
3. Dashboard Review Packet 页面按 structured source 展示。
4. `symphony review path` 保持只输出 metadata/path diagnostics。
5. review packet failure 不进入 Human Review。

验收：

- operator 不需要读 filesystem 即可完成审查。
- 禁止 artifact 的 `content_url=null` 或返回明确 refusal。

### D2. Dashboard 产品化补齐

覆盖 R11。

目标：

- 所有页面覆盖 loading、empty、auth error、daemon unavailable、artifact refusal、command error。
- Overview 展示 workflow、running runs、pending approvals、failed runs、Human Review、paused issues、Codex availability、recent events。
- Approval Inbox、Review Packet、Diagnostics 与 API contract 对齐。
- action inventory 继续禁止未来动作。

主要改动面：

- `web/src`
- `internal/httpapi`
- `api/openapi.yaml`
- `schemas/`

任务拆分：

1. 生成或同步 API client/types。
2. 页面级状态覆盖。
3. Overview 数据聚合。
4. Approval Inbox 使用结构化字段。
5. Review Packet 页面改为 structured source。
6. Dashboard action inventory regression。

验收：

- dashboard 不出现 publish、PR、backup/restore、workspace delete、secret、issue delete、project settings 等未来动作。
- auth/session 失效有明确恢复路径。

### D3. Codex availability 与 diagnostics 产品化

覆盖 R14。

目标：

- diagnostics 展示 installed Codex version、fixture support、selected protocol/schema metadata、最后一次 preflight。
- Overview 和 `symphony status` 展示 Codex availability。
- diagnostics export 保持 redacted-only。

主要改动面：

- `internal/agent/codex`
- `internal/observability`
- `internal/cli`
- `internal/httpapi`
- `web/src`

任务拆分：

1. adapter 提供 preflight summary。
2. diagnostics 纳入 Codex version、fixture support、metadata、last failure。
3. status API/CLI 增加 Codex availability。
4. dashboard Overview 链接 Diagnostics。
5. diagnostics export redaction regression。

验收：

- 无 fixture 时显示 `unsupported_codex_version` 风险。
- 不导出 raw Codex log、raw prompt 或 raw secret。

### D4. Rework prompt 上下文产品化

覆盖 R16。

目标：

- 从 `Human Review` send-to-rework 后，下一轮 Rework prompt 包含 latest review reason。
- prompt 包含 previous review packet safe summary。
- prompt snapshot 记录 metadata/hash。
- Rework 继续复用 workspace、branch、base_sha，并保留 cumulative diff 语义。

主要改动面：

- `internal/orchestrator`
- `internal/review`
- `internal/store`
- `internal/agent/codex`
- `schemas/review_packet.schema.json`

任务拆分：

1. 定义 previous review packet safe summary DTO。
2. prompt renderer 注入 latest review reason 和 safe summary。
3. 禁止 raw prompt/log/secret artifact 内容进入 prompt。
4. prompt snapshot 记录 Rework metadata/hash。
5. 增加 Rework cumulative diff regression。

验收：

- Rework prompt 可追溯人工反馈。
- redacted prompt snapshot 可用于 review packet 和 diagnostics 追踪。

### D5. Release packaging 与安装体验

覆盖 R13。

目标：

- 产出明确 release artifact，例如 `dist/symphony`。
- dashboard 静态资产随安装布局可被 daemon 安全发现，API/Tool Gateway 路由优先。
- release notes 声明 Go、Node、SQLite、Git、Codex 支持版本。
- quickstart、README、CLI help 与实际命令一致。

主要改动面：

- `scripts/`
- `cmd/symphony`
- `internal/app`
- `web`
- `README.md`
- release notes

任务拆分：

1. 定义 release build layout。
2. 选择 dashboard asset embedding 或 install layout，并补路由优先测试。
3. 建立 macOS arm64/x64、Linux x64 release blocking checklist。
4. 更新 quickstart、README、CLI help。
5. 记录 Windows best-effort 限制，如提供。

验收：

- 用户不需要从源码拼装 dashboard dist。
- release notes 不声称未完成能力。

### D6. 文档、合同与验收对齐

覆盖 R15 的阶段收口。

目标：

- README 保持当前可用能力，不误导真实 Codex 默认可用状态。
- PRD/TECH 如作为目标合同，release notes 标注当前达成度。
- `docs/codex/*` 与 adapter、fixtures、测试同步。
- `docs/testing/ACCEPTANCE.md` 区分 fake acceptance、安全回归、real Codex opt-in acceptance。

主要改动面：

- `README.md`
- `PRD.md`
- `TECH_SPEC.md`
- `docs/codex`
- `docs/testing`
- `docs/productization`

任务拆分：

1. 为每个已完成 R 项增加 status note。
2. 更新 acceptance 分类和执行命令。
3. 更新 known limitations。
4. 确认禁止能力在 README、dashboard action inventory、API 文档中一致。

阶段 D 收口门禁：

```bash
go test ./...
cd web && npm run typecheck && npm test
python3 scripts/validate_contracts.py
bash scripts/acceptance-local.sh
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
```

## 8. 跨阶段依赖图

```text
R17
  -> R1
R2
  -> R1
  -> R3
R1 + R2 + R3
  -> R4
  -> R6
R4 + R3
  -> R5
R5
  -> R12
R1 + R4 + R5
  -> R10
R5 + R10 + R14
  -> R11
R3 + R4
  -> R14
R1 + R6 + R10
  -> R16
R7 + R8 + R9
  -> release readiness
R15
  -> every phase gate
```

关键串行约束：

- R2 必须早于真实 Codex process 默认启用。
- R3 必须早于 approval writeback 和 availability 诊断。
- R4 必须早于 dashboard run timeline 产品化。
- R5 必须早于安全策略生产闭环。
- R10/R14 必须早于 dashboard 产品化收口。
- R15 必须在每个阶段同步，不能留到最终补文档。

## 9. 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| Codex protocol 变动 | adapter 启动但事件或 approval 语义不兼容 | fixture gate fail-closed；metadata mismatch 映射 `codex_protocol_error` |
| 真实 Codex 测试不稳定 | CI 不可重复 | 默认 CI 只跑 fake/fixture；真实测试 opt-in |
| raw log/prompt 泄露 | 安全边界破坏 | adapter 层 redaction；API/dashboard refusal；golden fixtures |
| approval 与 cancel 语义混淆 | operator 操作导致错误 run 状态 | `deny` 与 `cancel_run` 分离测试；统一 failure path |
| CLI 直连 SQLite 与 daemon 状态分叉 | session、CSRF、runtime owner 语义不一致 | CLI over REST；Tool Gateway CLI 独立 token 路径 |
| dashboard 超前展示未来能力 | 用户误操作或误解 v1 能力 | action inventory regression；禁止项文档同步 |
| release artifact 与开发布局不一致 | 安装后 dashboard 或 schema 发现失败 | release layout tests；asset route priority tests |

## 10. 建议执行节奏

每个工作包按以下节奏推进：

1. 复读对应 R 项和 TECH/PRD 相关段落，列出不可做事项。
2. 写聚焦失败测试或合同 fixture。
3. 做最小实现，避免跨阶段重构。
4. 跑窄范围测试。
5. 更新 OpenAPI/schema/docs/dashboard action inventory。
6. 跑阶段相关合同校验。
7. 做只读审查，先修 Critical/Important。
8. 更新本计划或 gap 文档状态。

每个阶段收口前至少生成一份阶段验收记录，包含：

- 完成的 R 项和未完成子项。
- 已运行命令和结果。
- 已知限制。
- 是否允许进入下一阶段。

## 11. 第一批建议 work orders

为了降低风险，建议从以下顺序开始：

1. [x] **A0 / R17**：DB schema version guard 与 diagnostics 真实版本读取。
2. [x] **A1 / R2**：Codex fixture metadata 和 `codex --version` gate。
3. [x] **A2 / R1**：Runner interface 与 fake runner 迁移。
4. [x] **A3 / R3**：Codex process launcher + startup timeout + redacted stderr。
5. [x] **A4 / R4**：最小 normalized timeline。
6. [x] **A5 / R6**：missing handoff continuation。
7. [x] **B1 / R5**：Approval API contract 字段补齐。

第一批、阶段 B、C1 与 C2 已完成。下一步建议优先推进 **C3 / 单 daemon 项目所有权与 runtime lock**，先明确 runtime lock 存储、owner token redaction、serve startup ownership guard 和 stale owner 恢复语义。
