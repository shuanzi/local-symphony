# Agent Work Orders

本目录把 Local Symphony App v1 拆成 M0-M8 九个可验收任务包。implementation agent 必须按顺序执行，除非人工 reviewer 明确调整。

每个 work order 包含：

```text
目标
输入文件
必须创建/修改的组件
验收命令
禁止事项
完成检查清单
```

通用规则：

```text
不得实现 Linear、PR、push、merge、workspace cleanup、auto retry、dynamic tools、MCP。
不得发明未写入 OpenAPI/SQL/JSON Schema 的 API 或持久化 shape。
每个 milestone 结束时必须更新测试并运行对应验收命令。
```
