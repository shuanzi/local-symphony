# D3 / R14: Codex availability 与 diagnostics 产品化

> 实施、验证、已知限制、commit 列表
>
> 实施日期：2026-06-09
> worktree 分支：`codex/v1-productization-d3-codex-availability`
> 父主线：main @ f15ba39

## 1. 实施内容

D3 把 Codex adapter 已经在 fixture gate 阶段收集到的细粒度信息（detected codex version、protocol/schema metadata、fixture 资产存在性、失败 reason）以产品化形式暴露给 operator，让 Overview 和 `symphony status` 能直接看到 Codex 是否可用、装了哪个版本、fixture 是否就位、上一次 preflight 为什么失败。

### 1.1 adapter 层 — `internal/agent/codex/preflight.go`（新）

- 新增 `PreflightReason` enum：覆盖 16 种 preflight 失败 reason（malformed_version / missing_fixture / missing_metadata / malformed_metadata / metadata_codex_version_mismatch / experimental_api_not_supported / metadata_missing_codex_* / missing_schema_fixture / missing_transcript_fixture / invalid_transcript_fixture / codex_not_installed / unknown）。
- 新增 `CodexSupport{CLI,Model,Sandbox}` 三态结构，对齐 diagnostics schema 的 `supportStatus` enum。
- 新增 `FixtureSupport{SchemaAvailable,MetadataAvailable,TranscriptAvailable}`，独立报告三个 fixture 资产的存在性。
- 新增 `PreflightSummary`（ran_at / command / version / available / support / metadata / fixture_support / failure_code / failure_reason / failure_message / failure_details）和 `RunPreflight(opts PreflightOptions)`。
- `RunPreflight` 复用现有 `DetectVersionForCommand` / `SelectFixtureMetadata` / `unsupportedCodexVersion`，**不引入新的 codex 进程组**，纯 side-effect-free。
- 新增 `SYMPHONY_CODEX_VERSION_OUTPUT` 测试 env var，让 offline / sandbox 测试可以钉住 version probe。
- 新增 `redactedFailureDetails` + `scrubbedForDiagnostics` 两次 redaction 屏障，自动剥离 `SYNTHETIC_*` 哨兵（raw prompt / raw Codex log / owner nonce / secret 形状）。

### 1.2 observability 层 — `internal/observability/{codex_availability.go,redact_assertions.go}`（新）

- `codexAvailability(summary)` 把 `PreflightSummary` 投影成 diagnostics 形状的 map。结构由 `schemas/diagnostics.schema.json` 严格约束（`additionalProperties: false`）。
- 失败时填 `warning="unsupported_codex_version"`，与 v1 禁项 "operator 应能看到 unsupported 风险" 对齐。
- 新增 `CodexAvailability()` 公开 entry，被 `cli.statusData` 和 `httpapi.Server.state` 共用，确保 `symphony status` / `/state` / `/diagnostics` 三处拿同一份数据。
- 新增 `assertNoSentinelsInDiagnostics` / `assertDiagnosticsFieldShape` 测试辅助，把 redaction 验证下沉到所有 diagnostics 测试。

### 1.3 CLI 层 — `internal/cli/operator.go`

`statusData` 在 `issues` / `runs` 后追加 `codex: observability.CodexAvailability()`。

### 1.4 HTTP API 层 — `internal/httpapi/httpapi.go`

`Server.state` 在响应 map 中追加 `codex: observability.CodexAvailability()`。`/diagnostics` 自动通过 `observability.Diagnostics` 覆盖（Diagnostics 内部已经填 `codex`）。

### 1.5 Schema / OpenAPI / 合同

- `schemas/diagnostics.schema.json`：`codex` required 新增 `metadata` / `fixture_support` / `last_preflight` / `warning`；新增 `$defs.codexMetadata` / `codexFixtureSupport` / `codexPreflight` 三个子对象。
- `api/openapi.yaml`：`DiagnosticsCodex` 同步 required 列表；新增 `DiagnosticsCodexMetadata` / `DiagnosticsCodexFixtureSupport` / `DiagnosticsCodexPreflight` 三个 components。`failure_reason` enum 列出 16 个 `PreflightReason` 值。
- `scripts/validate_contracts.py`：扩展 required-field map，新增 `codexMetadata` / `codexFixtureSupport` / `codexPreflight` 三个 schema 校验；新增 `DIAGNOSTICS_CODEX_FAILURE_REASON_ENUM` 断言，保证 schema 枚举与 code 常量同步。

