# v1 真实产品化阶段 B policy-execution slice 验收记录

**日期**：2026-06-01  
**对应计划**：`docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` 阶段 B  
**阶段目标**：验证 Approval 与安全策略的 policy-execution slice，让 Codex command/file/network/protected-path 请求进入 operator approval 或本地 policy auto-deny，并保持默认验证不依赖真实 Codex。阶段 B 仍需补齐 redaction golden fixture 后才能完全收口。

## 1. 验收结论

阶段 B 尚未完全完成；本记录只验收已完成的 B1、B2 与 B3 policy-execution slice：

- B1 / R5 API/model：Approval DTO、OpenAPI、JSON Schema、DB/fallback schema、store/httpapi projection 和 dashboard 类型已对齐，API/dashboard 不暴露内部 `request_json` / `decision_json`。
- B2 / R5 bridge：Codex command/file_change/network approval request 会写入 `approval_requests`，operator 决策能写回 Codex；`deny` 只拒绝当前 action，`cancel_run` 使用 `operator_cancelled`，approval timeout 使用 `approval_timeout`。
- B3 / R12 policy-execution slice：默认 command allow/review/deny、network default deny、protected-path deny 已接入 Codex approval bridge；auto-deny 写入 `approval_requests.status=auto_denied`，并返回 `command_denied`、`network_denied` 或 `protected_path_denied`。
- Tool Gateway `artifact.attach` protected-path 拒绝保持 daemon hard-deny：记录 failed tool_call、无 approval row，且该 tool error 本身不直接终止 run。

## 2. 验收命令

已执行并通过：

```bash
go test ./internal/security ./internal/httpapi ./internal/store ./internal/agent/codex ./internal/orchestrator ./internal/toolgateway
go test ./...
PYTHONPATH=/tmp/local-symphony-pydeps python3 scripts/validate_contracts.py
PATH=/tmp/codex-go/go/bin:$PATH GOTELEMETRY=off GOMODCACHE=/tmp/local-symphony-gomodcache GOCACHE=/tmp/local-symphony-gocache GOFLAGS=-modcacherw bash scripts/acceptance-local.sh
git diff --check
```

本机默认 `PATH` 中没有 Go；验收使用临时下载的 Go 1.23.0 工具链。Contract validation 使用临时 Python target 安装 `requirements-dev.txt`，未修改仓库依赖文件。

## 3. 本阶段新增测试覆盖

- `internal/security`：默认 command allow/review/deny、compound shell command 不自动 allow、protected-path override、`-flag=protected_path` 提取、network default deny/review/allowlist、configured protected path。
- `internal/agent/codex`：unknown network default auto-deny、protected file_change auto-deny、command deny auto-deny、command allow auto-accept without approval row、command review pending approval、network review policy path。
- `internal/store`：auto-denied approval row 的 structured fields、resolved timestamp、reason 和不可二次 decide。
- `internal/toolgateway`：protected artifact attach 失败记录 failed tool_call，不创建 approval row，不直接改变 run terminal status。
- `internal/config` / schemas / examples：`approvals.network.default`、allowlist 和 protected paths 的 workflow parsing/schema/default example。

## 4. 已知边界

- v1 policy evaluator 是 pattern/prefix 分类与浅层 path 提取，不是完整 shell parser、DLP、filesystem sandbox 或 OS-level firewall。
- Network deny 是 Codex-mediated policy decision，不声明 packet-level egress isolation。
- Redaction golden fixture topic 已在 contract manifest 保留，但 fixture 本身仍是阶段 B 待补工作；在补齐前不声明 B3 或阶段 B 完全完成。本阶段继续依赖现有 redacted event、review/artifact refusal、diagnostics redacted-only 合同，后续 D 阶段会补更完整 diagnostics/UX 呈现。

## 5. 后续入口

先补齐 B3 redaction golden fixture，再关闭阶段 B。阶段 C（hook lifecycle、serve tick loop、single daemon ownership/runtime lock，以及 operator CLI over REST）是阶段 B 收口后的下一阶段入口，不应在 redaction golden fixture 待补时无条件进入。
