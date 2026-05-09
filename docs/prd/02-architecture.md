# 系统架构与运行模型

> 产品背景说明：本文件仅保留产品上下文。若与 `docs/implementation/`、`docs/schema/`、`docs/config/` 或 `docs/api/` 冲突，后者为准；不要从本 PRD 复制实现级 enum、schema、命令策略或模板。


## 1. 总体架构

```text
┌──────────────────────────────────────────────┐
│              React Dashboard                 │
│  Overview / Board / Issue / Run / Review     │
└──────────────────────┬───────────────────────┘
                       │ REST + SSE
                       ▼
┌──────────────────────────────────────────────┐
│        Go backend: cmd/symphony serve          │
│                                              │
│  HTTP API / SSE                              │
│  Orchestrator Actor                          │
│  Local Tracker Service                       │
│  Workspace Manager                           │
│  Git Service                                 │
│  Agent Runner                                │
│  Codex App-Server Adapter                    │
│  Tool Gateway                                │
│  Review Packet Generator                     │
│  Diagnostics                                 │
└──────────────┬───────────────┬───────────────┘
               │               │
               ▼               ▼
     ┌────────────────┐   ┌────────────────────┐
     │ SQLite DB      │   │ Filesystem          │
     │ project facts  │   │ workspaces/logs     │
     └────────────────┘   └────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────┐
│          Codex app-server subprocess          │
│          cwd = issue workspace                │
└──────────────────────────────────────────────┘
```

## 2. 运行形态

v1：

```text
Go daemon + localhost dashboard + CLI
```

v2：

```text
Tauri desktop shell + Go sidecar + 同一套 React UI
```

v1 不做 Electron / Tauri 壳，但所有边界按未来桌面化设计：

```text
UI 只通过 REST/SSE 访问 daemon
CLI 直接连接 daemon
agent tool 通过 scoped token 连接 daemon
桌面壳未来只负责启动 sidecar 和系统集成
```

## 3. 技术栈

| 层 | 决策 |
|---|---|
| 后端 | Go |
| 前端 | React + TypeScript |
| UI 形态 | v1 localhost dashboard；v2 Tauri + Go sidecar |
| 数据库 | SQLite |
| Agent runtime | Codex app-server |
| Codex transport | selected Codex app-server stdio target; protocol isolated in adapter |
| Realtime | SSE |
| CLI | `symphony` |
| 本地 tool | `symphony tool` CLI + local IPC |
| Workspace | Git worktree |
| Review artifact | Markdown + JSON + patch + logs |

## 4. Orchestrator Actor

Orchestrator 是唯一调度状态权威。

它负责：

```text
poll tick
workflow reload effect
candidate issue selection
blocker filtering
dispatch
claim / release
run cancellation
manual failure pause/resume; no automatic retry timers in v1
handoff finalizer 状态衔接
事件写入
```

它不负责：

```text
直接修改代码
直接执行 agent 业务逻辑
直接决定 Done
直接创建 PR
替代 UI/CLI 做人工 review
```

## 5. 进程模型

`cmd/symphony` 是单 binary。`symphony serve` 启动 daemon/server mode。

```text
symphony serve
├── embedded HTTP server
├── orchestrator loop
├── SQLite connection pool
├── file watcher
├── agent runner #1
│   └── codex app-server subprocess
├── agent runner #2
│   └── codex app-server subprocess
└── agent runner #N
    └── codex app-server subprocess
```

默认：

```text
每个 run 一个 Codex app-server 子进程
每个 run 一个 live thread
run 结束后关闭子进程
```

## 6. 存储布局

```text
~/.symphony/
├── app.db
├── logs/
├── workspaces/
│   └── <project_id>/
│       └── LOC-123/
└── cache/

<repo>/
├── WORKFLOW.md
├── .symphony/
│   ├── symphony.db
│   ├── artifacts/
│   │   └── LOC-123/
│   │       └── run_<run_id>/
│   ├── exports/
│   ├── logs/
│   └── tmp/
└── source code
```

## 7. 数据库分层

| DB | 路径 | 用途 |
|---|---|---|
| Global App DB | `~/.symphony/app.db` | registered projects、app settings、recent projects、session metadata |
| Project DB | `<repo>/.symphony/symphony.db` | issues、runs、events、review packets、workflow snapshots |

v1 不做完整 migration / rollback，但需要最小 `schema_version`，用于判断 DB 是否兼容。

## 8. UI 边界

UI 是 control surface，不是 correctness layer。

UI 不做：

```text
直接读写 SQLite
直接执行 git
直接控制 Codex subprocess
直接写 workspace 文件
直接计算 dispatch eligibility
```

UI 只做：

```text
展示
过滤
搜索
审批
触发 command
打开 artifact
辅助 review
```
