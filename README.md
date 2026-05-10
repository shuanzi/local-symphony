# Local Symphony App v1

**状态**：v1 合并版文档入口  
**更新日期**：2026-05-10  
**文档用途**：作为 agent、开发者和 reviewer 使用本方案文档的入口说明。  
**权威文档**：本包只保留一套产品方案文档和一套技术方案文档。

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

当前合并版文档只包含以下核心文件：

```text
README.md       # 文档入口和使用说明
PRD.md          # 唯一产品方案文档
TECH_SPEC.md    # 唯一技术方案文档
```

### 2.1 PRD.md

`PRD.md` 是 Local Symphony App v1 的唯一产品需求文档，定义：

```text
产品定位
目标用户
核心场景
v1 目标和非目标
端到端主流程
产品模块范围
issue 状态机的业务语义
Human Review / Rework / Done 规则
Dashboard / CLI / API 的产品要求
安全与运维的产品原则
v1 验收标准
后续路线
```

### 2.2 TECH_SPEC.md

`TECH_SPEC.md` 是 Local Symphony App v1 的唯一技术规格文档，定义：

```text
实现边界
架构和进程模型
Go / frontend / package layout
核心模块职责
SQLite 数据模型
REST API 合同
SSE 合同
CLI 合同
Tool Gateway 合同
WORKFLOW.md 合同
Codex adapter 合同
orchestrator run lifecycle
workspace / git 生命周期
review packet 和 rework 语义
安全模型
错误码和 failure_code
测试策略
验收测试
Definition of Done
M0-M8 实施阶段
```

原始文档包中的 `api/openapi.yaml`、`db/schema/*.sql`、`docs/implementation/*`、`docs/schema/*`、`docs/config/*`、`docs/security/*`、`docs/agent/*`、`docs/backlog/*` 等内容已经被合并、去重并吸收到 `TECH_SPEC.md`。它们不再作为独立 source of truth 使用。

---

## 3. 文档权威关系

使用本项目文档时，按以下规则处理：

```text
1. 产品目标、用户场景、范围和业务语义以 PRD.md 为准。
2. API、DB、CLI、状态机、模块职责、安全、测试和发布合同以 TECH_SPEC.md 为准。
3. README.md 只作为入口说明，不覆盖 PRD.md 或 TECH_SPEC.md。
4. 不再使用原始多文档 source-of-truth 层级。
5. 不根据 upstream Symphony SPEC 或旧文档自行恢复 Linear、PR 自动化或其他 v1 非目标能力。
```

当 `PRD.md` 和 `TECH_SPEC.md` 出现表面冲突时：

```text
产品意图以 PRD.md 为准。
实现合同以 TECH_SPEC.md 为准。
不能确定时，优先保持 Local v1 的本地、安全、可复核、人工决策边界。
```

---

## 4. 与 upstream Symphony SPEC 的关系

Local Symphony App v1 受 OpenAI Symphony SPEC 启发，但不是 upstream SPEC 的逐字实现，也不是简单的 “upstream Symphony 去掉 Linear”。

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
启动 Codex app-server
  ↓
Codex 在 issue workspace 内工作
  ↓
Codex 调用 symphony tool handoff
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

实现本项目时，建议按以下顺序读取：

```text
1. README.md
2. PRD.md
3. TECH_SPEC.md
```

实际开发时，以 `TECH_SPEC.md` 为实现主文档。`PRD.md` 用于理解产品意图、用户场景、范围和验收口径。

建议执行方式：

```text
1. 先建立 repo / package / app shell。
2. 实现 SQLite app DB 和 project DB。
3. 实现 local tracker、issue state machine 和 dispatch pause/resume。
4. 实现 WORKFLOW.md parser、prompt renderer 和 fake runner。
5. 实现 orchestrator、workspace manager 和 git worktree flow。
6. 实现 Tool Gateway、handoff、after_run 和 review packet finalizer。
7. 实现 REST API、SSE、CLI 和 dashboard。
8. 实现 security policy、diagnostics、tests 和 release packaging。
9. 按 TECH_SPEC.md 的 M0-M8 和 Definition of Done 做验收。
```

不要从旧的 upstream 行为或原始多文档结构中补回未声明能力。

---

## 9. 验收口径摘要

v1 完成时必须至少满足：

```text
可以初始化本地项目。
可以创建、编辑、评论、转态、阻塞、dispatch 本地 issue。
Ready issue 可以被 orchestrator 或手动 dispatch。
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

完整验收标准以 `TECH_SPEC.md` 的测试、验收和 Definition of Done 章节为准。

---

## 10. 变更原则

后续修改文档时，遵守以下原则：

```text
产品范围变化先改 PRD.md。
技术合同变化先改 TECH_SPEC.md。
README.md 只同步入口说明和高层摘要。
不要重新引入多文档 source-of-truth 层级。
不要让 README.md 成为第三份权威文档。
不要新增与 v1 非目标冲突的实现要求，除非同时明确版本升级。
```

