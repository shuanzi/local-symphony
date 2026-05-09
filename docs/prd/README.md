# Local Symphony App v1 文档套件

版本：v1 最终冻结版  
冻结日期：2026-05-08  
语言：中文  
状态：已冻结，可进入 Implementation Spec / Engineering Backlog 阶段

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
| `08-data-model.md` | v1 数据模型草案 |
| `09-api-cli-spec.md` | v1 REST API 与 CLI 规格 |
| `10-workflow-template.md` | `WORKFLOW.md` v1 模板 |
| `11-mvp-roadmap.md` | M0–M8 MVP 里程碑与验收标准 |
| `12-adr.md` | 架构决策记录 ADR |
| `13-implementation-entrypoint.md` | 后续实施阶段建议入口 |
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
+ atomic handoff
+ review packet
+ REST/SSE API
+ balanced-secure local security baseline
```

v1 的目标不是最大自动化，而是先建立一条可靠、可观察、可审查的本地 agent 工程工作流。
