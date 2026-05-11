# Local Symphony App v1

**状态**：v1 agent-executable 文档包  
**更新日期**：2026-05-11  
**文档用途**：作为 implementation agent、review agent、test agent 和人工 reviewer 使用的完整产品/技术/验收输入。  
**权威文档**：`PRD.md` 与 `TECH_SPEC.md` 是解释性权威；`api/`、`db/`、`schemas/`、`examples/`、`docs/agent_work_orders/` 是可执行合同与验收材料。

---

## 1. 项目摘要

Local Symphony App v1 是一个 **local-first agent engineering workflow control plane**。它在本地 Git 仓库中运行，把本地工程任务转化为可以由 Codex 执行、由 operator 观察、审批、复核、返工和归档的完整工程流程。

v1 的目标不是最大化自动化，而是建立一条可靠、可观察、可审查、可恢复的本地 agent 工程工作流。

核心形态：

```text
Go daemon
+ React/TypeScript dashboard
+ SQLite local tracker
+ git worktree workspace manager
+ Codex app-server runner
+ CLI/IPC tool gateway
+ two-stage handoff submit/finalize
+ review packet generator
+ REST/SSE API
+ balanced-secure local security baseline
```

---

## 2. 文档结构

```text
README.md                       # 文档入口和使用说明
PRD.md                          # 产品目标、范围、用户流程和验收口径
TECH_SPEC.md                    # 技术架构、状态机、模块职责、安全、测试和发布合同
api/openapi.yaml                # REST/SSE API 可机器校验合同
db/schema/v1_app.sql            # app-level SQLite DDL
db/schema/v1_project.sql        # project-level SQLite DDL
schemas/*.schema.json           # WORKFLOW、RunEvent、Tool Gateway、Review Packet、Diagnostics、FailureCode JSON Schema
schemas/tools/*.input.schema.json # Tool Gateway 各工具输入 schema
examples/WORKFLOW.default.md    # 默认 WORKFLOW.md 模板
examples/handoff.json           # agent handoff 输入示例
examples/followup.json          # agent followup 输入示例
docs/agent_work_orders/*.md     # M0-M8 可执行开发任务包
docs/testing/ACCEPTANCE.md      # 端到端验收场景和命令
docs/testing/FAILURE_CODE_MATRIX.md
docs/codex/ADAPTER_MAPPING.md   # Codex app-server → Symphony 映射
docs/codex/FIXTURE_POLICY.md    # Codex fixture-gated 策略
```

### 2.1 `PRD.md`

`PRD.md` 定义产品定位、目标用户、核心场景、v1 目标/非目标、端到端主流程、状态机业务语义、Human Review / Rework / Done 规则、Dashboard / CLI / API 产品要求、安全与运维产品原则、成功指标和验收标准。

### 2.2 `TECH_SPEC.md`

`TECH_SPEC.md` 定义实现边界、架构和进程模型、Go/frontend/package layout、SQLite 数据模型、REST/SSE、CLI、Tool Gateway、WORKFLOW.md、Codex adapter、orchestrator lifecycle、workspace/git、review packet、安全模型、failure_code、测试策略、M0-M8 实施阶段和 Definition of Done。

### 2.3 Contract artifacts

以下文件是 implementation agent 必须消费的可执行合同：

```text
api/openapi.yaml
schemas/*.schema.json
schemas/tools/*.input.schema.json
db/schema/*.sql
docs/testing/*.md
docs/agent_work_orders/*.md
```

它们不是新的产品 source of truth，而是 `TECH_SPEC.md` 的机器可读落地形态。实现时不得只读 PRD/TECH_SPEC 后自行发明 API、DB 或 JSON shape。


### 2.4 Historical patch files

如文档包根目录中存在 `local-symphony-*.patch` 历史文件，它们只作为修订记录保存，不属于 v1 可执行合同；implementation agent 不得自动应用这些 patch，也不得以其中旧内容覆盖当前 PRD、Tech SPEC、OpenAPI、SQL 或 JSON Schema。

---

## 3. 文档权威关系

