# v1 真实产品化执行计划

**状态**：执行计划  
**更新日期**：2026-05-25  
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

1. 扩展 approval response DTO，不暴露 opaque raw request。
2. DB 现有 `request_json` 保持内部记录，API projection 输出结构化字段。
3. 更新 OpenAPI 和 dashboard client/type tests。
4. 增加 pending/resolved/expired approval API tests。

验收：

- Dashboard Approval Inbox 不解析 opaque `request_json`。
- API 字段与 TECH 对齐。

### B2. Codex approval producer 与 writeback

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

1. Codex adapter 将 approval request normalizes 成 store row。
2. orchestrator 等待 approval resolution 或 timeout。
3. 实现 deny 只拒绝当前动作，不取消 run。
4. 实现 cancel_run 立即终止 run，失败码 `operator_cancelled`，并 pause dispatch。
5. 实现 approval timeout 后不可再决策。
6. 增加 Codex transcript fixture 测试覆盖 writeback。

验收：

- approval timeout -> `approval_timeout`。
- deny 不直接设置 `operator_cancelled`。
- cancel_run 使用 `operator_cancelled`，走终止 pause 路径。

### B3. 安全策略执行与回归套件

覆盖 R12。

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

1. 建立 hook executor 统一输出截断和 redaction。
2. workspace 创建成功后运行 after_create。
3. runner 启动前运行 before_run。
4. terminal outcome 后运行 after_run。
5. review packet 和 diagnostics 纳入 hook summary。

验收：

- `after_create_failed`、`before_run_failed` 不启动 runner。
- hook 输出受 `hooks.max_output_bytes` 限制。

### C2. Scheduler tick loop 与 dispatch preflight 统一

覆盖 R8 的 scheduler 部分。

目标：

- `symphony serve` 根据 workflow polling interval 启动 tick loop。
- tick 只选择 `Ready` 和 `Rework`。
- tick 和手动 dispatch 共享同一 preflight。
- failure 后 pause 的 issue 不会自动 redispatch。

主要改动面：

- `internal/app`
- `internal/orchestrator`
- `internal/store`
- `internal/config`

任务拆分：

1. 在 serve runtime 启动 tick loop，支持 graceful shutdown。
2. 统一 manual dispatch 和 tick dispatch 的 eligibility/preflight。
3. 增加 polling interval config tests。
4. 增加 paused issue 不 redispatch 的 regression tests。

验收：

- `Working` 不作为正常 tick 候选。
- stale active run reconciliation 后仍遵守 dispatch pause。

### C3. 单 daemon 项目所有权与 runtime lock

覆盖 R8 的 ownership 部分。

目标：

- 每个 project DB 同时只有一个活跃 daemon owner。
- stale descriptor/lock owner 可安全恢复。
- runtime descriptor 不包含 secret/token。

主要改动面：

- `internal/app`
- `internal/store`
- `internal/db`
- `internal/httpapi`
- `internal/observability`

任务拆分：

1. 明确 runtime lock 存储位置和 owner token 不出 diagnostics。
2. serve startup 获取 lock；失败返回明确错误。
3. daemon shutdown 释放 lock。
4. stale owner 检测与安全恢复。
5. diagnostics 展示 non-secret runtime descriptor。

验收：

- 同 project 第二个 daemon 无法同时写入。
- crash/stale 场景可恢复。

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

1. **A0 / R17**：DB schema version guard 与 diagnostics 真实版本读取。
2. **A1 / R2**：Codex fixture metadata 和 `codex --version` gate。
3. **A2 / R1**：Runner interface 与 fake runner 迁移。
4. **A3 / R3**：Codex process launcher + startup timeout + redacted stderr。
5. **A4 / R4**：最小 normalized timeline。
6. **A5 / R6**：missing handoff continuation。
7. **B1 / R5**：Approval API contract 字段补齐。

第一批完成后再决定是否先深化 Codex approval writeback，或转向 daemon ownership/tick loop。判断标准是：真实 Codex 最小闭环是否已经能稳定通过 opt-in integration，且 fake acceptance 没有回退。