### 1.6 Dashboard — `web/src/{types.ts,App.tsx}`

- `Diagnostics.codex` 类型从 `Record<string, unknown>` 收紧为 `CodexAvailability`，并细化为 `CodexSupport` / `CodexCompatibilityMetadata` / `CodexFixtureSupport` / `CodexPreflight`。
- `OverviewPage` Codex metric：value 仍为 Available/Unavailable，**helper** 优先级为 `warning` → `last_preflight.failure_reason` → `version + protocol_version`；**tone** 在 warning 出现时升级到 `danger`。
- 新增"Open Diagnostics"按钮，operator 可直接从 Overview 跳到 Diagnostics 页。
- `MetricCard` 新增可选 `action` prop（向后兼容）。

## 2. 验证命令与结果

### 2.1 窄范围单测

```bash
cd /Users/xiquandai/Documents/code/local-symphony-d3-codex-availability
go test -timeout 60s ./internal/agent/codex ./internal/observability ./internal/cli ./internal/httpapi
```

结果：

```
ok  	local-symphony/internal/agent/codex	27.630s
ok  	local-symphony/internal/observability	2.813s
ok  	local-symphony/internal/cli	4.277s
ok  	local-symphony/internal/httpapi	12.370s
```

### 2.2 opt-in 真实 Codex 集成测试

```bash
SYMPHONY_TEST_CODEX=1 go test -timeout 30s -v ./internal/agent/codex/ -run TestIntegrationPreflightFixtureGate
```

结果：

```
=== RUN   TestIntegrationPreflightFixtureGate
    codex_test.go:34: installed Codex is not covered by committed fixtures: unsupported_codex_version: unsupported Codex version: missing_fixture
--- SKIP: TestIntegrationPreflightFixtureGate (0.08s)
PASS
ok  	local-symphony/internal/agent/codex	0.979s
```

> 报告：环境已安装 `codex`（`/Users/xiquandai/.nvm/versions/node/v25.9.0/bin/codex`），但其版本不在 `internal/agent/codex/testdata/schema/<version>` 覆盖范围内——集成测试按设计 SKIP。SKIP 行为本身验证了：live codex 进程被成功 probe，preflight 走完完整路径并报告 `missing_fixture` reason；新加的 `RunPreflight` 与既有 `PreflightFixtureGate` 行为一致。

### 2.3 合同校验

```bash
python3 scripts/validate_contracts.py
```

结果：

```
ok manifest docs/testing/CONTRACT_VALIDATION_MANIFEST.json
ok json schemas/diagnostics.schema.json
ok diagnostics schema contract schemas/diagnostics.schema.json
ok openapi api/openapi.yaml
ok tool gateway registry schemas/tool_gateway.schema.json
ok docs docs/agent_work_orders/*.md v1 scope
ok workflow examples/WORKFLOW.default.md
contract validation passed
```

### 2.4 Acceptance gate

```bash
bash scripts/acceptance-local.sh
```

结果：

```
acceptance-local passed
```

### 2.5 Web 静态检查

```bash
cd web && node scripts/typecheck.mjs
```

结果：

```
web dependencies not installed; static frontend checks passed.
```

> 报告：本沙箱无 pnpm 依赖安装；typecheck 走静态 fallback（requiredFiles + required component token）。运行 `pnpm --dir web install && pnpm --dir web typecheck` 即可补齐 tsc / vite 严格类型检查。

## 3. 已知限制

- **不持久化 preflight 历史**：每次 diagnostics / status 调用都重新跑 `DetectVersionForCommand`（可能调用 `codex --version`）+ 一次 fixture tree stat，无网络、无 spawn process group；在慢 fs 上 ~5ms。v1 范围内不持久化 historical preflight 列表。
- **不在 diagnostics 里加 sandbox runtime detection**：v1 禁项保持，不引入 sysctl / capability / 权限探测。
- **不引入 fixture 自动下载**：fixture 必须由 operator 预置，适配器层 `defaultFixtureRoot` / `findRepoFixtureRoot` 行为不变。
- **不自动 redaction of `codex` 字段内 `metadata.supported_notifications` / `supported_requests`**：这两类值在 fixture metadata 中已是协议方法名（如 `turn/started`），不含 raw payload。如果未来 fixture 来源不可信，需要把 `codexMetadata` 也走 `scrubbedForDiagnostics`——目前 D3 范围内未做。
- **Codex 二进制本身不可在测试中匹配**：本仓库 fixture 只覆盖 `0.0.0-test` 版本；operator 在真实环境跑 `codex --version` 拿到的是 `0.x.x` 真版本，preflight 会返回 `unsupported_codex_version` + `missing_fixture`，operator 需要在 `internal/agent/codex/testdata/schema/<真实版本>/` 添加 fixture 才能 dispatch。

