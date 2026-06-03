# v1 真实产品化阶段 B 验收记录

**日期**：2026-06-03
**对应计划**：`docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` 阶段 B
**阶段目标**：验证 Approval 与安全策略闭环，让 Codex command/file/network/protected-path 请求进入 operator approval 或本地 policy auto-deny，并保持默认验证不依赖真实 Codex。

## 1. 验收结论

阶段 B 已完成；本记录验收 B1、B2 与 B3 的阶段 B 范围：

- B1 / R5 API/model：Approval DTO、OpenAPI、JSON Schema、DB/fallback schema、store/httpapi projection 和 dashboard 类型已对齐，API/dashboard 不暴露内部 `request_json` / `decision_json`。
- B2 / R5 bridge：Codex command/file_change/network approval request 会写入 `approval_requests`，operator 决策能写回 Codex；`deny` 只拒绝当前 action，`cancel_run` 使用 `operator_cancelled`，approval timeout 使用 `approval_timeout`。
- B3 / R12：默认 command allow/review/deny、network default deny、protected-path deny 已接入 Codex approval bridge；auto-deny 写入 `approval_requests.status=auto_denied`，并返回 `command_denied`、`network_denied` 或 `protected_path_denied`。
- Redaction golden fixture 已补齐到 `docs/testing/redaction-golden/redaction-golden.json`，并由 `scripts/validate_contracts.py` 校验 manifest metadata 与 fixture case content，覆盖 prompt、Codex log、secret、diagnostics。
- Tool Gateway `artifact.attach` protected-path 拒绝保持 daemon hard-deny：记录 failed tool_call、无 approval row，且该 tool error 本身不直接终止 run。

## 2. 验收命令

已执行并通过：

```bash
/Users/shuanzi/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.10.darwin-arm64/bin/go test ./internal/security ./internal/httpapi ./internal/store ./internal/agent/codex ./internal/orchestrator ./internal/toolgateway
/Users/shuanzi/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.10.darwin-arm64/bin/go test ./...
python3 -m unittest tests.test_validate_contracts_manifest
PYTHONPATH=/tmp/local-symphony-pydeps python3 scripts/validate_contracts.py
PATH=/Users/shuanzi/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.10.darwin-arm64/bin:$PATH bash scripts/acceptance-local.sh
git diff --check
```

本机默认 `PATH` 中没有 Go；验收使用本机已存在的 Go toolchain 路径。Contract validation 使用已有临时 Python dependency path 提供 `jsonschema`，未修改仓库依赖文件。

## 3. 本阶段新增测试覆盖

- `internal/security`：默认 command allow/review/deny、compound shell command 不自动 allow、protected-path override、`-flag=protected_path` 提取、network default deny/review/allowlist、configured protected path。
- `internal/agent/codex`：unknown network default auto-deny、protected file_change auto-deny、command deny auto-deny、command allow auto-accept without approval row、command review pending approval、network review policy path。
- `internal/store`：auto-denied approval row 的 structured fields、resolved timestamp、reason 和不可二次 decide。
- `internal/toolgateway`：protected artifact attach 失败记录 failed tool_call，不创建 approval row，不直接改变 run terminal status。
- `internal/config` / schemas / examples：`approvals.network.default`、allowlist 和 protected paths 的 workflow parsing/schema/default example。
- `tests/test_validate_contracts_manifest.py` / `scripts/validate_contracts.py`：redaction golden fixture metadata 和 fixture case content 校验，覆盖缺失文件、缺失 surface、空 input/redacted、单个与多个 synthetic sentinel 泄漏。

## 4. 已知边界

- v1 policy evaluator 是 pattern/prefix 分类与浅层 path 提取，不是完整 shell parser、DLP、filesystem sandbox 或 OS-level firewall。
- Network deny 是 Codex-mediated policy decision，不声明 packet-level egress isolation。
- Redaction golden fixture 是合成合同 fixture，不是完整 DLP 或 runtime redaction engine；本阶段继续依赖现有 redacted event、review/artifact refusal、diagnostics redacted-only 合同，后续 D 阶段会补更完整 diagnostics/UX 呈现。

## 5. 后续入口

阶段 C（hook lifecycle、serve tick loop、single daemon ownership/runtime lock，以及 operator CLI over REST）是下一阶段入口。
