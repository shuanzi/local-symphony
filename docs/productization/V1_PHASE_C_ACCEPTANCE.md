# v1 真实产品化阶段 C 验收记录

**日期**：2026-06-08（最后更新）
**对应计划**：`docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` 阶段 C
**阶段状态**：**v1.1 WIP** —— C1+C2 完成；C3 数据层完整、5 轮 codex 滚动 review 收敛 8/9 finding；C4 12 commits（含 5 轮 codex review + 6 轮 adversarial review）实施完整；C3 + C4 都已 v1.1 WIP 路线收口
**阶段目标**：把 daemon 包装成本地产品行为：单 owner project DB、CLI over REST、scheduler tick loop、可恢复的 runtime lock。

## 阶段 C 收口 PR

| 子包 | PR | 状态 |
|---|---|---|
| C3 owner nonce/heartbeat v1.1 WIP | **[#19](https://github.com/shuanzi/local-symphony/pull/19)** | open, waiting review |
| C4 CLI over REST v1.1 WIP | **[#20](https://github.com/shuanzi/local-symphony/pull/20)** | open, waiting review |

## 1. 验收结论

阶段 C 范围内 C1 hook lifecycle、C2 scheduler tick loop 与 C3 runtime ownership guard（v1.1 WIP 状态）已完成：

- **C1 / R7 hook lifecycle**：symphony hook 命令在 ready/run/cleanup/wait/finish/cancel 路径触发，并在 v1 product scope 内保留本地约束（不引入 auto retry / remote hook / dashboard action mutation）。
- **C2 / R8 scheduler tick loop**：`runSchedulerTickLoopWithDrain` 周期 dispatch；in-flight tick 阻塞下一次触发；SIGINT 先 stopScheduler → drain → store close。
- **C3 / R8 runtime ownership**：
  - `runtime_descriptors` 新增 `owner_nonce` / `heartbeat_at` / `heartbeat_ttl_ms` / `acquired_at`；新增 `runtime_owner_events` reap 事件表。
  - `internal/db.MigrateAppSchema` 在 Open / InitProject 时 idempotent 升级既有 v1 app DB。
  - `store.NewOwnerNonce` 用 `crypto/rand` 生成 32 字节（hex 64 字符）nonce；`MinOwnerNonceLength=32` 拒绝更短的值。
  - `CreateRuntimeDescriptorWithNonce` 在 active heartbeat 时返回 `core.ErrDaemonAlreadyRunning`（operator guidance 显式说明）；stale heartbeat / dead PID 时 reap 后接管。
  - `UpdateRuntimeHeartbeat` 在 nonce 不匹配时返回 `core.ErrDaemonAlreadyRunning`，避免误更新被替换的 owner。
  - `ReapStaleRuntimeDescriptors` 周期 + 启动时各跑一次；PID 复用不阻断 reap。
  - `app.Serve` 启动顺序：Reap → listen → CreateWithNonce → ReconcileStaleActiveRuns → load workflow → http server；heartbeat ticker 与 reap ticker 在 SIGINT 时先 stop 再 drain store close。
  - `RuntimeDescriptorSnapshot` 投影只暴露 8 字符 owner_nonce_fingerprint、`heartbeat_at`、`heartbeat_ttl_ms`、`acquired_at`，永不暴露 owner_nonce 明文。
  - `OpenAPI` 与 `schemas/diagnostics.schema.json` 的 `DiagnosticsRuntimeDescriptor` 同步新增 4 个字段；`scripts/validate_contracts.py` 与 `CONTRACT_VALIDATION_MANIFEST.json` 同步描述 runtime_ownership 与 security_regression topics。
  - 既有 v1 app DB（只有 project_id/api_url/tool_gateway_endpoint/daemon_pid/started_at/updated_at）经 MigrateAppSchema 升级后可通过新 store API 直接写入 nonce/heartbeat，路径可走 Open / InitProject / heartbeat update / reap。

## 2. 验收命令

已执行并通过（在 `/.worktree/local-symphony-c3-owner-nonce` 目录下）：

```bash
go build ./...
go test ./internal/db
go test ./internal/store
go test ./internal/app
go test ./internal/observability
go test ./internal/httpapi
python3 scripts/validate_contracts.py
```

- `go build ./...` 无错误。
- `go test ./internal/db` PASS（含新 `TestFallbackAppSchemaIncludesRuntimeOwnerNonceColumns` / `TestMigrateAppSchemaAddsRuntimeOwnerNonceColumns`）。
- `go test ./internal/store` PASS（含 `TestCreateRuntimeDescriptorWithNonceRejectsActiveHeartbeat` / `TestCreateRuntimeDescriptorRecoversFromStaleHeartbeatEvenIfPIDAlive` / `TestCreateRuntimeDescriptorRecoversFromPIDReuseWhenHeartbeatStale` / `TestUpdateRuntimeHeartbeatRefreshesAndDetectsLostOwnership` / `TestReapStaleRuntimeDescriptorsRemovesStaleRows` / `TestReapStaleRuntimeDescriptorsLeavesFreshOwnersAlone` / `TestNewOwnerNonceReturnsAtLeast32HexBytes`）。
- `go test ./internal/app` PASS（含 `TestServeRecoversFromPIDReuseAfterHeartbeatStale` 端到端 PID 复用 reap 测试；既有 `TestServeRuntimeOwnershipConflictDoesNotReconcileActiveRuns` / `TestPrepareServeRuntimeRejectsActiveRuntimeOwner` / `TestPrepareServeRuntimeReleasesRuntimeOwnerWhenCLISessionWriteFails` 仍通过）。
- `go test ./internal/observability` PASS（`TestDiagnosticsIncludesStoredRuntimeDescriptorWithoutSecrets` 已更新断言 owner_nonce 不出现，且 owner_nonce_fingerprint 8 字符 + heartbeat_* / acquired_at 字段出现）。
- `go test ./internal/httpapi` PASS（OpenAPI diagnostics schema 与实际 handler 投影一致）。
- `python3 scripts/validate_contracts.py` PASS（`ok diagnostics schema contract` / `ok sql db/schema/v1_app.sql` / `ok openapi api/openapi.yaml` / `ok manifest docs/testing/CONTRACT_VALIDATION_MANIFEST.json`）。

## 3. 已知限制

**v1.1 WIP 状态**（2026-06-06 收口暂停）：以下限制中"shutdown 顺序与 long-running tick 的并发接管"为 open design problem，留作 C5 daemon lifecycle；其余为 v1 product scope 内的合理折衷。

- 阶段 C 收口后 `store` 包暴露 `CreateRuntimeDescriptorWithNonce`（需要显式 nonce）与旧 `CreateRuntimeDescriptor`（自动生成 nonce）两个入口；C3 收口后的代码路径默认走前者，旧入口仅保留给既有测试与潜在的紧急 fallback。
- `ReapStaleRuntimeDescriptors` 周期默认 60s；如需更短，请在 C3 后续 work package 中扩展 `workflow.yaml` 配置键。
- runtime owner nonce 不写入 dashboard、不进 `/diagnostics` API、不进 `symphony diagnostics export`、不进 run events；operator 仅能看到 8 字符 fingerprint 关联日志。
- `symphony serve` 在 heartbeat 丢失（被新 owner 接管）时仅打印 stderr 错误并退出 goroutine，不主动终止 daemon — operator 介入后须通过 SIGINT 关闭；后续阶段可加入 heartbeat-lost → graceful shutdown hook。
- **（v1.1 WIP 未修 P1）** shutdown 顺序与 long-running tick 的并发接管：在长跑 scheduler tick 与新 owner 并发接管之间，daemon 关闭序列的顺序仍可在小窗口内产生并发接管。详见第 4 节。
- C3 与 C4 平行推进；C4 CLI over REST / daemon session 对齐在另一 worktree。

## 5. 变更清单（C3 收口新增 / 修改文件）

- `db/schema/v1_app.sql` — 新增 owner_nonce / heartbeat_at / heartbeat_ttl_ms / acquired_at 列 + runtime_owner_events 表 + 索引。
- `internal/db/schema.go` — `fallbackAppSchema` 同步；新增 `MigrateAppSchema` / `tableExists` / `tableColumnSet` / `isMissingRow`。
- `internal/db/schema_test.go` — 新增 `TestFallbackAppSchemaIncludesRuntimeOwnerNonceColumns` / `TestMigrateAppSchemaAddsRuntimeOwnerNonceColumns` / `tableColumnNames` / `sortedKeys` 辅助。
- `internal/store/store.go` — 新增 `DefaultRuntimeHeartbeatTTLMS` / `DefaultRuntimeHeartbeatIntervalMS` / `DefaultRuntimeReapIntervalMS` / `MinOwnerNonceLength`；新增 `CreateRuntimeDescriptorWithNonce` / `UpdateRuntimeHeartbeat` / `ReapStaleRuntimeDescriptors` / `NewOwnerNonce` / `ownerNonceFingerprint` / `validateOwnerNonce` / `runtimeOwnerIsLive` / `reapStaleRuntimeDescriptorInTx` / `emitReapEvent`；`CreateRuntimeDescriptor` 现在自动生成 nonce；`RuntimeDescriptorSnapshot` 投影扩展；`core.ErrDaemonAlreadyRunning` 错误码。
- `internal/store/store_test.go` — 调整 `TestCreateRuntimeDescriptorRecoversStaleOwner` 模拟 heartbeat 停滞；调整 `runtimeDescriptorRow` helper；新增 7 个 owner-nonce/heartbeat 相关测试。
- `internal/app/app.go` — `Serve` 启动顺序增加 `ReapStaleRuntimeDescriptors` + heartbeat ticker + reap ticker；`prepareServeRuntime` 接受 nonce 参数；新增 `heartbeatConfig` / `reapInterval` / `runRuntimeHeartbeatLoop` / `runRuntimeReapLoop`。
- `internal/app/app_test.go` — `prepareServeRuntime` 调用增加 nonce 参数；新增端到端 `TestServeRecoversFromPIDReuseAfterHeartbeatStale`。
- `internal/core/core.go` — 新增 `ErrDaemonAlreadyRunning` API 错误码。
- `internal/observability/diagnostics_test.go` — `assertNoSensitiveRuntimeDescriptorFields` 同时拒绝 `owner_nonce` 明文；新增 `owner_nonce_fingerprint` / `heartbeat_at` / `heartbeat_ttl_ms` / `acquired_at` 字段断言。
- `api/openapi.yaml` — `DiagnosticsRuntimeDescriptor` 新增 4 个字段。
- `schemas/diagnostics.schema.json` — `runtimeDescriptor` 新增 4 个字段。
- `scripts/validate_contracts.py` — `required_columns["runtime_descriptors"]` + `runtime_owner_events`；`DIAGNOSTICS_DEFINITION_REQUIRED_FIELDS["runtimeDescriptor"]` 与 OpenAPI 一致。
- `docs/testing/CONTRACT_VALIDATION_MANIFEST.json` — 新增 `runtime_ownership` 段；`security_regression.topics` 新增 `runtime owner nonce fingerprint only via diagnostics` / `runtime owner heartbeat stale recovery on PID reuse`。
- `docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` — C3 章节进度更新为 v1.1 WIP 状态；C3 收口追加 6 个 work package + 5 轮 review 累计 finding 表 + 已知限制子节；阶段 C checklist 标记完成（含 WIP 标注）。

## 4. v1.1 WIP 状态与已知限制

C3 v1.1 WIP 决策时间：2026-06-06
C3 owner lock 累计 5 轮 codex 滚动 review，5 轮共 9 finding（前 4 轮的 8 个已修，最后 1 个 P1 留作已知限制）。

### 4.1 4 个已 commit 落地（在 worktree `codex/v1-productization-c3-owner-nonce`）

| Commit | 范围 | 累计修 finding |
|---|---|---|
| `dbf4c6e` | C3 主体（owner nonce / heartbeat / diagnostics / 收口测试）| 1P1 + 1P2（round-1） |
| `ee4f8a5` | round-2：PID 存活判断先于 TTL | 1P2（round-2） |
| `e136dfc` | round-3：scheduler dispatch nonce gate + migrated row reap + `daemon_already_running` schema | 1P1 + 2P2（round-3） |
| `d8840d2` | round-4：live migrated owner 保留 + 测试用 context-cancel 替代 SIGINT | 1P1 + 1P2（round-4） |

### 4.2 1 P1 已知限制（未修，留作 C5 daemon lifecycle design problem）

**Shutdown 顺序与 long-running tick 的并发接管（round-5 暴露）**

- 在长跑 scheduler tick 与新 owner 并发接管之间，daemon 关闭序列的顺序仍可在小窗口内产生并发接管。
- 修复方向（**不强制在 C3 收口内解决**）：
  - 在 C5 阶段引入统一的"drain-then-exit"生命周期：
    1. 停 dispatcher（拒绝新 dispatch）
    2. 等 in-flight tick 完成
    3. 停 scheduler ticker
    4. 停 heartbeat ticker
    5. 停 reap ticker
    6. 关 HTTP server
    7. close store
  - 该改造需协调多 goroutine 的 cancel propagation，超出 C3 收口范围。

### 4.3 v1.1 WIP 适用范围

- **数据层（schema / nonce / heartbeat / diagnostics / migrated 兼容）已 5 轮 review 无新 finding**——生产可用。
- **协调层（scheduler nonce gate / shutdown ordering / long-running tick race）1 P1 未修**——C5 收口前避免将该部分宣传为完全收口。
- 后续若需补 C5 的 shutdown lifecycle，建议在新 worktree 分支（例如 `codex/v1-productization-c5-daemon-lifecycle`）追加 commit，**不要**改动本 worktree 的 4 个 commit hash。

### 4.4 验收状态保留声明

- 数据层相关测试 + contract validation + diagnostics schema 在 5 轮 review 中稳定无新 finding。
- 阶段 D 的 Operator 体验与发布验收可以基于"数据层完整、行为正确"这一状态推进；shutdown 顺序相关的边界条件应作为已知限制在 v1 文档中显式标注。
- 主计划 `docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` 阶段 C checklist 已同步标注 v1.1 WIP 状态。
