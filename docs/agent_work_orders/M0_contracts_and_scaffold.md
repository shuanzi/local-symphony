# M0 — Contracts and scaffold

## 目标

建立 Go module、web skeleton、API/DB/schema 校验入口、fake runner 基础目录。

## 输入文件

- `README.md`
- `PRD.md`
- `TECH_SPEC.md`
- `api/openapi.yaml`
- `db/schema/*.sql`
- `schemas/*.schema.json`
- `schemas/tools/*.input.schema.json`

## 必须创建/修改的组件

- `cmd/symphony`
- `internal/core`
- `internal/app`
- `internal/db`
- `scripts/validate-contracts.sh`

## 验收命令

```bash
go test ./...
python3 scripts/validate_contracts.py
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