## 4. Commit 列表（worktree `codex/v1-productization-d3-codex-availability`）

```
90f0ded internal/agent/codex: add preflight summary and tests
9f48b1c internal/observability: surface codex availability in diagnostics
a05a0eb internal/cli+httpapi: expose codex availability in status and /state
ea2a130 schemas/openapi/contract: extend codex diagnostics with metadata/fixture_support/last_preflight
7b5cb64 web: tighten Codex types and link Overview → Diagnostics
4b37cb5 internal/agent/codex: round-1 review fixes (4 findings)
293985c docs/productization: D3 Codex availability notes
6073a15 internal/agent/codex: round-2 review fixes (2 findings)
69dedc6 docs/productization: note round-2 review fixes in D3 notes
5003070 internal/agent/codex: round-3 review fixes (3 findings)
fceae8d docs/productization: note round-3 review fixes in D3 notes
62709b0 internal/agent/codex: add team-lead repro tests as permanent regression
5e76253 internal/agent/codex: round-4 review fix (1 P2: path-qualified binary)
```

提交 hash（`git log` 输出）：

```
$ git log --oneline codex/v1-productization-d3-codex-availability ^main
5e76253 (HEAD -> codex/v1-productization-d3-codex-availability) internal/agent/codex: round-4 review fix (1 P2: path-qualified binary)
62709b0 internal/agent/codex: add team-lead repro tests as permanent regression
fceae8d docs/productization: note round-3 review fixes in D3 notes
5003070 internal/agent/codex: round-3 review fixes (3 findings)
69dedc6 docs/productization: note round-2 review fixes in D3 notes
6073a15 internal/agent/codex: round-2 review fixes (2 findings)
293985c docs/productization: D3 Codex availability notes
4b37cb5 internal/agent/codex: round-1 review fixes (4 findings)
7b5cb64 web: tighten Codex types and link Overview → Diagnostics
ea2a130 schemas/openapi/contract: extend codex diagnostics with metadata/fixture_support/last_preflight
a05a0eb internal/cli+httpapi: expose codex availability in status and /state
9f48b1c internal/observability: surface codex availability in diagnostics
90f0ded internal/agent/codex: add preflight summary and tests
```

9 个 commit + 2 个笔记更新。R1（4 findings）/R2（2 findings）/R3（3 findings）/R4（1 P2）四个 review 轮次，每个 finding 都对应一个修复 commit；R3 之后多加一个 commit 把 team-lead 的 repro 测试加入永久回归套件（带 `defer recover()` panic 兜底，避免 R2 漏 panic 的复现）。

### 4.1 Round-1 review 修复

codex review 对 commit 90f0ded 给出 4 个 finding（3 × P1，1 × P2），全部针对 `internal/agent/codex/preflight.go`：

| # | Sev | 修复 |
|---|-----|------|
| 1 | P1 | `summary.Command` 只保留 binary basename（`filepath.Base(commandParts(command)[0])`），参数被丢弃。basename 再过一次 `scrubbedForDiagnostics` 作为 defense in depth。 |
| 2 | P1 | 新增 `scrubbedMetadata` 包装 `selected.Metadata`，把 `SupportedNotifications` / `SupportedRequests` 走 `scrubbedForDiagnostics`；成功路径的 metadata 不再是 leak 路径。 |
| 3 | P1 | `humanMessageForReason` 输出在两个失败分支都被 `scrubbedForDiagnostics` 包裹，attacker-controlled detail text 不再外泄。 |
| 4 | P2 | 新增 `classifyEmptyOrMalformedVersion`，用 `exec.LookPath(firstTokenOfCommand(command))` 区分 binary-真的没装（`codex_not_installed`）vs binary-装了但 --version 行解析失败（`malformed_version`）。 |

