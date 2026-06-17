# D1 / R10 阶段收口 notes（v1.1 WIP 收口路径）

**日期**：2026-06-09
**worktree 分支**：`codex/v1-productization-d1-review-packet`
**主体 commit**：`2cc1888`
**最终状态**：D1 / R10 进入 v1.1 WIP 收口，**不**再开新 codex review track

## 决策

采用 **方案 A（v1.1 WIP 收口）**，与 D3 / R14（5 轮 review 收口）、D6 / R15（无 R2 review 落盘即收口）的 v1.1 WIP 策略保持一致。

理由：
- D1 / R10 主要 contract（结构化 review packet 投影 / raw prompt-Codex log-secret 拒绝 / EvalSymlinks 沙箱 / openapi-schema 一致）已在 2 轮 codex review 期间修毕。
- 任何后续残余的"边角 finding"应在 v1.1 WIP 阶段继续按需修复，**不**阻塞 v1 主线 ship。
- codex-review-d1-round2 agent 已 sign-off 关闭，无法再开 R3 review track，重新跑 review 会拉长 stage D 收口时间表。

## Round 1（commit `2cc1888` → `0aee74c`）— 0 P1 + 2 P2 + 1 P3

| # | Sev | finding | 修复 |
|---|-----|---------|------|
| 1 | P2 | `ReviewPacketArtifact` per-artifact map 多了未声明的 `raw_*_exposed` 字段 | api/openapi.yaml + schemas/review_packet.schema.json 都补三个 boolean；local `ReviewPacketProjection` 同步 |
| 2 | P2 | `safeContainedPath` 词法检查，缺 `EvalSymlinks`，symlink 可绕过 | internal/store/store.go 补 EvalSymlinks + 保留 `os.IsNotExist` 兜底 |
| 3 | P3 | `ReviewPacketSummary` 缺 `git` 字段声明 | api/openapi.yaml 加 git object schema + required |

## Round 2（commit `0aee74c` → `2709a94`）— 1 P1 + 2 P2（全部为 R1 修复引入的边角回归）

| # | Sev | finding | 修复 |
|---|-----|---------|------|
| 1 | P1 | Generator 写出的 review.json 不再校验通过 schemas/review_packet.schema.json | schemas 顶层 required 移除 `artifacts`（HTTP 路径从 DB 派生）；Generator 只在 promptID 非空时写 `prompt_snapshot` 字段 |
| 2 | P2 | `ReviewPacketArtifact.kind.enum` 比 `Artifact.kind.enum` 窄 | openapi + JSON schema 都补齐为 `Artifact.kind` 的超集（prompt_snapshot / codex_log / secret_artifact / secrets / agent_file / diagnostic） |
| 3 | P2 | daemon 端 `raw_secret_exposed` 始终为 false，local store 端为 true | internal/httpapi/httpapi.go 把 secret 谓词从 `false` 改为 `a.Kind == "secret_artifact" \|\| a.Kind == "secrets"`，与 store 端 `isRawArtifactRefusalKindLocal` / openapi 描述对齐 |

## 已知限制（v1 阶段保留，非阻塞）

见 `docs/productization/D1_REVIEW_PACKET_NOTES.md` "已知限制" 节：

- Dashboard TypeScript 类型用 `additionalProperties: true` 兼容老 daemon；
- `ToolCallsForRun` 返回的 `input_hash` / `output_hash` 是 raw payload 的 SHA256 hash，不等于原始 prompt / Codex 输出内容；
- `reviewStructuredProjection` 在 `review.json` 不可解析时返回默认值（仍不会泄露 raw 内容）；
- CLI `symphony review path` 没有 `--json` 形态；
- OpenAPI `ReviewPacketSummary` 加 `additionalProperties: true` 兼容老 daemon。

## 全量验证（最终态）

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/review/ ./internal/httpapi/ ./internal/store/ ./internal/cli/` | ok |
| `go test -p 1 ./internal/...` | all internal packages PASS |
| `python3 scripts/validate_contracts.py` | contract validation passed |
| `bash scripts/acceptance-local.sh` | acceptance-local passed |
| `cd web && npx tsc --noEmit && npm test` | web tests passed |

## 分支 commit 列表（4 个）

```
2709a94 D1 R2: fix codex review round 2 findings (1 P1 + 2 P2)
0aee74c D1 R1: fix codex review round 1 findings (0 P1 + 2 P2 + 1 P3)
d0677a1 D1: notes for review packet structured projection work
2cc1888 D1: Review Packet API returns structured projection + raw refusal
```

## 风险与后续

- v1.1 WIP 阶段若发现新 D1 finding，由 `d1-review-packet` agent 在 v1.1 worktree 继续修。
- v1 主线 ship 后，D1 实施 agent 角色关闭。
