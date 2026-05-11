# M5 — Review packet and Human Review gate

## 目标

实现 after_run guarantee、temporary-index patch generation、review.json/md、Human Review transition。

## 输入文件

- `schemas/review_packet.schema.json`
- `TECH_SPEC.md §14`

## 必须创建/修改的组件

- `internal/review`
- `internal/gitx`
- `internal/orchestrator`

## 验收命令

```bash
go test ./internal/review ./internal/orchestrator
SYMPHONY_RUN_E2E=1 go test ./test/e2e/...
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