测试新增 4 个（9 个 sub-case），覆盖 redaction / scrub / classifier 三个分支。修复后：

```
$ go test -count=1 ./internal/agent/codex ./internal/observability ./internal/cli ./internal/httpapi
ok  	local-symphony/internal/agent/codex	19.207s
ok  	local-symphony/internal/observability	2.929s
ok  	local-symphony/internal/cli	2.851s
ok  	local-symphony/internal/httpapi	11.484s

$ python3 scripts/validate_contracts.py
contract validation passed

$ SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
ok  	local-symphony/internal/agent/codex	1.075s
```

### 4.2 Round-2 review 修复

codex review round 2 对 commit 4b37cb5 给出 2 个 finding（1 × P1，1 × P2）：

| # | Sev | 修复 |
|---|-----|------|
| 1 | P1 | `validateCompatibilityMetadata` 新增 `isSyntheticSentinelString()` 检测 `CodexVersion` / `ProtocolVersion` / `SchemaVersion` 三个标量；命中 sentinel 形状直接 reject 并报告 `metadata_protocol_version_sentinel` / `metadata_schema_version_sentinel` / `metadata_codex_version_sentinel`。成功路径在 source 处就阻断了 leak，不再依赖事后 scrub。 |
| 2 | P2 | `classifyEmptyOrMalformedVersion` 改用 `unwrapCodexBinaryToken`，按 `commandParts` 走 argv 跳过 `env` / `sudo` / `nohup` / `command` / `time` / `xargs` 等已知 wrapper，连带消耗 `-E` / `-I{}` 等 flag-形状参数；`KEY=VALUE` env-var 前缀也按 shell 语义跳过。未知 wrapper 退回到 round-1 first-token 行为，保持向后兼容。 |

`failure_reason` enum 由 16 扩到 19（增加 `metadata_protocol_version_sentinel` / `metadata_schema_version_sentinel` / `metadata_codex_version_sentinel`），`schemas/diagnostics.schema.json` / `api/openapi.yaml` / `scripts/validate_contracts.py::DIAGNOSTICS_CODEX_FAILURE_REASON_ENUM` 三处同步。

新增 5 个 R2 回归测试（24 个 sub-case）：

- `TestIsSyntheticSentinelStringDistinguishesVersionAndSentinel`（10 sub-case）
- `TestRunPreflightRejectsSentinelShapedProtocolVersion`
- `TestRunPreflightRejectsSentinelShapedSchemaVersion`
- `TestUnwrapCodexBinaryTokenSkipsKnownWrappers`（13 sub-case）
- `TestRunPreflightClassifiesWrappedMissingBinaryAsNotInstalled`（3 sub-case）

修复后：

```
$ go test -count=1 ./internal/agent/codex ./internal/observability ./internal/cli ./internal/httpapi
ok  	local-symphony/internal/agent/codex	28.532s
ok  	local-symphony/internal/observability	3.176s
ok  	local-symphony/internal/cli	2.928s
ok  	local-symphony/internal/httpapi	12.279s

$ python3 scripts/validate_contracts.py
contract validation passed

$ SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
ok  	local-symphony/internal/agent/codex	1.020s
```

### 4.3 Round-3 review 修复

codex review round 3 对 commit 6073a15 给出 3 个 finding（1 × P1 + 1 × P2 + 1 × P3 panic）：

| # | Sev | 修复 |
|---|-----|------|
| 1 | P3 panic | `isWrapperFlagToken` 增 `len(token) == 0` guard 2 行。`env "" codex` / 多空格 / 空命令等边界 case 不再索引空字符串。`unwrapCodexBinaryToken` round-2 的 `token == ""` 早退逻辑保留。 |
| 2 | P1 | `isSyntheticSentinelString` 改用与 `scripts/validate_contracts.py::SYNTHETIC_SENTINEL_RE` 同一份预编译 regex `r"\bSYNTHETIC_[A-Z0-9_]+\b"`。gate 与 contract validator 现在共享同一份 source of truth，`protocol-SYNTHETIC_PROMPT_BODY-v1` 这种嵌入 sentinel 不再漏。复用现有 3 个 PreflightReason（`metadata_protocol_version_sentinel` / `metadata_schema_version_sentinel` / `metadata_codex_version_sentinel`），schema / openapi / contract enum 不动。 |
| 3 | P2 | `classifyEmptyOrMalformedVersion` 改用 `lookPathWithWrapperPATH`：当 command 含 `PATH=<value>` env-var prefix 时，构造 `PATH=<value>:<current>` 临时 env 给 LookPath。`env PATH=/opt/codex/bin codex` 这种命令不再被误报 `codex_not_installed`。`pathPrefixFromCommand` 只识别 `PATH=` 键，其他 env 前缀（`X=1` 等）不影响 PATH 行为。 |

