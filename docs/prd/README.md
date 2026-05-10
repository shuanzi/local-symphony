# Local Symphony App v1 文档套件

版本：v1 最终冻结版
冻结日期：2026-05-09
语言：中文
状态：产品背景文档已冻结；实现以 `docs/AGENT_IMPLEMENTATION_GUIDE.md`、`api/openapi.yaml`、`db/schema/*.sql`、`docs/implementation/`、`docs/schema/`、`docs/config/`、`docs/security/`、`docs/agent/` 为准

## 权威性说明

本目录保留 PRD 和产品背景。**不要从本目录直接实现 API、DB schema、CLI、状态机、安全或测试合同。** 若本目录内容与高优先级文档冲突，以 `docs/AGENT_IMPLEMENTATION_GUIDE.md` 的 source-of-truth 顺序为准。若 upstream SPEC 与 Local v1 文档存在歧义，按 `docs/references/spec-conformance-matrix.md` 和 `docs/implementation/IS-013-local-vs-upstream-resolution.md` 处理。

## 文档目录

| 文件 | 内容 |
|---|---|
| `00-final-frozen-version.md` | 最终冻结版本总览 |
| `01-prd.md` | 产品需求文档 PRD |
| `02-architecture.md` | 系统架构与运行模型 |
| `03-local-tracker.md` | 本地 tracker、issue 状态机与调度规则 |
| `04-workspace-git.md` | Workspace 与 Git 策略 |
| `05-agent-prompt-tools.md` | Agent、Prompt、Tool Gateway 与 Handoff |
| `06-ui-api-observability.md` | UI、API、SSE、日志与可观测性 |
| `07-security-operations.md` | 安全、权限、Secrets、Sandbox 与 v1 后移项 |
| `08-data-model.md` | 数据模型概念说明；具体 schema 已由 `docs/implementation/IS-002-sqlite-schema-v1.md` 和 `docs/schema/project-schema-v1.md` 取代 |
| `09-api-cli-spec.md` | API/CLI 产品概览；具体 API 以根目录 `api/openapi.yaml` 为准，具体 CLI 以 `IS-004` 为准 |
| `10-workflow-template.md` | 默认模板说明；唯一模板源为 `docs/config/starter-WORKFLOW.md` |
| `11-mvp-roadmap.md` | M0–M8 MVP 里程碑与验收标准 |
| `12-adr.md` | 架构决策记录 ADR |
| `13-implementation-entrypoint.md` | 实施入口说明；已同步到 Implementation Spec 权威结构 |
| `99-references.md` | 参考资料 |

## 一句话定义

Local Symphony App v1 是一个本地运行的 agent engineering workflow control plane：

```text
Go daemon
+ React/TypeScript dashboard
+ SQLite local tracker
+ git worktree workspace
+ Codex app-server runner
+ CLI/IPC tool gateway
+ two-stage handoff finalization
+ review packet
+ REST/SSE API
+ balanced-secure local security baseline
```

v1 的目标不是最大自动化，而是先建立一条可靠、可观察、可审查的本地 agent 工程工作流。

补充权威 schema：`docs/schema/normalized-issue-v1.md` 定义 API、orchestrator、prompt 与 UI 共用的 normalized issue DTO。
