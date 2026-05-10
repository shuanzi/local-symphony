# Workspace 与 Git 策略

> **Implementation warning:** This PRD file is product background only. Do not implement API, DB schema, CLI, state-machine, security, or test contracts from this file. Start from `../AGENT_IMPLEMENTATION_GUIDE.md`; executable contracts are `../../api/openapi.yaml` and `../../db/schema/*.sql`.


> 产品背景说明：本文件仅保留产品上下文。若与 `docs/implementation/`、`docs/schema/`、`docs/config/` 或 `docs/api/` 冲突，后者为准；不要从本 PRD 复制实现级 enum、schema、命令策略或模板。


## 1. v1 项目单位

v1 project = 一个本地 Git repo。

不做：

```text
多 repo issue
纯目录项目
remote-only 项目
非 Git workspace
```

## 2. Workspace 技术

每个 issue 使用独立 `git worktree`。

默认：

```text
主 repo：用户项目 repo
issue workspace：~/.symphony/workspaces/<project_id>/<issue_identifier>/
```

## 3. Branch 策略

每个 issue 一个稳定 branch。

格式：

```text
symphony/<issue_identifier>-<title_slug>-<short_hash>
```

示例：

```text
symphony/LOC-123-add-local-tracker-7f3a9c
```

规则：

```text
issue_identifier 保留大小写，例如 LOC-123
title_slug 小写，最长 40 字符
short_hash 来自 issue.id，避免重名
最终 branch name 最长 96 字符
非法字符替换为 -
连续 - 合并
首尾 - 去除
```

## 4. Base ref 选择

默认规则：

```text
1. WORKFLOW.md 中显式配置 git.base_ref 时，必须能被 Git 解析。
2. 未显式配置时，git.base_ref = auto。
3. auto resolution order: origin/main → origin/master → main → master → HEAD。
```

创建 workspace 时记录：

```text
base_ref
base_sha
branch_name
workspace_path
```

## 5. Workspace 生命周期

第一次 run：

```text
create worktree
create branch
record base_sha
run hooks.after_create
run hooks.before_run
launch Codex
```

后续 operator re-dispatch / Rework：

```text
reuse same worktree
reuse same branch
do not reset by default
run hooks.before_run
launch Codex

# v1 does not have automatic retry timers; resume is operator-driven
```

## 6. Destructive action

v1 默认不做自动 destructive reset。

禁止自动执行：

```text
git reset --hard
git clean -xfd
auto rebase dirty workspace
auto delete workspace
```

v1 也暂不实现 workspace delete API。后续实现前，需要接入备份 / snapshot / audit 能力。

## 7. Agent Git 权限

agent 可以：

```text
编辑 workspace 内文件
运行测试
查看 git diff/status/log
生成说明
通过 local tool handoff
```

agent 默认不可以：

```text
git push
创建 PR
删除 branch
force push
修改主 repo working tree
修改 workspace.root 外的文件
```

## 8. Commit / publish 策略

v1 默认：

```yaml
git:
  agent_commit: manual
  auto_push: false
  auto_rebase: false
```

含义：

```text
agent 主要产出 working tree changes
系统生成 review packet
人审后用户自行 commit / publish
```

v1 不做自动 PR / merge。

## 9. Review Packet 文件

每次 run 结束生成：

```text
<repo>/.symphony/artifacts/<issue_identifier>/run_<run_id>/
├── review.md
├── review.json
├── changes.patch
├── changed-files.txt
├── untracked-files.json
├── test-output.txt
├── agent-final-message.md
├── commands.jsonl
├── tool-calls.jsonl
├── approvals.jsonl
├── codex-events.redacted.jsonl
└── prompt/
    ├── context.json
    ├── rendered_prompt.redacted.md
    └── prompt_meta.json
```

## 10. Cleanup 策略

v1 不自动清理 Human Review 前后的 workspace。这是相对 upstream SPEC terminal cleanup 的 intentional deviation，避免在没有 snapshot/backup/audit 前删除本地工作区。

建议策略：

| Issue 状态 | Workspace 策略 |
|---|---|
| Ready | 保留 |
| Working | 保留 |
| Rework | 保留 |
| Blocked | 保留 |
| Human Review | 保留 |
| Done | v1 保留；后续可做 retention cleanup |
| Cancelled | v1 保留；后续可做 cleanup |
| Duplicate | v1 保留；后续可做 cleanup |
