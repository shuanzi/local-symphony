# M7 — Codex adapter

## 目标

在 committed fixtures 存在时实现真实 Codex app-server adapter 与 approval bridge。

## 输入文件

- `docs/codex/ADAPTER_MAPPING.md`
- `docs/codex/FIXTURE_POLICY.md`
- `TECH_SPEC.md §10`

## 必须创建/修改的组件

- `internal/agent/codex`
- `internal/agent/codex/testdata`

## 验收命令

```bash
go test ./internal/agent/codex
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
scripts/validate-contracts.sh
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
- [ ] 无 committed fixture 的 Codex protocol version 在启动真实 Codex process 前失败，并记录 `unsupported_codex_version`。
- [ ] 文档、CLI help、API schema 没有明显漂移。
