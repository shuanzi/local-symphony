# D1 Review Fix — R-Fix 实施说明

> 工作分支：`codex/v1-productization-d1-review-packet`（PR #26 / D1）
> 修复日期：2026-06-09
> 修复范围：codex review round（PR #26）指出的 2 个 finding（1 P1 + 1 P2）

## 1. 背景

D1（R10 Review Packet API）上线后，codex review 在 PR #26 上标记了 2 个 finding：

- **F1（P1）**：`db/schema/v1_project.sql` 的 `artifacts.kind` CHECK 仍只允许
  老的 enum（`test_output/patch/.../codex_log/review_packet/agent_file/diagnostic/other`），
  不接受 D1 新引入的 raw 拒绝类 kind（`codex_events / prompt_rendered /
  prompt_context / prompt_meta / prompt_tool_manifest / secret_artifact / secrets`）。
  实际 attach 这些 kind 时 `Store.InsertArtifact` 会被 `artifacts.kind` CHECK
  拒绝。fallback `internal/db/schema.go` 故意省略了 CHECK，所以单元测试漏过。
- **F2（P2）**：`internal/toolgateway/toolgateway.go` 的 `allowedArtifactKind`
  以及 `schemas/tools/artifact_attach.input.schema.json` 的 `kind.enum` 同样
  只列了老 enum。Agent 走 `artifact.attach` 调用新 kind 时，会在 persist
  之前被 `validateToolInput` 拒绝。

修完后，Agent 可经 `artifact.attach` 写入全部 raw 拒绝类 kind；
Review/Artifact API 可正常返回 `content_url=null` 给这些 kind；
遗留的 pre-D1 project DB 通过幂等 migration 升级到新 CHECK。

## 2. 改动清单

| 文件 | 变更 |
| --- | --- |
| `db/schema/v1_project.sql` | `artifacts.kind` CHECK 扩到 19 个值（含新 7 个 kind） |
| `internal/db/schema.go` | 新增 `MigrateProjectSchema(database *DB) error`：对 pre-D1 project DB 用表重建的方式幂等扩 CHECK |
| `internal/store/store.go` | `InitProject` 与 `Open` 都在读取 v1_project.sql 后调用 `MigrateProjectSchema`，确保老 DB 升级 |
| `internal/toolgateway/toolgateway.go` | `allowedArtifactKind` 加 7 个新 kind |
| `schemas/tools/artifact_attach.input.schema.json` | `kind.enum` 加 7 个新 kind，与 `api/openapi.yaml` 已有的 `Artifact.kind` enum 对齐 |
| `internal/db/schema_test.go` | 新增 2 个失败测试 → 通过：production schema 接受新 kind + 老 DB 迁移幂等 |
| `internal/toolgateway/toolgateway_test.go` | 新增 1 个失败测试 → 通过：tool gateway 接受新 kind 并落库 |

## 3. Test-First 验证

| Test | FAIL（修复前） | PASS（修复后） |
| --- | --- | --- |
| `TestProjectSchemaArtifactsKindCheckAcceptsNewKinds`（7 子测，覆盖 7 个新 kind） | FAIL — production `v1_project.sql` CHECK 拒绝 `codex_events` 等 | PASS |
| `TestMigrateProjectSchemaWidenArtifactsKindCheck` | FAIL — `MigrateProjectSchema` 未实现（build 错） | PASS — 旧 `codex_log` 行保留，7 个新 kind 可写 |
| `TestArtifactAttachAcceptsNewRawRefusalKinds`（7 子测） | FAIL — `kind is invalid` 拒绝 `codex_events` 等 | PASS — 全部落库，`artifacts.kind` 与请求一致 |

## 4. Migration 设计

`MigrateProjectSchema` 是 `MigrateAppSchema` 的兄弟函数。设计要点：

- **幂等性**：用 `artifactsKindProbe` 在事务内尝试 insert 一个 `codex_events`
  sentinel。成功说明 CHECK 已经够宽，立刻 return；失败才进入 rebuild 路径。
- **表重建**：SQLite 不支持 `ALTER TABLE ... ALTER CONSTRAINT`，所以在
  `PRAGMA foreign_keys=OFF` 事务里用 `artifacts_new` 中转 — `INSERT INTO
  artifacts_new SELECT ... FROM artifacts`、`DROP TABLE artifacts`、
  `ALTER TABLE artifacts_new RENAME TO artifacts`。
- **保留数据**：rebuild 完成后重新创建 `idx_artifacts_run_kind` 和
  `idx_artifacts_issue_kind` 两个索引，与 v1_project.sql 保持一致。
- **FK 重建**：`artifacts` 上的三个 `ON DELETE SET NULL` FK（`issues /
  run_attempts / review_packets`）必须重新声明，否则 rebuild 出来的表
  就丢失了 FK 语义（也会让 `PRAGMA foreign_keys=ON` 的下一次 probe
  因找不到 `review_packets` 报 `no such table`）。

## 5. 全量验证

```
go test -count=1 ./internal/review ./internal/httpapi ./internal/store ./internal/cli ./internal/toolgateway ./internal/db
  → 6 个包全部 ok

go test -p 1 -count=1 ./...
  → 全部 ok（review 6.8s、httpapi 9.5s、orchestrator 8.7s、agent/codex 25s 等）

python3 scripts/validate_contracts.py
  → contract validation passed（包含 schemas/tools/artifact_attach.input.schema.json 改后的 enum）

bash scripts/acceptance-local.sh
  → acceptance-local passed

(cd web && npx tsc --noEmit && npm test)
  → tsc 静默通过、web tests passed
```

## 6. 风险与后续

- **零数据丢失**：rebuild 路径只针对 CHECK 变更，列定义（NOT NULL /
  外键 / 索引）完全保留，单元测试用 `art_legacy`（kind=`codex_log`）
  验证了行保留语义。
- **MigrateProjectSchema 调用点**：加在 `InitProject`（新建项目，
  v1_project.sql 已是新 CHECK，probe 立刻成功，no-op）和 `Open`（打开
  已有项目，对 pre-D1 DB 执行 rebuild）。两条路径都覆盖。
- **F1 finding 的 root cause**——fallback `internal/db/schema.go`
  `fallbackProjectSchema` 故意省略 `artifacts.kind` CHECK——保持不变。
  fallback 是给 in-memory test 用的"宽松 schema"，生产 schema 与
  `allowedArtifactKind` / JSON schema enum 必须三者同步。本次的
  `TestProjectSchemaArtifactsKindCheckAcceptsNewKinds` 已经把这个三方
  同步通过 `ReadSchema` 强约束到生产 v1_project.sql。
- **后续 C3 阶段 CI 可以再加一个 round-trip 测试**：起一个 pre-D1
  project DB → 跑 `MigrateProjectSchema` → 跑 D1 review packet API
  写入所有 7 个新 kind → 校验 `content_url=null`。本 R-fix 仅在单测
  层覆盖，避免引入过大的 fixture。

## 7. Commit 与 Push

```
git add db/schema/v1_project.sql \
        internal/db/schema.go \
        internal/db/schema_test.go \
        internal/store/store.go \
        internal/toolgateway/toolgateway.go \
        internal/toolgateway/toolgateway_test.go \
        schemas/tools/artifact_attach.input.schema.json

git commit -m "D1 R-fix: 2 review findings (F1 widen artifacts.kind CHECK incl. migration, F2 allowedArtifactKind sync)"

git push origin codex/v1-productization-d1-review-packet

git add docs/productization/D1_REVIEW_FIX_NOTES.md
git commit -m "docs: D1 R-fix notes"
git push origin codex/v1-productization-d1-review-packet
```