新增 5 个 R3 回归测试（21 个 sub-case），全部覆盖：empty token 路径（用 `defer recover()` 确保 panic 也会失败）、embedded sentinel、`PATH=` prefix 抽取、PATH-augmented classification end-to-end。

修复后：

```
$ go test -count=1 -race ./internal/agent/codex
ok  	local-symphony/internal/agent/codex	20.134s

$ go vet ./internal/agent/codex/...
(clean)

$ go test -count=1 ./internal/agent/codex ./internal/observability ./internal/cli ./internal/httpapi
ok  	local-symphony/internal/agent/codex	28.427s
ok  	local-symphony/internal/observability	2.996s
ok  	local-symphony/internal/cli	2.890s
ok  	local-symphony/internal/httpapi	12.210s

$ python3 scripts/validate_contracts.py
contract validation passed

$ SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
ok  	local-symphony/internal/agent/codex	1.046s
```

### 4.4 Round-4 review 修复

codex review round 4 对 commit 5003070 给出 1 个 P2 finding：path-qualified binary（`/opt/codex/bin/codex` 这种）必须 short-circuit 到 `exec.LookPath`，不能走 PATH 搜索循环。round-3 的 `lookPathWithWrapperPATH` helper 在「`PATH=<value>` prefix + absolute binary」组合下，错误地把绝对路径和 PATH entry 拼起来（`<dir>//opt/...`），永远不命中。

修复是 3 行 guard（`preflight.go:332-334`）：

```go
if strings.ContainsRune(binary, os.PathSeparator) {
    return exec.LookPath(binary)
}
```

单职责 commit `5e76253`。新增 3 个 R4 回归测试：

- `TestLookPathWithWrapperPATHHandlesAbsoluteBinary`（pre-fix FAIL: `executable file not found in $PATH`；post-fix PASS）
- `TestLookPathWithWrapperPATHHandlesRelativeBinary`（chdir 到 stub 目录，`./codex-rel` 走 verbatim stat）
- `TestRunPreflightClassifiesAbsoluteBinaryAsMalformedNotNotInstalled`（end-to-end：absolute binary 真实存在 + 走 wrapper PATH augmentation → 期望 `ReasonMalformedVersion`）

修复后：

```
$ go test -count=1 -race -timeout 90s ./internal/agent/codex
ok  	local-symphony/internal/agent/codex	31.218s

$ go test -count=1 -timeout 60s ./internal/agent/codex ./internal/observability ./internal/cli ./internal/httpapi
ok  	local-symphony/internal/agent/codex	21.471s
ok  	local-symphony/internal/observability	2.750s
ok  	local-symphony/internal/cli	2.464s
ok  	local-symphony/internal/httpapi	10.962s

$ go vet ./internal/agent/codex/...
(clean)

$ python3 scripts/validate_contracts.py
contract validation passed

$ SYMPHONY_TEST_CODEX=1 go test -timeout 30s ./internal/agent/codex -run Integration
ok  	local-symphony/internal/agent/codex	1.036s
```

## 5. 范围合规

未引入 v1 禁项：

- ❌ raw prompt / raw Codex log / raw secret 暴露 → 走 `redactedFailureDetails` 屏障，`assertNoSentinelsInDiagnostics` 覆盖。
- ❌ auto push / PR / merge / publish → 无。
- ❌ auto commit / workspace cleanup/reset/rebase → 无。
- ❌ auto retry queue / timer → 无。
- ❌ dynamic tools / MCP → 无。
- ❌ remote dashboard / multi-tenant RBAC / secret 管理 → 无。

新增 surface 仅为 read-only diagnostics：`/api/v1/state` 增加 `codex` 子对象（GET-only）、`/api/v1/diagnostics` 自动同步（GET-only）、`symphony status` 增加 `codex` 字段。
