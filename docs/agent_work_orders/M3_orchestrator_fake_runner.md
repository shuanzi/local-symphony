# M3 — Orchestrator and fake runner

## 目标

实现单 actor orchestrator、dispatch claim、source_issue_state、fake runner E2E。

## 输入文件

- `TECH_SPEC.md §8`
- `docs/testing/ACCEPTANCE.md`

## 必须创建/修改的组件

- `internal/orchestrator`
- `internal/agent/fake`

## 验收命令

```bash
go test ./internal/orchestrator ./internal/agent/fake
SYMPHONY_RUN_E2E=1 go test ./...
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