使用本项目文档时，按以下规则处理：

```text
1. 产品目标、用户场景、范围和业务语义以 PRD.md 为准。
2. API、DB、CLI、状态机、模块职责、安全、测试和发布合同以 TECH_SPEC.md 为准。
3. api/openapi.yaml、db/schema/*.sql、schemas/*.json 是 TECH_SPEC.md 的可执行合同。
4. docs/agent_work_orders/*.md 是开发 agent 的 milestone 任务拆解。
5. docs/testing/*.md 是 test/review agent 的验收输入。
6. README.md 只作为入口说明，不覆盖 PRD.md 或 TECH_SPEC.md。
7. 不根据 upstream Symphony SPEC 或旧文档自行恢复 Linear、PR 自动化或其他 v1 非目标能力。
```

当文件之间出现冲突时：

```text
产品意图冲突：以 PRD.md 为准。
实现合同冲突：以 TECH_SPEC.md 为准，并同步修正 api/db/schemas。
机器合同与 TECH_SPEC.md 冲突：先修文档，再改合同或实现。
不能确定时：优先保持 Local v1 的本地、安全、可复核、人工决策边界。
```

---

## 4. 与 upstream Symphony SPEC 的关系

Local Symphony App v1 受 OpenAI Symphony SPEC 启发，但不是 upstream SPEC 的逐字实现，也不是简单的“upstream Symphony 去掉 Linear”。

v1 继承的核心理念包括：

```text
daemon-style orchestrator
repo-owned workflow config
per-issue isolated workspace
bounded concurrency
agent runner abstraction
status / logging / observability surface
active run reconciliation
```

v1 的本地化决策包括：

```text
tracker.kind = local only
SQLite project DB is local tracker source of truth
no Linear dependency
one issue → one git worktree
localhost dashboard/API
CLI/IPC Tool Gateway
two-stage handoff
review packet finalizer
manual failure pause/resume
```

---

## 5. Frozen v1 main path

v1 的主流程固定如下：

```text
symphony init
  ↓
创建本地 issue
  ↓
Issue → Ready
  ↓
手动或 orchestrator dispatch
  ↓
创建 / 复用 git worktree + branch
  ↓
启动 Codex app-server 或 fake runner
  ↓
agent 在 issue workspace 内工作
  ↓
agent 调用 symphony tool handoff submit --json ./handoff.json
  ↓
执行 after_run hook
  ↓
生成 review packet
  ↓
Issue → Human Review
  ↓
人工复核
  ├── Send to Rework
  └── Mark Done
```

关键规则：

```text
handoff.submit 不直接把 issue 变成 Human Review。
review packet finalizer 成功后，issue 才能进入 Human Review。
Done 只能由 operator 触发。
Rework 必须复用同一个 workspace / branch。
Done 不触发 commit、push、merge、PR、cleanup 或 workspace 删除。
失败后 issue 回到 dispatch 前来源状态 Ready/Rework，并设置 dispatch_paused=true。
dispatch-resume 只清除 pause，不自动删除 blocker，不自动修改 issue 内容。
```

---

## 6. v1 核心约束

实现 agent 必须遵守以下约束：

```text
tracker.kind = local only
SQLite project DB 是 issue / run / review 的本地 source of truth
Linear adapter、Linear credential、Linear API 调用都禁止实现
每个 issue 一个 git worktree
run 必须有 active run reconciliation
run failure 默认暂停该 issue 的 dispatch
operator cancel / approval cancel_run / agent issue.block 都会暂停 dispatch
v1 没有自动 retry queue 或 retry timers
v1 不自动删除、reset、clean、rebase workspace
v1 不自动 push、create PR、merge、publish
v1 不允许 agent 自动 commit
Human Review 是 v1 唯一 handoff target
real Codex tests 是 opt-in；默认 CI 使用 fake runner
Codex adapter 必须 fixture-gated；无 fixture 的 Codex protocol version dispatch 前失败
```

---

## 7. v1 非目标

v1 明确不实现：

