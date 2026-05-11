# M2 — Workflow, prompt, workspace and git

## 目标

实现 WORKFLOW strict parser、EffectiveConfig、prompt rendering、workspace/worktree 准备、branch naming。

## 输入文件

- `examples/WORKFLOW.default.md`
- `schemas/workflow_config.schema.json`
- `TECH_SPEC.md §6 §9`

## 必须创建/修改的组件

- `internal/config`
- `internal/workspace`
- `internal/gitx`

## 验收命令

```bash
go test ./internal/config ./internal/workspace ./internal/gitx
```

## 禁止事项

```text
不得实现 Linear / PR / push / merge / auto retry / workspace cleanup / dynamic tools / MCP。
不得绕过 api/openapi.yaml、db/schema/*.sql、schemas/*.json 自行发明合同。
```

## 完成检查清单

- [ ] 相关合同文件已被测试消费。
- [ ] 关键状态/错误路径有单元或集成测试。
- [ ] 默认路径不依赖真实 Codex。
- [ ] 文档、CLI help、API schema 没有明显漂移。
