# 仓库指南

## 项目结构与模块组织

Local Symphony 是一个本地优先的 agent 工作流控制台。Go 源码位于 `cmd/symphony/` 和 `internal/`：前者是 CLI/daemon 入口，后者包含 `app`、`cli`、`store`、`orchestrator`、`workspace`、`toolgateway`、`review`、`httpapi` 以及 agent adapter 等包。合同与数据定义放在 `api/openapi.yaml`、`schemas/` 和 `db/schema/`。项目文档和 work order 位于 `docs/`，可运行脚本位于 `scripts/`，示例位于 `examples/`，dashboard skeleton 位于 `web/`。聚焦的 Python 测试位于 `tests/`。

## 构建、测试与开发命令

- `go build -o ./bin/symphony ./cmd/symphony`：构建本地 CLI/daemon 二进制。
- `go run ./cmd/symphony --help`：不生成二进制，直接运行 CLI。
- `go test ./...`：编译并测试全部 Go package。
- `python3 scripts/validate_contracts.py` 或 `bash scripts/validate-contracts.sh`：校验 OpenAPI、JSON Schema、SQLite schema、示例和 manifest 合同。
- `bash scripts/acceptance-local.sh`：构建二进制，并在临时 Git 仓库中运行从 init 到 review 的本地验收流程。
- `cd web && npm run typecheck && npm test`：校验 dashboard action inventory。

## 编码风格与命名约定

Go 变更使用 `gofmt`，package 保持小型、全小写，并按领域命名。agent、安全和合同相关路径应优先使用显式错误和 fail-closed 行为。`web/src/` 中的 TypeScript 文件保持简洁，变量和 action 名称使用 camelCase。没有明确的仓库级理由时，不要引入新依赖。

## 测试指南

先运行最窄范围的相关检查；如果修改共享合同或工作流行为，再扩大验证范围。合同变更应运行 `python3 scripts/validate_contracts.py`；Go package 变更应运行 `go test ./...`；CLI 工作流变更应运行 `bash scripts/acceptance-local.sh`。Python 测试使用描述性的 `test_...` 命名，新聚焦测试放在 `tests/`。

## Commit 与 Pull Request 规范

近期 commit 使用简短的祈使句摘要，有时带 `docs:` 或 `test:` 等作用域前缀。保持 commit 聚焦且便于审查。Pull request 应说明变更的工作流或合同，列出已运行的验证命令，关联相关 issue；仅在 dashboard UI 行为变化时附截图。

## 安全与配置提示

v1 项目明确禁止自动 push、创建 PR、merge、workspace 清理、原始 secret 导出和任意状态修改。保留本地服务的 loopback 默认绑定；不要通过 API、dashboard 或 diagnostics 记录 raw prompt 或 Codex log；Codex adapter 的 fixture-gated 行为应保持 fail-closed。
