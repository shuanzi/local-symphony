# WORKFLOW.md v1 模板

> 产品背景说明：本文件仅保留产品上下文。若与 `docs/implementation/`、`docs/schema/`、`docs/config/` 或 `docs/api/` 冲突，后者为准；不要从本 PRD 复制实现级 enum、schema、命令策略或模板。


## 状态

产品背景文档。默认模板的唯一实现源是：

```text
docs/config/starter-WORKFLOW.md
```

配置字段、默认值、校验规则和插值规则的权威说明是：

```text
docs/config/workflow-reference-v1.md
docs/implementation/IS-005-workflow-prompt.md
```

## 关键规则

```text
YAML front matter 是配置。
Markdown body 是 agent prompt。
配置字段不支持 {{ ... }} Liquid 插值。
只有 prompt body 支持 Liquid-style variables。
workspace.root 未显式配置时，默认由实现解析为全局 workspace root 下的 <project_id> 目录。
git.base_ref 默认是 auto。
```

不要在配置 front matter 中使用 Liquid 变量拼接 workspace root。类似“workspace root 加 project id 变量”的写法是无效配置，因为 config 不支持 Liquid 插值。

## 默认模板摘要

默认模板采用：

```yaml
tracker:
  kind: local

git:
  enabled: true
  mode: worktree
  base_ref: auto

agent:
  max_turns_per_run: 2
  max_handoff_continuations: 1
  handoff_required: true
  handoff_state: Human Review
  pause_on_missing_handoff: true

tools:
  gateway: cli
  require_handoff_tool: true
  allow_dynamic_tools: false
  allow_mcp: false
  agent_can_set_terminal_state: false
```

完整模板必须从 `docs/config/starter-WORKFLOW.md` 复制或由 `symphony init` 写入，本文档不再维护重复模板内容。

## Handoff 提示要求

默认 prompt 必须告诉 agent：

```text
1. 只在当前 workspace 工作。
2. 不 push branch。
3. 不创建 PR。
4. 不标记 issue Done。
5. 完成后写 handoff.json。
6. 运行 symphony tool handoff --json ./handoff.json。
7. handoff tool 只提交 handoff 数据；Human Review 由 review-packet finalizer 成功后转换。
```