```text
Linear tracker dependency or adapter
Tauri desktop shell
remote dashboard
multi-tenant RBAC
automatic PR creation
git push / merge / publish automation
agent automatic commit
automatic SQLite backup
database migration / rollback framework
automatic retry queue or timers
crash recovery beyond startup stale-run interruption
full compliance-grade audit log
supply-chain deep risk scoring
dynamic tools / MCP
automatic workspace cleanup/delete/reset/rebase
raw prompt or raw Codex log export through v1 API
```

---

## 8. 给 implementation agent 的使用方式

推荐读取顺序：

```text
1. README.md
2. PRD.md
3. TECH_SPEC.md
4. api/openapi.yaml
5. db/schema/v1_app.sql + db/schema/v1_project.sql
6. schemas/*.schema.json
7. docs/agent_work_orders/README.md
8. docs/testing/ACCEPTANCE.md
9. docs/codex/*.md
```

推荐执行顺序：

```text
M0  Contracts and scaffold
M1  Local tracker and store
M2  Workflow, prompt, workspace and git
M3  Orchestrator and fake runner
M4  Tool Gateway and handoff
M5  Review packet and Human Review gate
M6  API, CLI, dashboard, auth and security
M7  Codex adapter
M8  Release hardening
```

每个 milestone 必须满足对应 `docs/agent_work_orders/M*.md` 的验收命令后再进入下一阶段。

---

## 9. Agent 禁止事项

Implementation agent MUST NOT：

```text
实现 Linear、GitHub Issues、PR 创建、push、merge、publish。
实现自动 retry timer、retry queue 或后台重试策略。
实现 workspace cleanup/delete/reset/rebase。
暴露 raw prompt 或 raw Codex logs 到 API/dashboard/diagnostics export。
新增 dynamic tools、MCP、remote dashboard、多租户 RBAC。
改变 issue 状态机而不同时更新 PRD.md、TECH_SPEC.md、api/db/schemas 和测试。
发明未写入 api/openapi.yaml、db/schema/*.sql 或 schemas/*.json 的 API/DB/JSON shape。
在 M0-M8 之外扩展 v1 非目标能力。
```

---

## 10. 验收口径摘要

v1 完成时必须至少满足：

```text
可以初始化本地项目。
可以创建、编辑、评论、状态流转、阻塞、dispatch 本地 issue。
Ready/Rework issue 可以被 orchestrator 或手动 dispatch。
失败后 issue 回到 dispatch 前来源状态并 pause，resume 后可以重新调度。
每个 issue 在独立 git worktree 和 branch 内运行。
fake runner E2E 默认可通过。
Codex app-server integration 有 fixture/version gate。
agent 可以通过固定 Tool Gateway registry 提交 artifact、follow-up、block 和 handoff。
handoff 后必须执行 after_run 并生成 review packet。
review packet 成功后 issue 才进入 Human Review。
operator 可以 Send to Rework 或 Mark Done。
失败、取消、block、missing handoff 等情况会暂停 dispatch 并保留诊断信息。
Dashboard、CLI、REST、SSE 能反映 issue、run、approval、review packet 和 diagnostics 状态。
安全模型覆盖 loopback API、browser session、CSRF、CLI bearer、run-scoped tool token、protected path、redaction、artifact containment、command/network policy。
默认测试不依赖真实 Codex；真实 Codex 测试只在显式 opt-in 时运行。
```

完整验收标准以 `docs/testing/ACCEPTANCE.md`、`TECH_SPEC.md` 的测试章节和 Definition of Done 为准。

---

## 11. 变更原则

后续修改文档时，遵守以下原则：

```text
产品范围变化先改 PRD.md。
技术合同变化先改 TECH_SPEC.md。
API 变化必须同步 api/openapi.yaml。
DB 变化必须同步 db/schema/*.sql。
JSON shape 变化必须同步 schemas/*.schema.json 与 schemas/tools/*.input.schema.json。
验收变化必须同步 docs/testing/ACCEPTANCE.md 与相关 work order。
README.md 只同步入口说明和高层摘要。
不要新增与 v1 非目标冲突的实现要求，除非同时明确版本升级。
```
