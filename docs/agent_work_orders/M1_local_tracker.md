# M1 — Local tracker and store

## 目标

实现 app/project SQLite 初始化、issue CRUD、comments、labels、blocker relation、state transition。

## 输入文件

- `db/schema/v1_app.sql`
- `db/schema/v1_project.sql`
- `TECH_SPEC.md §7-8`

## 必须创建/修改的组件

- `internal/store`
- `internal/tracker/local`
- `internal/core`

## 验收命令

```bash
go test ./internal/store ./internal/tracker/...
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
