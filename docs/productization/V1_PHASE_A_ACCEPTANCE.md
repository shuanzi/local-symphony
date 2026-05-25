# v1 真实产品化阶段 A 验收记录

**日期**：2026-05-25  
**对应计划**：`docs/productization/V1_REAL_PRODUCTIZATION_EXECUTION_PLAN.md` 阶段 A  
**阶段目标**：建立真实 Codex 最小可运行闭环，同时保持 fake runner 作为默认、确定性的本地验收路径。

## 1. 验收结论

阶段 A 已按 fixture-gated 最小闭环完成：

- A0 / R17：DB schema version guard 已落地；打开现有 project/app DB 前先只读校验 `schema_meta.schema_version`，diagnostics 暴露 schema version 状态。
- A1 / R2：Codex fixture gate 已落地；支持 `compatibility.json` 元数据、版本解析、happy-path transcript 校验、experimental API fail-closed、post-launch handshake metadata 校验。
- A2 / R1：Runner abstraction 已落地；orchestrator 通过 runner interface 调用 fake 或 codex runner，默认仍为 fake，`SYMPHONY_RUNNER_KIND=codex` 才进入 Codex 路径。
- A3 / R3：Codex process 最小生命周期已落地；可启动 fixture-gated `codex app-server`，设置 workspace cwd、受控 env、stdio JSONL 边界、startup/turn/stall timeout、process group terminate/kill escalation、redacted stderr summary。
- A4 / R4：最小 normalized timeline 已落地；Codex process、handshake、thread、turn、approval placeholder、tool observation、protocol error、process exited 等事件写入 redacted `run_events`，不存 raw Codex payload。
- A5 / R6：missing handoff continuation 已落地；`max_handoff_continuations=0` 直接失败，`=1` 时在同一 Codex process/thread 中发起一次 handoff-only continuation，再根据最终 handoff 结果收口。

## 2. 验收命令

已执行并通过：

```bash
go test -count=1 ./internal/agent ./internal/agent/fake ./internal/agent/codex ./internal/orchestrator ./internal/db ./internal/store ./internal/toolgateway ./internal/review ./internal/observability ./internal/cli
python3 scripts/validate_contracts.py
bash scripts/acceptance-local.sh
```

已执行 opt-in Codex gate 命令：

```bash
SYMPHONY_TEST_CODEX=1 go test -count=1 ./internal/agent/codex -run Integration -v
```

结果：命令通过；当前本机安装的 Codex 版本没有对应 committed fixture，因此 integration test 按预期 `SKIP`，未启动未知协议的真实 `codex app-server`。

## 3. 本阶段新增测试覆盖

- DB schema version：初始化、打开、缺失 schema metadata、unsupported version、diagnostics 状态。
- Codex fixture gate：supported/missing/malformed/drift/experimental metadata、happy-path transcript 消费。
- Codex runner：fixture process 成功、startup timeout、turn timeout、stall timeout、handshake mismatch、prompt 传递、raw payload/stderr 不落库、missing handoff result、同 process/thread continuation。
- Orchestrator runner：默认 fake、opt-in codex unsupported fail-closed、fixture Codex 完整进入 Human Review、Codex missing handoff continuation 进入 Human Review。
- Run cancellation：run 被 operator cancel 后，orchestrator 取消 runner context 并终止 Codex process group。
- Missing handoff continuation：0 次直接失败、1 次 retry 后失败、1 次 retry 后成功、`after_run` 只在最终 runner 结果后执行。

## 4. 已知边界

- 当前只提交 `0.0.0-test` fixture；真实本机 Codex 版本未提交 fixture 时必须 fail-closed 或在 opt-in integration 中 skip。
- 本阶段只覆盖无需真实 operator approval 的最小 Codex run；`approval.requested` / `approval.resolved` 只有 redacted timeline placeholder，command/file/network/protected-path approval bridge 属于阶段 B。
- Codex transport 当前按已提交 fixture 的 JSONL 边界实现，不声称支持未提交 fixture 的真实协议变体。
- Stderr 仅以 redacted summary 事件记录；完整 diagnostics artifact 与更细粒度来源聚合继续在后续阶段扩展。

## 5. 后续入口

阶段 B 可在当前基础上继续推进 Approval 与安全策略闭环，重点是 Codex approval request producer、operator 决策 writeback、policy evaluator 与 dashboard/CLI/API 一致性。
